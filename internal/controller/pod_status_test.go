package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodStatusHintImagePull(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "api",
				Ready: false,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{
						Reason:  "ImagePullBackOff",
						Message: `Back-off pulling image "registry/api:dev-200"`,
					},
				},
			}},
		},
	}
	reason, msg := podStatusHint(pod)
	if reason != "ImagePullBackOff" {
		t.Fatalf("reason = %q, want ImagePullBackOff", reason)
	}
	if !strings.Contains(msg, "registry/api") {
		t.Fatalf("message = %q, want image pull detail", msg)
	}
}

func TestPodStatusHintReadyClears(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "api",
				Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
	if reason, _ := podStatusHint(pod); reason != "" {
		t.Fatalf("reason = %q, want empty for Ready pod", reason)
	}
}

func TestPodStatusHintPrefersImagePullOverPending(t *testing.T) {
	gotR, _ := pickWorstStatus("Pending", "", "ImagePullBackOff", "not found")
	if gotR != "ImagePullBackOff" {
		t.Fatalf("got %q, want ImagePullBackOff", gotR)
	}
}

func TestEnrichWorkloadLanguagesCopiesStatusHint(t *testing.T) {
	workloads := []ReportedWorkload{
		{Name: "api", Namespace: "dev", Kind: "Deployment", Replicas: 1, Ready: 0},
	}
	pods := []ReportedPod{
		{
			Name:          "api-abc-xyz",
			Namespace:     "dev",
			StatusReason:  "ImagePullBackOff",
			StatusMessage: "image not found",
		},
	}
	got := enrichWorkloadLanguages(workloads, pods)
	if got[0].StatusReason != "ImagePullBackOff" {
		t.Fatalf("StatusReason = %q, want ImagePullBackOff", got[0].StatusReason)
	}
	if got[0].StatusMessage != "image not found" {
		t.Fatalf("StatusMessage = %q", got[0].StatusMessage)
	}
}

func TestPodStatusHintFallsBackToPhase(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Status: corev1.PodStatus{
			Phase:   corev1.PodPending,
			Message: "waiting for image",
		},
	}
	reason, msg := podStatusHint(pod)
	if reason != "Pending" {
		t.Fatalf("reason = %q, want Pending", reason)
	}
	if msg != "waiting for image" {
		t.Fatalf("message = %q", msg)
	}
}
