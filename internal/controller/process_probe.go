package controller

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	processProbeMaxPerCycle  = 40
	processProbeConcurrency  = 8
	processProbeTimeout      = 2 * time.Second
	processProbeFailBackoff  = 5 * time.Minute
)

type imageCacheEntry struct {
	cmdline   string
	lang      string
	failUntil time.Time
}

type processLangCache struct {
	mu         sync.RWMutex
	byImageID  map[string]imageCacheEntry
	byWorkload map[string]string
}

// processLangByWorkload holds process-detected runtimes for opaque images.
// Reconcile reads it when pod templates lack command/args.
var processLangByWorkload = &processLangCache{
	byImageID:  make(map[string]imageCacheEntry),
	byWorkload: make(map[string]string),
}

func getProcessLangByWorkload(namespace, workloadName string) string {
	if namespace == "" || workloadName == "" {
		return ""
	}
	processLangByWorkload.mu.RLock()
	defer processLangByWorkload.mu.RUnlock()
	// Prefer exact Deployment/StatefulSet name (what reconcile looks up).
	if lang := processLangByWorkload.byWorkload[namespace+"/"+workloadName]; lang != "" {
		return lang
	}
	// Fallbacks when the cache was keyed by app label only.
	for key, lang := range processLangByWorkload.byWorkload {
		if lang == "" || !strings.HasPrefix(key, namespace+"/") {
			continue
		}
		name := strings.TrimPrefix(key, namespace+"/")
		if name == workloadName || strings.HasPrefix(workloadName, name+"-") || strings.HasPrefix(name, workloadName+"-") {
			return lang
		}
	}
	return ""
}

func (c *processLangCache) cachedCmdline(imageID string) (cmdline string, ok bool) {
	if imageID == "" {
		return "", false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, found := c.byImageID[imageID]
	if !found || e.cmdline == "" {
		return "", false
	}
	if e.failUntil.After(time.Now()) && e.lang == "" {
		return "", false
	}
	return e.cmdline, true
}

func (c *processLangCache) backedOff(imageID string) bool {
	if imageID == "" {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.byImageID[imageID]
	return ok && e.failUntil.After(time.Now()) && e.lang == ""
}

func (c *processLangCache) store(imageID string, workloadKeys []string, cmdline, lang string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if imageID != "" {
		c.byImageID[imageID] = imageCacheEntry{cmdline: cmdline, lang: lang}
	}
	if lang == "" {
		return
	}
	for _, workloadKey := range workloadKeys {
		if workloadKey != "" {
			c.byWorkload[workloadKey] = lang
		}
	}
}

func (c *processLangCache) markFailed(imageID string) {
	if imageID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byImageID[imageID] = imageCacheEntry{failUntil: time.Now().Add(processProbeFailBackoff)}
}

func podReady(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// needsProcessProbe reports whether we should read /proc/1/cmdline.
// Like Datadog's process agent: always observe Running+Ready app pods;
// ImageID cache avoids re-exec on every cycle.
func needsProcessProbe(pod *corev1.Pod) bool {
	return pod != nil && pod.Status.Phase == corev1.PodRunning && podReady(pod)
}

func appContainerForPod(pod *corev1.Pod) (string, bool) {
	apps := appContainers(pod.Spec.Containers)
	if len(apps) == 0 {
		return "", false
	}
	return apps[0].Name, true
}

func imageIDForContainer(pod *corev1.Pod, containerName string) string {
	for _, st := range pod.Status.ContainerStatuses {
		if st.Name == containerName {
			return st.ImageID
		}
	}
	return ""
}

// workloadKeysFromPod returns every cache key reconcile might look up:
// owner Deployment/StatefulSet/DaemonSet name plus common app labels.
func workloadKeysFromPod(pod *corev1.Pod) []string {
	if pod == nil || pod.Namespace == "" {
		return nil
	}
	seen := map[string]bool{}
	var keys []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		k := pod.Namespace + "/" + name
		if seen[k] {
			return
		}
		seen[k] = true
		keys = append(keys, k)
	}
	for _, o := range pod.OwnerReferences {
		ctrl := o.Controller != nil && *o.Controller
		if !ctrl {
			continue
		}
		switch o.Kind {
		case "ReplicaSet":
			add(deploymentNameFromReplicaSet(o.Name))
		case "StatefulSet", "DaemonSet":
			add(o.Name)
		}
	}
	if pod.Labels != nil {
		for _, key := range []string{"app.kubernetes.io/name", "app", "service", "k8s-app"} {
			add(pod.Labels[key])
		}
	}
	add(serviceNameFromPodName(pod.Name))
	return keys
}

// deploymentNameFromReplicaSet strips the pod-template-hash suffix
// (e.g. app-frontend-6dd44fbd75 → app-frontend).
func deploymentNameFromReplicaSet(rsName string) string {
	i := strings.LastIndex(rsName, "-")
	if i <= 0 {
		return rsName
	}
	suf := rsName[i+1:]
	if len(suf) < 5 || len(suf) > 10 {
		return rsName
	}
	for _, r := range suf {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return rsName
		}
	}
	return rsName[:i]
}

func serviceNameFromPodName(name string) string {
	if name == "" {
		return ""
	}
	parts := strings.Split(name, "-")
	if len(parts) >= 3 {
		return strings.Join(parts[:len(parts)-2], "-")
	}
	return name
}

func probeProcessCmdlines(ctx context.Context, clients *controllerClients, pods []corev1.Pod) map[string]string {
	type probeJob struct {
		podKey    string
		pod       *corev1.Pod
		container string
		imageID   string
		wKeys     []string
	}

	results := make(map[string]string)
	var jobs []probeJob
	var candidates []probeJob
	for i := range pods {
		pod := &pods[i]
		if !isAppNamespace(pod.Namespace) || !needsProcessProbe(pod) {
			continue
		}
		container, ok := appContainerForPod(pod)
		if !ok {
			continue
		}
		imageID := imageIDForContainer(pod, container)
		podKey := pod.Namespace + "/" + pod.Name
		if cmdline, hit := processLangByWorkload.cachedCmdline(imageID); hit {
			results[podKey] = cmdline
			continue
		}
		if processLangByWorkload.backedOff(imageID) {
			continue
		}
		candidates = append(candidates, probeJob{
			podKey: podKey, pod: pod, container: container, imageID: imageID, wKeys: workloadKeysFromPod(pod),
		})
	}
	// Rotate so we don't starve pods beyond the per-cycle cap.
	if n := len(candidates); n > processProbeMaxPerCycle {
		offset := int(time.Now().Unix()/10) % n
		rotated := make([]probeJob, 0, n)
		rotated = append(rotated, candidates[offset:]...)
		rotated = append(rotated, candidates[:offset]...)
		candidates = rotated
	}
	if len(candidates) > processProbeMaxPerCycle {
		jobs = candidates[:processProbeMaxPerCycle]
	} else {
		jobs = candidates
	}

	var mu sync.Mutex
	sem := make(chan struct{}, processProbeConcurrency)
	var wg sync.WaitGroup

	for _, job := range jobs {
		wg.Add(1)
		go func(job probeJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cmdline, err := execProcessCmdline(ctx, clients.config, clients.kube, job.pod, job.container)
			mu.Lock()
			defer mu.Unlock()
			if err != nil || strings.TrimSpace(cmdline) == "" {
				processLangByWorkload.markFailed(job.imageID)
				return
			}
			lang := languageFromProcessCmdline(cmdline)
			processLangByWorkload.store(job.imageID, job.wKeys, cmdline, lang)
			results[job.podKey] = cmdline
		}(job)
	}
	wg.Wait()
	return results
}

func execProcessCmdline(ctx context.Context, config *rest.Config, kube kubernetes.Interface, pod *corev1.Pod, container string) (string, error) {
	if config == nil || kube == nil || pod == nil {
		return "", nil
	}
	for _, argv := range [][]string{
		{"cat", "/proc/1/cmdline"},
		{"/bin/sh", "-c", "tr '\\0' ' ' </proc/1/cmdline"},
	} {
		out, err := podExec(ctx, config, kube, pod.Namespace, pod.Name, container, argv)
		if err == nil && strings.TrimSpace(out) != "" {
			return out, nil
		}
	}
	return "", nil
}

func podExec(ctx context.Context, config *rest.Config, kube kubernetes.Interface, namespace, podName, container string, argv []string) (string, error) {
	req := kube.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   argv,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(config, http.MethodPost, req.URL())
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	execCtx, cancel := context.WithTimeout(ctx, processProbeTimeout)
	defer cancel()
	err = executor.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return "", err
	}
	return stdout.String(), nil
}
