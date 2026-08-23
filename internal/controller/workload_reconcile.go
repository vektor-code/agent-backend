package controller

import (
	"context"
	"encoding/json"
	"fmt"
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
	case "php", "ruby", "rails":
		// Operator 0.58 has no inject-php/inject-ruby. inject-sdk sets OTLP
		// env (Datadog SSI / OTel docs pattern); we add language env below.
		return "sdk"
	case "nginx":
		return "nginx"
	case "apache", "httpd", "apache-httpd", "apachehttpd":
		return "apache-httpd"
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
		origLang := strings.ToLower(strings.TrimSpace(w.Language))
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
		patchWorkloadAnnotation(ctx, kube, w, origLang, lang, desiredKey, instName)
	}
}

// patchWorkloadAnnotation ensures the workload's pod template carries exactly the
// desired inject annotation (or none), removing any stale inject-* keys.
func patchWorkloadAnnotation(ctx context.Context, kube kubernetes.Interface, w workloadConfig, origLang, lang, desiredKey, instName string) {
	template, ok := getPodTemplate(ctx, kube, w)
	if !ok {
		return
	}
	current := template.Annotations
	if current == nil {
		current = map[string]string{}
	}

	patch := map[string]interface{}{}
	for k := range current {
		if strings.HasPrefix(k, injectPrefix) && k != desiredKey {
			patch[k] = nil
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

	var containers []map[string]interface{}
	if w.Enabled && (lang == "sdk" || origLang == "php" || origLang == "ruby" || origLang == "rails") {
		containers = otelLibraryEnvPatch(template, origLang)
	}

	if len(patch) == 0 && len(containers) == 0 {
		return
	}

	tpl := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": patch,
		},
	}
	if len(containers) > 0 {
		tpl["spec"] = map[string]interface{}{"containers": containers}
	}
	body, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{"template": tpl},
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
	if template == nil || len(template.Spec.Containers) == 0 {
		return "/app"
	}
	c := template.Spec.Containers[0]
	if path := firstProcessPath(append(append([]string{}, c.Command...), c.Args...)); path != "" {
		return path
	}
	if name := executableName(c.Name); name != "" && !genericProcessName(name) {
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
	if name := executableName(imageName); name != "" && !genericProcessName(name) {
		return "/" + name
	}
	return "/app"
}

func firstProcessPath(parts []string) string {
	wrappers := map[string]bool{
		"sh": true, "bash": true, "ash": true, "dash": true, "busybox": true,
		"/bin/sh": true, "/bin/bash": true, "/bin/ash": true,
		"entrypoint.sh": true, "docker-entrypoint.sh": true,
		"dumb-init": true, "tini": true, "env": true, "/usr/bin/env": true,
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "-") {
			continue
		}
		base := part
		if i := strings.LastIndex(part, "/"); i >= 0 {
			base = part[i+1:]
		}
		if wrappers[part] || wrappers[base] {
			continue
		}
		if strings.HasPrefix(part, "/") {
			return part
		}
		if strings.HasPrefix(part, "./") {
			return "/" + strings.TrimPrefix(part, "./")
		}
	}
	return ""
}

func genericProcessName(name string) bool {
	switch strings.ToLower(name) {
	case "app", "server", "web", "main", "container", "workload", "service":
		return true
	}
	return false
}

func otelLibraryEnvPatch(template *corev1.PodTemplateSpec, lang string) []map[string]interface{} {
	if template == nil || len(template.Spec.Containers) == 0 {
		return nil
	}
	endpoint := fmt.Sprintf("http://agent-backend.%s.svc.cluster.local:4318", currentAgentNamespace())
	out := make([]map[string]interface{}, 0, len(template.Spec.Containers))
	for _, c := range template.Spec.Containers {
		env := []map[string]string{
			{"name": "OTEL_EXPORTER_OTLP_ENDPOINT", "value": endpoint},
			{"name": "OTEL_EXPORTER_OTLP_PROTOCOL", "value": "http/protobuf"},
			{"name": "OTEL_TRACES_EXPORTER", "value": "otlp"},
			{"name": "OTEL_METRICS_EXPORTER", "value": "none"},
			{"name": "OTEL_LOGS_EXPORTER", "value": "none"},
			{"name": "OTEL_SERVICE_NAME", "value": c.Name},
		}
		if lang == "php" {
			env = append(env, map[string]string{"name": "OTEL_PHP_AUTOLOAD_ENABLED", "value": "true"})
		}
		out = append(out, map[string]interface{}{
			"name": c.Name,
			"env":  env,
		})
	}
	return out
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
