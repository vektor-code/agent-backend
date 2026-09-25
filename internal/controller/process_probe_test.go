package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestProcessLangCacheSetGet(t *testing.T) {
	c := &processLangCache{
		byImageID:  make(map[string]imageCacheEntry),
		byWorkload: make(map[string]string),
	}
	c.store("sha256:abc", []string{"ns/app"}, "nginx: master process", "nginx")
	if cmdline, ok := c.cachedCmdline("sha256:abc"); !ok || cmdline != "nginx: master process" {
		t.Fatalf("cachedCmdline() = %q, %v; want nginx cmdline", cmdline, ok)
	}
	if lang := c.byWorkload["ns/app"]; lang != "nginx" {
		t.Fatalf("workload cache = %q, want nginx", lang)
	}
}

func TestProcessLangCacheBackoff(t *testing.T) {
	c := &processLangCache{
		byImageID:  make(map[string]imageCacheEntry),
		byWorkload: make(map[string]string),
	}
	c.markFailed("sha256:fail")
	if !c.backedOff("sha256:fail") {
		t.Fatal("expected backed off after markFailed")
	}
	c.store("sha256:fail", []string{"ns/svc"}, "java -jar app.jar", "java")
	if c.backedOff("sha256:fail") {
		t.Fatal("successful store should clear backoff")
	}
	c.byImageID["sha256:old"] = imageCacheEntry{failUntil: time.Now().Add(processProbeFailBackoff)}
	if !c.backedOff("sha256:old") {
		t.Fatal("expected backed off for failed image")
	}
}

func TestGetProcessLangByWorkload(t *testing.T) {
	processLangByWorkload.store("sha256:x", []string{"prod/api"}, "uvicorn main:app", "python")
	if got := getProcessLangByWorkload("prod", "api"); got != "python" {
		t.Fatalf("getProcessLangByWorkload() = %q, want python", got)
	}
}

func TestWorkloadKeysIncludeDeploymentOwner(t *testing.T) {
	ctrl := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-frontend-6dd44fbd75-wh64s",
			Namespace: "4sim-website-dev",
			Labels:    map[string]string{"app.kubernetes.io/name": "app-frontend"},
			OwnerReferences: []metav1.OwnerReference{{
				Kind:       "ReplicaSet",
				Name:       "app-frontend-6dd44fbd75",
				Controller: &ctrl,
			}},
		},
	}
	keys := workloadKeysFromPod(pod)
	want := map[string]bool{
		"4sim-website-dev/app-frontend": true,
	}
	for _, k := range keys {
		delete(want, k)
	}
	if len(want) != 0 {
		t.Fatalf("missing keys %v; got %v", want, keys)
	}
	if got := deploymentNameFromReplicaSet("app-frontend-6dd44fbd75"); got != "app-frontend" {
		t.Fatalf("deploymentNameFromReplicaSet() = %q, want app-frontend", got)
	}
}
