package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetPodInstrumentationStatusFromOTLPEndpoint(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Env: []corev1.EnvVar{{
					Name:  "OTEL_EXPORTER_OTLP_ENDPOINT",
					Value: "http://agent-backend.crnet-apm.svc:4318",
				}},
			}},
		},
	}
	ok, kind, _ := getPodInstrumentationStatus(pod)
	if !ok || kind != "env" {
		t.Fatalf("OTLP endpoint: ok=%v kind=%q", ok, kind)
	}
}

func TestGetPodInstrumentationStatusIgnoresDisabledInject(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"instrumentation.opentelemetry.io/inject-java": "false",
			},
		},
	}
	ok, _, _ := getPodInstrumentationStatus(pod)
	if ok {
		t.Fatal("inject-*=false should not count as instrumented")
	}
}

func TestInstrumentationSpecRejected(t *testing.T) {
	if instrumentationSpecRejected(nil) {
		t.Fatal("nil should not be rejected")
	}
}
