package controller

import (
	"os"
	"strings"
)

func namespaceConfigURL(centralURL string) string {
	configURL := strings.Replace(centralURL, "/v1/traces", "/v1/namespaces/config", 1)
	return strings.Replace(configURL, "/api/traces", "/v1/namespaces/config", 1)
}

func currentClusterName() string {
	if clusterName := os.Getenv("CLUSTER_NAME"); clusterName != "" {
		return clusterName
	}
	return "default"
}

// currentAgentNamespace resolves the namespace the agent is running in, so it
// reports the correct OTLP endpoint to the platform. It never hardcodes a
// namespace: it prefers the downward-API env, then the in-cluster ServiceAccount
// namespace file (always present in a pod), then an explicit override.
func currentAgentNamespace() string {
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		return namespace
	}
	if b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		if ns := strings.TrimSpace(string(b)); ns != "" {
			return ns
		}
	}
	if namespace := os.Getenv("AGENT_NAMESPACE"); namespace != "" {
		return namespace
	}
	return "default"
}
