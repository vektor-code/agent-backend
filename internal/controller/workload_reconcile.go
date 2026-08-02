package controller

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const injectPrefix = "instrumentation.opentelemetry.io/inject-"
const goTargetExeAnnotation = "instrumentation.opentelemetry.io/otel-go-auto-target-exe"

// normalizeInjectLang maps a user-chosen stack to the operator's inject suffix.
// Returns "" for unsupported/blank languages so we never annotate blindly.
func normalizeInjectLang(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "java":
		return "java"
	case "nodejs", "node", "javascript", "typescript":
		return "nodejs"
	case "python":
		return "python"
	case "go", "golang":
		return "go"
	case "dotnet", ".net", "csharp":
		return "dotnet"
	case "php":
		return "php"
	default:
		return ""
	}
}

// reconcileWorkloadInstrumentation applies the operator's per-service choices:
// it annotates the exact workload (with the chosen language) so the OpenTelemetry
// operator injects it, and strips the annotation when disabled. It is idempotent —
// a workload is only patched when its inject annotation actually needs to change,
// so steady state never triggers a rollout.
func reconcileWorkloadInstrumentation(ctx context.Context, kube kubernetes.Interface, workloads []workloadConfig) {
	for _, w := range workloads {
		if w.Namespace == "" || w.WorkloadName == "" {
			continue
		}
		lang := normalizeInjectLang(w.Language)
		if w.Enabled && lang == "" {
			log.Printf("[controller/workload] skip %s/%s: unsupported language %q", w.Namespace, w.WorkloadName, w.Language)
			continue
		}
		desiredKey := ""
		if w.Enabled {
			desiredKey = injectPrefix + lang
		}
		instName := instrumentationName(w.Namespace)
		patchWorkloadAnnotation(ctx, kube, w, desiredKey, instName)
	}
}

// patchWorkloadAnnotation ensures the workload's pod template carries exactly the
// desired inject annotation (or none), removing any stale inject-* keys.
func patchWorkloadAnnotation(ctx context.Context, kube kubernetes.Interface, w workloadConfig, desiredKey, instName string) {
	template, ok := getPodTemplate(ctx, kube, w)
	if !ok {
		return
	}
	current := template.Annotations
	if current == nil {
		current = map[string]string{}
	}

	// Desired end state: only desiredKey (if any) among the inject-* keys.
	patch := map[string]interface{}{}
	for k := range current {
		if strings.HasPrefix(k, injectPrefix) && k != desiredKey {
			patch[k] = nil // remove stale / other-language inject annotations
		}
	}
	if desiredKey != "" && current[desiredKey] != instName {
		patch[desiredKey] = instName
	}
	if desiredKey == injectPrefix+"go" {
		if target := current[goTargetExeAnnotation]; target == "" || target == "/app" {
			patch[goTargetExeAnnotation] = guessGoTargetExe(template)
		}
	} else if _, ok := current[goTargetExeAnnotation]; ok {
		patch[goTargetExeAnnotation] = nil
	}
	if len(patch) == 0 {
		return // already in the desired state — no rollout
	}

	body, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": patch,
				},
			},
		},
	})
	if err != nil {
		return
	}

	kind := w.WorkloadKind
	if kind == "" {
		kind = "Deployment"
	}
	var perr error
	switch kind {
	case "StatefulSet":
		_, perr = kube.AppsV1().StatefulSets(w.Namespace).Patch(ctx, w.WorkloadName, types.MergePatchType, body, metav1.PatchOptions{})
	case "DaemonSet":
		_, perr = kube.AppsV1().DaemonSets(w.Namespace).Patch(ctx, w.WorkloadName, types.MergePatchType, body, metav1.PatchOptions{})
	default:
		_, perr = kube.AppsV1().Deployments(w.Namespace).Patch(ctx, w.WorkloadName, types.MergePatchType, body, metav1.PatchOptions{})
	}
	if perr != nil {
		log.Printf("[controller/workload/error] patch %s %s/%s: %v", kind, w.Namespace, w.WorkloadName, perr)
		return
	}
	action := "enabled"
	if desiredKey == "" {
		action = "disabled"
	}
	log.Printf("[controller/workload] %s instrumentation on %s %s/%s", action, kind, w.Namespace, w.WorkloadName)
}

// getPodTemplate returns the workload's pod template.
func getPodTemplate(ctx context.Context, kube kubernetes.Interface, w workloadConfig) (*corev1.PodTemplateSpec, bool) {
	kind := w.WorkloadKind
	if kind == "" {
		kind = "Deployment"
	}
	switch kind {
	case "StatefulSet":
		ss, err := kube.AppsV1().StatefulSets(w.Namespace).Get(ctx, w.WorkloadName, metav1.GetOptions{})
		if err != nil {
			return nil, false
		}
		return &ss.Spec.Template, true
	case "DaemonSet":
		ds, err := kube.AppsV1().DaemonSets(w.Namespace).Get(ctx, w.WorkloadName, metav1.GetOptions{})
		if err != nil {
			return nil, false
		}
		return &ds.Spec.Template, true
	default:
		dep, err := kube.AppsV1().Deployments(w.Namespace).Get(ctx, w.WorkloadName, metav1.GetOptions{})
		if err != nil {
			return nil, false
		}
		return &dep.Spec.Template, true
	}
}

func guessGoTargetExe(template *corev1.PodTemplateSpec) string {
	if len(template.Spec.Containers) == 0 {
		return "/app"
	}
	c := template.Spec.Containers[0]
	if len(c.Command) > 0 && strings.HasPrefix(c.Command[0], "/") {
		return c.Command[0]
	}
	if len(c.Args) > 0 && strings.HasPrefix(c.Args[0], "/") {
		return c.Args[0]
	}
	if name := executableName(c.Name); name != "" {
		return "/" + name
	}
	imageName := c.Image
	if slash := strings.LastIndex(imageName, "/"); slash >= 0 {
		imageName = imageName[slash+1:]
	}
	if colon := strings.LastIndex(imageName, ":"); colon >= 0 {
		imageName = imageName[:colon]
	}
	if at := strings.LastIndex(imageName, "@"); at >= 0 {
		imageName = imageName[:at]
	}
	if name := executableName(imageName); name != "" {
		return "/" + name
	}
	return "/app"
}

func executableName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "/")
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, " \t\n") {
		return ""
	}
	return value
}
