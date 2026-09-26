package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestContainerImagesFromPod(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Image: "busybox:1.36"}},
			Containers: []corev1.Container{
				{Image: "myapp:v1"},
				{Image: "sidecar:latest"},
				{Image: "myapp:v1"},
			},
		},
	}
	got := containerImagesFromPod(pod)
	if len(got) != 2 || got[0] != "myapp:v1" || got[1] != "sidecar:latest" {
		t.Fatalf("got %v, want [myapp:v1 sidecar:latest]", got)
	}
}

func TestReportedNodeCloudFields(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "worker-1",
			Labels: map[string]string{
				"topology.kubernetes.io/zone":     "us-east-1a",
				"topology.kubernetes.io/region":   "us-east-1",
				"node.kubernetes.io/instance-type": "m5.large",
				"eks.amazonaws.com/nodegroup":     "workers",
			},
		},
		Status: corev1.NodeStatus{
			Capacity:    corev1.ResourceList{},
			Allocatable: corev1.ResourceList{},
			NodeInfo: corev1.NodeSystemInfo{
				OperatingSystem:         "linux",
				OSImage:                 "Amazon Linux 2",
				KernelVersion:           "5.10.0",
				Architecture:            "amd64",
				ContainerRuntimeVersion: "containerd://1.7.0",
			},
		},
	}
	got := reportedNodeFromK8sNode(node, nodeMetricsSnapshot{})
	if got.CloudProvider != "aws" || got.Zone != "us-east-1a" || got.Region != "us-east-1" || got.InstanceType != "m5.large" {
		t.Fatalf("got %+v, want aws us-east-1 us-east-1a m5.large", got)
	}
	if got.OsImage != "Amazon Linux 2" || got.Architecture != "amd64" {
		t.Fatalf("os fields = %q %q", got.OsImage, got.Architecture)
	}
}

func TestReportedNodeOnPremOsImage(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "bare-1"},
		Status: corev1.NodeStatus{
			Capacity:    corev1.ResourceList{},
			Allocatable: corev1.ResourceList{},
			NodeInfo: corev1.NodeSystemInfo{
				OperatingSystem: "linux",
				OSImage:         "Ubuntu 22.04.3 LTS",
				Architecture:    "amd64",
			},
		},
	}
	got := reportedNodeFromK8sNode(node, nodeMetricsSnapshot{})
	if got.CloudProvider != "" {
		t.Fatalf("expected empty cloud for on-prem, got %q", got.CloudProvider)
	}
	if got.OsImage != "Ubuntu 22.04.3 LTS" {
		t.Fatalf("osImage = %q", got.OsImage)
	}
}

func TestNormalizeOsImage(t *testing.T) {
	if got := normalizeOsImage("  Ubuntu 22.04.3 LTS  "); got != "Ubuntu 22.04.3 LTS" {
		t.Fatalf("got %q", got)
	}
}
