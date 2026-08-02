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

	if got := guessGoTargetExe(template); got != "/reverse-proxy" {
		t.Fatalf("guessGoTargetExe() = %q, want /reverse-proxy", got)
	}
}
