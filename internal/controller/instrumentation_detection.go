package controller

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

func podHasInstrumentationAnnotation(pod *corev1.Pod) bool {
	if pod.Annotations == nil {
		return false
	}
	for key, value := range pod.Annotations {
		if strings.HasPrefix(key, "instrumentation.opentelemetry.io/inject-") && value != "" {
			return true
		}
	}
	return false
}

func getPodInstrumentationStatus(pod *corev1.Pod) (bool, string, string) {
	if pod.Annotations != nil {
		for key, value := range pod.Annotations {
			if strings.Contains(key, "inject-") && value != "" {
				parts := strings.Split(key, "inject-")
				if len(parts) == 2 {
					return true, parts[1], fmt.Sprintf("Injected via annotation: %s=%s", key, value)
				}
			}
		}
	}

	for _, c := range pod.Spec.Containers {
		for _, env := range c.Env {
			if strings.Contains(env.Name, "OTEL_PHP_AUTOLOAD_ENABLED") {
				return true, "php", "Instrumented via PHP custom loader injection"
			}
			if strings.Contains(env.Name, "JAVA_TOOL_OPTIONS") && strings.Contains(env.Value, "opentelemetry") {
				return true, "java", "Instrumented via JAVA_TOOL_OPTIONS"
			}
			if strings.Contains(env.Name, "NODE_OPTIONS") && strings.Contains(env.Value, "opentelemetry") {
				return true, "nodejs", "Instrumented via NODE_OPTIONS"
			}
		}
	}

	return false, "", "No instrumentation injection detected. Click Enable in Admin to inject agent."
}
