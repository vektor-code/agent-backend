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

func currentAgentNamespace() string {
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		return namespace
	}
	return "trace-prod"
}
