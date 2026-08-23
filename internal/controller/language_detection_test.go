package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDetectLanguageFromTemurinImage(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-7d9f"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "eclipse-temurin:21-jre",
			}},
		},
	}
	if got := detectLanguageFromPodSpec(pod); got != "java" {
		t.Fatalf("detectLanguageFromPodSpec() = %q, want java", got)
	}
}

func TestDetectNginxEvenWhenFrontend(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Image: "nginx:1.27-alpine",
			}},
		},
	}
	if got := detectLanguageFromPodSpec(pod); got != "nginx" {
		t.Fatalf("detectLanguageFromPodSpec() = %q, want nginx", got)
	}
}

func TestDetectApacheHttpdFromImage(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Image: "httpd:2.4",
			}},
		},
	}
	if got := detectLanguageFromPodSpec(pod); got != "apache-httpd" {
		t.Fatalf("detectLanguageFromPodSpec() = %q, want apache-httpd", got)
	}
}

func TestInjectSDKAnnotationDoesNotUseCRName(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"instrumentation.opentelemetry.io/inject-sdk": "troni-dev-instrumentation",
			},
		},
	}
	if got := detectLanguageFromPodSpec(pod); got != "sdk" {
		t.Fatalf("detectLanguageFromPodSpec() = %q, want sdk", got)
	}
}
