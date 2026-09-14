package controller

import (
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
