package controller

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const otelInjectPrefix = "instrumentation.opentelemetry.io/inject-"

func podHasInstrumentationAnnotation(pod *corev1.Pod) bool {
	if pod.Annotations == nil {
		return false
	}
	for key, value := range pod.Annotations {
		if strings.HasPrefix(key, otelInjectPrefix) && value != "" && value != "false" {
			return true
		}
	}
	return false
}

func getPodInstrumentationStatus(pod *corev1.Pod) (bool, string, string) {
	if pod.Annotations != nil {
		for key, value := range pod.Annotations {
			if strings.HasPrefix(key, otelInjectPrefix) && value != "" && value != "false" {
				lang := strings.TrimPrefix(key, otelInjectPrefix)
				return true, lang, fmt.Sprintf("Injected via annotation: %s=%s", key, value)
			}
		}
	}

	for _, c := range pod.Spec.Containers {
		for _, env := range c.Env {
			switch env.Name {
			case "OTEL_PHP_AUTOLOAD_ENABLED":
				if env.Value == "true" {
					return true, "php", "Instrumented via PHP custom loader injection"
				}
			case "OTEL_TRACES_EXPORTER":
				if env.Value != "" && env.Value != "none" {
					return true, "env", fmt.Sprintf("OTEL_TRACES_EXPORTER=%s", env.Value)
				}
			case "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":
				if env.Value != "" {
					return true, "env", fmt.Sprintf("%s is set", env.Name)
				}
			case "JAVA_TOOL_OPTIONS":
				lower := strings.ToLower(env.Value)
				if strings.Contains(lower, "opentelemetry") || strings.Contains(env.Value, "otel") {
					return true, "java", "Instrumented via JAVA_TOOL_OPTIONS"
				}
			case "NODE_OPTIONS":
				if strings.Contains(strings.ToLower(env.Value), "opentelemetry") {
					return true, "nodejs", "Instrumented via NODE_OPTIONS"
				}
			case "PYTHONPATH":
				if strings.Contains(strings.ToLower(env.Value), "opentelemetry") {
					return true, "python", "Instrumented via PYTHONPATH"
				}
			}
		}
	}

	return false, "", "No instrumentation injection detected. Click Enable in Admin to inject agent."
}
