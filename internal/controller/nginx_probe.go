package controller

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ResolveNginxVersion returns the best-known nginx version from container spec fields,
// optionally probing a Ready pod with nginx -v when the version is still unknown.
func ResolveNginxVersion(ctx context.Context, kube kubernetes.Interface, config *rest.Config, namespace string, template *corev1.PodTemplateSpec) string {
	if template == nil {
		return ""
	}
	if v := resolveNginxVersionFromContainers(template.Spec.Containers); v != "" {
		return v
	}
	if config == nil || kube == nil || namespace == "" {
		return ""
	}
	return probeNginxVersion(ctx, config, kube, namespace, template)
}

func probeNginxVersion(ctx context.Context, config *rest.Config, kube kubernetes.Interface, namespace string, template *corev1.PodTemplateSpec) string {
	if template == nil || len(template.Labels) == 0 {
		return ""
	}
	sel := labels.Set(template.Labels).AsSelector().String()
	pods, err := kube.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return ""
	}
	for _, pod := range pods.Items {
		if !podIsReady(&pod) {
			continue
		}
		for _, c := range appContainers(pod.Spec.Containers) {
			if !looksLikeNginxContainer(c) {
				continue
			}
			if v := execNginxVersion(ctx, config, kube, pod.Namespace, pod.Name, c.Name); v != "" {
				return v
			}
		}
	}
	return ""
}

func looksLikeNginxContainer(c corev1.Container) bool {
	if ParseNginxVersion(c.Image) != "" {
		return true
	}
	lower := strings.ToLower(c.Image + " " + c.Name)
	return strings.Contains(lower, "nginx") || strings.Contains(lower, "openresty")
}

func execNginxVersion(ctx context.Context, config *rest.Config, kube kubernetes.Interface, namespace, podName, container string) string {
	for _, argv := range [][]string{
		{"nginx", "-v"},
		{"/bin/sh", "-c", "nginx -v 2>&1"},
	} {
		out, err := podExec(ctx, config, kube, namespace, podName, container, argv)
		if err != nil {
			continue
		}
		if v := ParseNginxVersion(strings.TrimSpace(out)); v != "" {
			return v
		}
	}
	return ""
}

func podIsReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
