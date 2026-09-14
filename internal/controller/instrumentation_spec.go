package controller

import (
	"fmt"
	"os"
)

// instrumentationImages pins the OpenTelemetry auto-instrumentation images used
// for injection. They default to current, fast, stable releases and can each be
// overridden by an env var without rebuilding — e.g. OTEL_PYTHON_IMAGE.
//
// These must be recent: older Python images bundle a typing_extensions that
// lacks Sentinel, which shadows the app's copy and breaks pydantic/FastAPI apps.
var instrumentationImages = struct {
	java, nodejs, python, dotnet, golang, nginx, apache string
}{
	java:   envOr("OTEL_JAVA_IMAGE", "ghcr.io/open-telemetry/opentelemetry-operator/autoinstrumentation-java:2.31.0"),
	nodejs: envOr("OTEL_NODEJS_IMAGE", "ghcr.io/open-telemetry/opentelemetry-operator/autoinstrumentation-nodejs:0.78.0"),
	python: envOr("OTEL_PYTHON_IMAGE", "ghcr.io/open-telemetry/opentelemetry-operator/autoinstrumentation-python:0.64b0"),
	dotnet: envOr("OTEL_DOTNET_IMAGE", "ghcr.io/open-telemetry/opentelemetry-operator/autoinstrumentation-dotnet:1.16.0"),
	golang: envOr("OTEL_GO_IMAGE", "ghcr.io/open-telemetry/opentelemetry-go-instrumentation/autoinstrumentation-go:v0.24.0"),
	nginx:  envOr("OTEL_NGINX_IMAGE", "ghcr.io/open-telemetry/opentelemetry-operator/autoinstrumentation-apache-httpd:1.0.4"),
	apache: envOr("OTEL_APACHE_IMAGE", "ghcr.io/open-telemetry/opentelemetry-operator/autoinstrumentation-apache-httpd:1.0.4"),
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func kv(name, value string) map[string]interface{} {
	return map[string]interface{}{"name": name, "value": value}
}

// otelSampler defaults to sampling every trace (parent-respecting) so nothing is
// missed — full detection. Override with OTEL_SAMPLER_TYPE (e.g.
// parentbased_traceidratio) + OTEL_SAMPLER_ARG (e.g. 0.1) to subsample high-traffic
// services without a rebuild.
func otelSampler() map[string]interface{} {
	s := map[string]interface{}{"type": envOr("OTEL_SAMPLER_TYPE", "parentbased_always_on")}
	if arg := os.Getenv("OTEL_SAMPLER_ARG"); arg != "" {
		s["argument"] = arg
	}
	return s
}

// optimizedInstrumentationEnv is the low-overhead SDK profile injected into every
// instrumented workload: cheap gRPC transport, metrics/logs off, background
// batched export (so instrumentation adds no request-path latency and low
// CPU/network), and bounded per-span memory. Every knob is env-overridable.
func optimizedInstrumentationEnv() []interface{} {
	env := []interface{}{
		kv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc"),
		kv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "grpc"),
		kv("OTEL_METRICS_EXPORTER", "none"),
		kv("OTEL_LOGS_EXPORTER", "none"),
	}
	return append(env, batchAndLimitEnv()...)
}

func batchAndLimitEnv() []interface{} {
	return []interface{}{
		// Async batched export — keeps span export off the request path.
		kv("OTEL_BSP_SCHEDULE_DELAY", envOr("OTEL_BSP_SCHEDULE_DELAY", "500")),
		kv("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", envOr("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", "512")),
		kv("OTEL_BSP_MAX_QUEUE_SIZE", envOr("OTEL_BSP_MAX_QUEUE_SIZE", "2048")),
		kv("OTEL_BSP_EXPORT_TIMEOUT", envOr("OTEL_BSP_EXPORT_TIMEOUT", "30000")),
		// Bound per-span memory so one chatty attribute cannot bloat the pod.
		kv("OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT", envOr("OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT", "192")),
		kv("OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT", envOr("OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT", "4096")),
	}
}

func languageInstrumentationSpec(image string) map[string]interface{} {
	return map[string]interface{}{
		"image": image,
		"env":   optimizedInstrumentationEnv(),
	}
}

func pythonInstrumentationEnv(endpoint string) []interface{} {
	env := []interface{}{
		kv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint),
		kv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf"),
		kv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf"),
		kv("OTEL_METRICS_EXPORTER", "none"),
		kv("OTEL_LOGS_EXPORTER", "none"),
	}
	return append(env, batchAndLimitEnv()...)
}

func pythonInstrumentationSpec(image, endpoint string) map[string]interface{} {
	return map[string]interface{}{
		"image": image,
		"env":   pythonInstrumentationEnv(endpoint),
	}
}

// dotnetInstrumentationSpec uses the .NET auto-instrumentation image with the
// HTTP OTLP exporter profile (same transport as Python). It must not reuse the
// Python inject image — only the shared HTTP exporter env.
func dotnetInstrumentationSpec(image, endpoint string) map[string]interface{} {
	return map[string]interface{}{
		"image": image,
		"env":   pythonInstrumentationEnv(endpoint),
	}
}

func goInstrumentationEnv(endpoint string) []interface{} {
	env := []interface{}{
		kv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint),
		kv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf"),
		kv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf"),
		kv("OTEL_METRICS_EXPORTER", "none"),
		kv("OTEL_LOGS_EXPORTER", "none"),
		// Also capture spans created via the Go OTel global API (common in libraries).
		kv("OTEL_GO_AUTO_GLOBAL", envOr("OTEL_GO_AUTO_GLOBAL", "true")),
	}
	return append(env, batchAndLimitEnv()...)
}

func goInstrumentationSpec(image, httpEndpoint string, compatible bool) map[string]interface{} {
	spec := map[string]interface{}{
		"image": image,
		"env":   goInstrumentationEnv(httpEndpoint),
		"resourceRequirements": map[string]interface{}{
			"limits": map[string]interface{}{
				"cpu":    envOr("OTEL_GO_CPU_LIMIT", "750m"),
				"memory": envOr("OTEL_GO_MEMORY_LIMIT", "512Mi"),
			},
			"requests": map[string]interface{}{
				"cpu":    envOr("OTEL_GO_CPU_REQUEST", "100m"),
				"memory": envOr("OTEL_GO_MEMORY_REQUEST", "128Mi"),
			},
		},
	}
	// Older Instrumentation CRDs (pre securityContext on spec.go) warn/drop the
	// field. Operator still injects privileged+runAsUser:0 defaults when unset.
	if !compatible {
		spec["securityContext"] = map[string]interface{}{
			"privileged":               true,
			"runAsUser":                int64(0),
			"allowPrivilegeEscalation": true,
			"capabilities": map[string]interface{}{
				"add": []interface{}{"SYS_PTRACE", "SYS_ADMIN"},
			},
		}
	}
	return spec
}

func instrumentationName(namespace string) string {
	return namespace + "-instrumentation"
}

func buildInstrumentationObject(namespace, agentNamespace string) map[string]interface{} {
	return buildInstrumentationObjectOpts(namespace, agentNamespace, false)
}

// buildCompatibleInstrumentationObject omits fields some operator 0.58
// installs reject (apacheHttpd/nginx/resource/jaeger) so CR create still succeeds.
func buildCompatibleInstrumentationObject(namespace, agentNamespace string) map[string]interface{} {
	return buildInstrumentationObjectOpts(namespace, agentNamespace, true)
}

func buildInstrumentationObjectOpts(namespace, agentNamespace string, compatible bool) map[string]interface{} {
	if agentNamespace == "" {
		agentNamespace = currentAgentNamespace()
	}
	name := instrumentationName(namespace)
	grpcEndpoint := fmt.Sprintf("http://agent-backend.%s.svc.cluster.local:4317", agentNamespace)
	httpEndpoint := fmt.Sprintf("http://agent-backend.%s.svc.cluster.local:4318", agentNamespace)

	propagators := []interface{}{"tracecontext", "baggage", "b3"}
	if !compatible {
		propagators = append(propagators, "jaeger")
	}

	spec := map[string]interface{}{
		"exporter": map[string]interface{}{
			"endpoint": grpcEndpoint,
		},
		"propagators": propagators,
		"sampler":     otelSampler(),
		"env":         withHTTPCapture(optimizedInstrumentationEnv()),
		"java":        javaInstrumentationSpec(instrumentationImages.java),
		"nodejs":      nodejsInstrumentationSpec(instrumentationImages.nodejs),
		"python":      pythonCaptureInstrumentationSpec(instrumentationImages.python, httpEndpoint),
		"dotnet":      dotnetInstrumentationSpec(instrumentationImages.dotnet, httpEndpoint),
		"go":          goInstrumentationSpec(instrumentationImages.golang, httpEndpoint, compatible),
	}
	if !compatible {
		spec["resource"] = map[string]interface{}{
			"addK8sUIDAttributes": true,
		}
		spec["nginx"] = map[string]interface{}{
			"image": instrumentationImages.nginx,
			"env":   pythonInstrumentationEnv(httpEndpoint),
		}
		spec["apacheHttpd"] = map[string]interface{}{
			"image": instrumentationImages.apache,
			"env":   pythonInstrumentationEnv(httpEndpoint),
		}
	}

	return map[string]interface{}{
		"apiVersion": "opentelemetry.io/v1alpha1",
		"kind":       "Instrumentation",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": spec,
	}
}
