package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestParseNginxVersionFromImageTags(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"nginx:1.31.4", "1.31.4"},
		{"nginx:1.31.4-alpine", "1.31.4"},
		{"nginx:1.25.3", "1.25.3"},
		{"docker.io/bitnami/nginx:1.24.0", "1.24.0"},
		{"registry.example/highping/app-frontend:dev-1286", ""},
		{"nginx:alpine", ""},
	}
	for _, tc := range tests {
		if got := ParseNginxVersion(tc.in); got != tc.want {
			t.Fatalf("ParseNginxVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNginxInjectSupportedDefaultAgent(t *testing.T) {
	if !NginxInjectSupported("1.24.0") || !NginxInjectSupported("1.25.3") {
		t.Fatal("expected default agent nginx versions to pass")
	}
	for _, v := range []string{"", "1.31.4", "1.27.0"} {
		if NginxInjectSupported(v) {
			t.Fatalf("NginxInjectSupported(%q) should be false with default agent", v)
		}
	}
}

func TestModulesForNginxAgentImageCrnet(t *testing.T) {
	img := "git.cloudraft.net:5050/devops/images/crnet-apm/instrumentation-nginx:crnet-1.1.0"
	modules := ModulesForNginxAgentImage(img)
	if len(modules) != len(crnetNginxModuleVersions) {
		t.Fatalf("got %d modules, want %d", len(modules), len(crnetNginxModuleVersions))
	}
	if modules[len(modules)-1] != "1.31.4" {
		t.Fatalf("last module = %q, want 1.31.4", modules[len(modules)-1])
	}
}

func TestNginxInjectSupportedCrnetImage(t *testing.T) {
	t.Setenv("OTEL_NGINX_IMAGE", "registry.example/crnet-apm/instrumentation-nginx:crnet-1.1.0")
	if !NginxInjectSupported("1.31.4") {
		t.Fatal("expected 1.31.4 supported when CRNET fat tag crnet-1.1.0 is configured")
	}
	t.Setenv("OTEL_NGINX_IMAGE", "registry.example/crnet-apm/instrumentation-nginx:dev")
	if NginxInjectSupported("1.31.4") {
		t.Fatal("smoke :dev tag must not claim 1.31.4 without crnet- tag or OTEL_NGINX_SUPPORTED_MODULES")
	}
}

func TestNginxInjectBlockedReason(t *testing.T) {
	if !strings.Contains(NginxInjectBlockedReason(""), "unknown") {
		t.Fatal("expected unknown-version message")
	}
	if !strings.Contains(NginxInjectBlockedReason("1.31.4"), "Supported nginx modules") {
		t.Fatal("expected supported modules in blocked reason")
	}
}

func TestNginxInjectStatusAlpineBlocked(t *testing.T) {
	containers := []corev1.Container{{
		Image: "nginx:1.25.3-alpine",
	}}
	_, compatible, reason := NginxInjectStatus(containers, "")
	if compatible || !strings.Contains(reason, "Alpine/musl") {
		t.Fatalf("alpine should block inject: compatible=%v reason=%q", compatible, reason)
	}
}

func TestNginxInjectStatusFromEnv(t *testing.T) {
	containers := []corev1.Container{{
		Image: "registry.example/app-frontend:dev",
		Env:   []corev1.EnvVar{{Name: "NGINX_VERSION", Value: "1.25.3"}},
	}}
	version, compatible, reason := NginxInjectStatus(containers, "")
	if version != "1.25.3" || !compatible || reason != "" {
		t.Fatalf("got version=%q compatible=%v reason=%q", version, compatible, reason)
	}
}
