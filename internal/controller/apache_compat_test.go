package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestParseApacheVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"httpd:2.4", "2.4"},
		{"docker.io/library/httpd:2.4.57", "2.4"},
		{"Server version: Apache/2.4.57 (Unix)", "2.4"},
		{"registry.example/app:dev", ""},
	}
	for _, tc := range tests {
		if got := ParseApacheVersion(tc.in); got != tc.want {
			t.Fatalf("ParseApacheVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestApacheInjectSupported(t *testing.T) {
	if !ApacheInjectSupported("2.4") || !ApacheInjectSupported("2.2") {
		t.Fatal("expected supported apache versions")
	}
	if ApacheInjectSupported("2.0") || ApacheInjectSupported("") {
		t.Fatal("expected unsupported apache versions to fail")
	}
}

func TestApacheInjectStatusAlpineBlocked(t *testing.T) {
	containers := []corev1.Container{{
		Image: "httpd:2.4-alpine",
	}}
	_, compatible, reason := ApacheInjectStatus(containers, "")
	if compatible || !strings.Contains(reason, "Alpine/musl") {
		t.Fatalf("alpine httpd should block: compatible=%v reason=%q", compatible, reason)
	}
}

func TestModulesForApacheAgentImageCrnet(t *testing.T) {
	img := "registry.example/instrumentation-nginx:crnet-1.1.0"
	modules := ModulesForApacheAgentImage(img)
	if len(modules) != 2 || modules[0] != "2.4" {
		t.Fatalf("modules = %v", modules)
	}
}
