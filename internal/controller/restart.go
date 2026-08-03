package controller

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// restartAnnotatedWorkloads bumps restartedAt on every Deployment/StatefulSet/DaemonSet
// in namespace that carries an OTel inject annotation, forcing a rollout so the
// operator webhook can inject (or drop) auto-instrumentation against the current
// Instrumentation CR.
func restartAnnotatedWorkloads(ctx context.Context, kube kubernetes.Interface, namespace string) {
	if namespace == "" || kube == nil {
		return
	}
	stamp := time.Now().Format(time.RFC3339)
	body, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]string{
						"kubectl.kubernetes.io/restartedAt": stamp,
					},
				},
			},
		},
	})
	if err != nil {
		return
	}

	restarted := 0
	deps, err := kube.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, dep := range deps.Items {
			if !hasInjectAnnotation(dep.Spec.Template.Annotations) {
				continue
			}
			if _, err := kube.AppsV1().Deployments(namespace).Patch(ctx, dep.Name, types.MergePatchType, body, metav1.PatchOptions{}); err != nil {
				log.Printf("[controller/restart] patch Deployment %s/%s: %v", namespace, dep.Name, err)
				continue
			}
			restarted++
		}
	}
	stsList, err := kube.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, sts := range stsList.Items {
			if !hasInjectAnnotation(sts.Spec.Template.Annotations) {
				continue
			}
			if _, err := kube.AppsV1().StatefulSets(namespace).Patch(ctx, sts.Name, types.MergePatchType, body, metav1.PatchOptions{}); err != nil {
				log.Printf("[controller/restart] patch StatefulSet %s/%s: %v", namespace, sts.Name, err)
				continue
			}
			restarted++
		}
	}
	dsList, err := kube.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, ds := range dsList.Items {
			if !hasInjectAnnotation(ds.Spec.Template.Annotations) {
				continue
			}
			if _, err := kube.AppsV1().DaemonSets(namespace).Patch(ctx, ds.Name, types.MergePatchType, body, metav1.PatchOptions{}); err != nil {
				log.Printf("[controller/restart] patch DaemonSet %s/%s: %v", namespace, ds.Name, err)
				continue
			}
			restarted++
		}
	}
	if restarted > 0 {
		log.Printf("[controller] restarted %d annotated workload(s) in namespace %s after instrumentation change", restarted, namespace)
	}
}

// stripInjectAnnotationsInNamespace removes OTel inject annotations from all
// workloads in namespace (used when namespace instrumentation is disabled).
func stripInjectAnnotationsInNamespace(ctx context.Context, kube kubernetes.Interface, namespace string) bool {
	if namespace == "" || kube == nil {
		return false
	}
	changed := false

	deps, err := kube.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range deps.Items {
			if stripInjectOnTemplate(ctx, kube, "Deployment", namespace, deps.Items[i].Name, deps.Items[i].Spec.Template.Annotations) {
				changed = true
			}
		}
	}
	stsList, err := kube.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range stsList.Items {
			if stripInjectOnTemplate(ctx, kube, "StatefulSet", namespace, stsList.Items[i].Name, stsList.Items[i].Spec.Template.Annotations) {
				changed = true
			}
		}
	}
	dsList, err := kube.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range dsList.Items {
			if stripInjectOnTemplate(ctx, kube, "DaemonSet", namespace, dsList.Items[i].Name, dsList.Items[i].Spec.Template.Annotations) {
				changed = true
			}
		}
	}
	return changed
}

func stripInjectOnTemplate(ctx context.Context, kube kubernetes.Interface, kind, namespace, name string, annotations map[string]string) bool {
	if !hasInjectAnnotation(annotations) {
		return false
	}
	patch := map[string]interface{}{
		"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339),
	}
	for k := range annotations {
		if strings.HasPrefix(k, injectPrefix) || k == goTargetExeAnnotation || strings.HasPrefix(k, "instrumentation.opentelemetry.io/") {
			patch[k] = nil
		}
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
		return false
	}
	var perr error
	switch kind {
	case "StatefulSet":
		_, perr = kube.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{})
	case "DaemonSet":
		_, perr = kube.AppsV1().DaemonSets(namespace).Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{})
	default:
		_, perr = kube.AppsV1().Deployments(namespace).Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{})
	}
	if perr != nil {
		log.Printf("[controller] strip inject annotations on %s %s/%s: %v", kind, namespace, name, perr)
		return false
	}
	return true
}

func hasInjectAnnotation(annotations map[string]string) bool {
	for k := range annotations {
		if strings.HasPrefix(k, injectPrefix) {
			return true
		}
	}
	return false
}
