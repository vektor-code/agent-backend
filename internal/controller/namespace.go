package controller

import "strings"

func isAppNamespace(ns string) bool {
	lower := strings.ToLower(ns)
	if strings.HasPrefix(lower, "kube-") ||
		strings.HasPrefix(lower, "istio-") ||
		strings.HasPrefix(lower, "ingress-") ||
		strings.HasPrefix(lower, "prometheus-") ||
		strings.HasPrefix(lower, "argocd-") ||
		strings.HasPrefix(lower, "cert-") ||
		strings.HasPrefix(lower, "devops-") ||
		strings.HasPrefix(lower, "devopstools-") ||
		lower == "argocd" ||
		lower == "prometheus" ||
		lower == "grafana" ||
		lower == "fluentbit" ||
		lower == "metallb-system" ||
		lower == "backstage" ||
		lower == "permission-manager" ||
		lower == "apm-observability" ||
		lower == "lens-shells" ||
		lower == "lens-with-go" ||
		lower == "nfs-provisioner" ||
		lower == "kong" {
		return false
	}
	return true
}
