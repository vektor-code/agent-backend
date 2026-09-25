package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func int32Ptr(v int32) *int32 { return &v }

func TestReportedWorkloadFromDeploymentUsesReadyReplicas(t *testing.T) {
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api"},
		Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(3)},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 2},
	}
	got := reportedWorkloadFromDeployment(d, "dev")
	if got.Kind != "Deployment" || got.Namespace != "dev" || got.Replicas != 3 || got.Ready != 2 {
		t.Fatalf("got %+v, want Deployment dev 3/2", got)
	}
}

func TestReportedWorkloadFromDeploymentDefaultsNilReplicasToOne(t *testing.T) {
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api"},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
	if got := reportedWorkloadFromDeployment(d, "dev"); got.Replicas != 1 {
		t.Fatalf("Replicas = %d, want 1 (unset spec.replicas defaults to 1)", got.Replicas)
	}
}

func TestReportedWorkloadFromStatefulSet(t *testing.T) {
	s := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db"},
		Spec:       appsv1.StatefulSetSpec{Replicas: int32Ptr(2)},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: 1},
	}
	got := reportedWorkloadFromStatefulSet(s, "dev")
	if got.Kind != "StatefulSet" || got.Replicas != 2 || got.Ready != 1 {
		t.Fatalf("got %+v, want StatefulSet 2/1", got)
	}
}

func TestReportedWorkloadFromDaemonSetUsesDesiredScheduled(t *testing.T) {
	d := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "logger"},
		Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: 4, NumberReady: 3},
	}
	got := reportedWorkloadFromDaemonSet(d, "dev")
	if got.Kind != "DaemonSet" || got.Replicas != 4 || got.Ready != 3 {
		t.Fatalf("got %+v, want DaemonSet 4/3", got)
	}
}

func TestEnrichWorkloadLanguagesCopiesFromOwnedPods(t *testing.T) {
	workloads := []ReportedWorkload{
		{Name: "api", Namespace: "dev", Kind: "Deployment", Replicas: 2, Ready: 2},
		{Name: "worker", Namespace: "dev", Kind: "Deployment", Replicas: 1, Ready: 1},
	}
	pods := []ReportedPod{
		{Name: "api-abc123-xyz", Namespace: "dev", Language: "java", Instrumented: true},
		{Name: "api-other-pod", Namespace: "prod", Language: "python"},
	}

	got := enrichWorkloadLanguages(workloads, pods)
	if got[0].Language != "java" {
		t.Fatalf("api language = %q, want java", got[0].Language)
	}
	if !got[0].Instrumented {
		t.Fatal("api should be marked instrumented")
	}
	if got[1].Language != "" {
		t.Fatalf("worker language = %q, want empty (no matching pod)", got[1].Language)
	}
}

func TestEnrichWorkloadLanguagesIgnoresUnrelatedPodNames(t *testing.T) {
	workloads := []ReportedWorkload{{Name: "api", Namespace: "dev"}}
	pods := []ReportedPod{{Name: "apiserver-1", Namespace: "dev", Language: "go"}}

	if got := enrichWorkloadLanguages(workloads, pods); got[0].Language != "" {
		t.Fatalf("Language = %q, want empty (apiserver is a different workload)", got[0].Language)
	}
}
