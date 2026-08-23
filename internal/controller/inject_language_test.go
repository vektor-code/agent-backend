package controller

import "testing"

func TestSPAImageKeepsAssignedNginx(t *testing.T) {
	if got := resolveInject("nginx", []string{"registry/cv-frontend:dev"}, nil); got != "nginx" {
		t.Fatalf("got %s", got)
	}
}

func TestPackageJSONNodeOnNginxProcess(t *testing.T) {
	got := resolveInject("nodejs", []string{"nginx:1.27-alpine"}, []string{"nginx", "-g", "daemon off;"})
	if got != "nginx" {
		t.Fatalf("got %s, want nginx", got)
	}
}

func TestJavaBackendUnchanged(t *testing.T) {
	got := resolveInject("java", []string{"eclipse-temurin:21-jre"}, nil)
	if got != "java" {
		t.Fatalf("got %s", got)
	}
}

func TestRealNodeSSRKeepsNode(t *testing.T) {
	got := resolveInject("nodejs", []string{"node:20-alpine"}, []string{"node", "server.js"})
	if got != "nodejs" {
		t.Fatalf("got %s", got)
	}
}
