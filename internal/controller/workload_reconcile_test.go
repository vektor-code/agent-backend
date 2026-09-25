package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestGuessGoTargetExeFromContainerName(t *testing.T) {
	template := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "reverse-proxy",
				Image: "maderovs/reverse-proxy:latest",
			}},
		},
	}

	if got := guessGoTargetExe(template); got != "/app/reverse-proxy" {
		t.Fatalf("guessGoTargetExe() = %q, want /app/reverse-proxy", got)
	}
}

func TestGuessGoTargetExeSkipsShellWrappers(t *testing.T) {
	template := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "app",
				Image:   "example/payments:1",
				Command: []string{"/bin/sh", "-c"},
				Args:    []string{"/usr/local/bin/payments"},
			}},
		},
	}
	if got := guessGoTargetExe(template); got != "/usr/local/bin/payments" {
		t.Fatalf("guessGoTargetExe() = %q, want /usr/local/bin/payments", got)
	}
}

func TestClearOTelEnvPatchStrategicDeletes(t *testing.T) {
	template := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "www",
				Env: []corev1.EnvVar{
					{Name: "OTEL_SERVICE_NAME", Value: "billing"},
					{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "http://agent:4318"},
					{Name: "DATABASE_URL", Value: "postgres://localhost"},
				},
			}},
		},
	}
	patches := clearOTelEnvPatch(template)
	if len(patches) != 1 {
		t.Fatalf("got %d container patches, want 1", len(patches))
	}
	env, ok := patches[0]["env"].([]map[string]interface{})
	if !ok {
		t.Fatalf("env patch type = %T", patches[0]["env"])
	}
	if len(env) != 2 {
		t.Fatalf("got %d env deletes, want 2", len(env))
	}
	for _, e := range env {
		if e["$patch"] != "delete" {
			t.Fatalf("expected strategic delete, got %#v", e)
		}
		if !strings.HasPrefix(e["name"].(string), "OTEL_") {
			t.Fatalf("non-OTEL env marked for delete: %#v", e)
		}
	}
}

func TestNormalizeInjectLangPHPAndRubyUseSDK(t *testing.T) {
	if got := normalizeInjectLang("php"); got != "sdk" {
		t.Fatalf("php inject = %q, want sdk", got)
	}
	if got := normalizeInjectLang("ruby"); got != "sdk" {
		t.Fatalf("ruby inject = %q, want sdk", got)
	}
	if got := normalizeInjectLang("apache"); got != "apache-httpd" {
		t.Fatalf("apache inject = %q, want apache-httpd", got)
	}
}
