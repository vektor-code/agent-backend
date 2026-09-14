package controller

import (
	"strings"
	"testing"
)

func TestBuildInstrumentationObjectAddsPythonEnvOverrides(t *testing.T) {
	inst := buildInstrumentationObject("troni-dev", "crnet-apm")
	spec := inst["spec"].(map[string]interface{})
	python := spec["python"].(map[string]interface{})
	env := python["env"].([]interface{})

	if got := envValue(env, "OTEL_EXPORTER_OTLP_ENDPOINT"); got != "http://agent-backend.crnet-apm.svc.cluster.local:4318" {
		t.Fatalf("python OTEL_EXPORTER_OTLP_ENDPOINT = %q, want HTTP endpoint", got)
	}
	if got := envValue(env, "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"); got != "http/protobuf" {
		t.Fatalf("python OTEL_EXPORTER_OTLP_TRACES_PROTOCOL = %q, want http/protobuf", got)
	}
	if got := envValue(env, "OTEL_METRICS_EXPORTER"); got != "none" {
		t.Fatalf("python OTEL_METRICS_EXPORTER = %q, want none", got)
	}
}

func TestBuildInstrumentationObjectUsesFastExportAndHTTPForDotnet(t *testing.T) {
	inst := buildInstrumentationObject("troni-dev", "crnet-apm")
	spec := inst["spec"].(map[string]interface{})
	env := spec["env"].([]interface{})
	if got := envValue(env, "OTEL_BSP_SCHEDULE_DELAY"); got != "500" {
		t.Fatalf("OTEL_BSP_SCHEDULE_DELAY = %q, want 500", got)
	}
	dotnet := spec["dotnet"].(map[string]interface{})
	dotnetEnv := dotnet["env"].([]interface{})
	if got := envValue(dotnetEnv, "OTEL_EXPORTER_OTLP_PROTOCOL"); got != "http/protobuf" {
		t.Fatalf("dotnet protocol = %q, want http/protobuf", got)
	}
	goSpec := spec["go"].(map[string]interface{})
	sec := goSpec["securityContext"].(map[string]interface{})
	if sec["privileged"] != true {
		t.Fatalf("go sidecar should be privileged for eBPF")
	}
	if sec["runAsUser"] != int64(0) {
		t.Fatalf("go sidecar runAsUser = %#v, want 0", sec["runAsUser"])
	}
	goEnv := goSpec["env"].([]interface{})
	if got := envValue(goEnv, "OTEL_EXPORTER_OTLP_PROTOCOL"); got != "http/protobuf" {
		t.Fatalf("go protocol = %q, want http/protobuf (operator/Go auto-instr default)", got)
	}
	if got := envValue(goEnv, "OTEL_GO_AUTO_GLOBAL"); got != "true" {
		t.Fatalf("OTEL_GO_AUTO_GLOBAL = %q, want true", got)
	}
	if got := envValue(goEnv, "OTEL_EXPORTER_OTLP_ENDPOINT"); got != "http://agent-backend.crnet-apm.svc.cluster.local:4318" {
		t.Fatalf("go endpoint = %q, want HTTP :4318", got)
	}
	compat := buildCompatibleInstrumentationObject("troni-dev", "crnet-apm")["spec"].(map[string]interface{})
	compatGo := compat["go"].(map[string]interface{})
	if _, ok := compatGo["securityContext"]; ok {
		t.Fatal("compatible go spec must omit securityContext for older CRDs")
	}
	if _, ok := spec["apacheHttpd"]; !ok {
		t.Fatal("expected apacheHttpd spec like nginx (OTel operator)")
	}
	nginx, ok := spec["nginx"].(map[string]interface{})
	if !ok {
		t.Fatal("expected nginx spec")
	}
	if nginx["image"] == nil || nginx["image"] == "" {
		t.Fatal("expected nginx image pin")
	}
}

func TestBuildInstrumentationObjectCapturesHTTPHeaders(t *testing.T) {
	inst := buildInstrumentationObject("troni-dev", "crnet-apm")
	spec := inst["spec"].(map[string]interface{})
	env := spec["env"].([]interface{})
	if got := envValue(env, "CRNET_HTTP_CAPTURE"); got != "true" {
		t.Fatalf("CRNET_HTTP_CAPTURE = %q", got)
	}
	if got := envValue(env, "OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SERVER_REQUEST"); got == "" {
		t.Fatal("expected HTTP header capture allowlist")
	}
	if strings.Contains(envValue(env, "OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SERVER_REQUEST"), "authorization") {
		t.Fatal("must not capture Authorization")
	}
}

func TestCrnetLanguageAgentsReplaceOfficialInjectors(t *testing.T) {
	t.Setenv("CRNET_AGENT_JAVA_IMAGE", "registry.example/instrumentation-java:dev")
	t.Setenv("CRNET_AGENT_PYTHON_IMAGE", "registry.example/instrumentation-python:dev")
	t.Setenv("CRNET_AGENT_NODEJS_IMAGE", "registry.example/instrumentation-nodejs:dev")
	inst := buildInstrumentationObject("troni-dev", "crnet-apm")
	spec := inst["spec"].(map[string]interface{})
	if spec["java"].(map[string]interface{})["image"] != "registry.example/instrumentation-java:dev" {
		t.Fatalf("java injector = %#v", spec["java"])
	}
	if spec["python"].(map[string]interface{})["image"] != "registry.example/instrumentation-python:dev" {
		t.Fatalf("python injector = %#v", spec["python"])
	}
	if spec["nodejs"].(map[string]interface{})["image"] != "registry.example/instrumentation-nodejs:dev" {
		t.Fatalf("nodejs injector = %#v", spec["nodejs"])
	}
	if _, ok := spec["java"].(map[string]interface{})["extensions"]; ok {
		t.Fatal("java extensions are not used; the CRNET javaagent is the injector image")
	}
}

func TestOfficialInjectorsWhenCrnetAgentsUnset(t *testing.T) {
	inst := buildInstrumentationObject("troni-dev", "crnet-apm")
	java := inst["spec"].(map[string]interface{})["java"].(map[string]interface{})["image"].(string)
	if !strings.Contains(java, "autoinstrumentation-java") {
		t.Fatalf("expected stock java injector fallback, got %q", java)
	}
}

func TestBuildCompatibleInstrumentationObjectOmitsOptionalFields(t *testing.T) {
	spec := buildCompatibleInstrumentationObject("troni-dev", "crnet-apm")["spec"].(map[string]interface{})
	if _, ok := spec["apacheHttpd"]; ok {
		t.Fatal("compatible spec should omit apacheHttpd")
	}
	if _, ok := spec["nginx"]; ok {
		t.Fatal("compatible spec should omit nginx")
	}
	if _, ok := spec["resource"]; ok {
		t.Fatal("compatible spec should omit resource")
	}
	if _, ok := spec["java"]; !ok {
		t.Fatal("compatible spec should still include java")
	}
	props := spec["propagators"].([]interface{})
	for _, p := range props {
		if p == "jaeger" {
			t.Fatal("compatible spec should omit jaeger propagator")
		}
	}
}

func envValue(env []interface{}, name string) string {
	for _, item := range env {
		kv, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if kv["name"] == name {
			value, _ := kv["value"].(string)
			return value
		}
	}
	return ""
}
