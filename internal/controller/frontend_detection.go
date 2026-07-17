package controller

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

func isFrontendPod(pod *corev1.Pod, frontendServices map[string]bool, services []corev1.Service) bool {
	if podHasInstrumentationAnnotation(pod) {
		return false
	}
	if podLooksBackend(pod) {
		return false
	}
	if podLooksStaticFrontend(pod) {
		return true
	}
	return podMatchesFrontendService(pod, frontendServices, services)
}

func podLooksBackend(pod *corev1.Pod) bool {
	for _, c := range pod.Spec.Containers {
		img := strings.ToLower(c.Image)
		if strings.Contains(img, "java") || strings.Contains(img, "openjdk") || strings.Contains(img, "tomcat") ||
			strings.Contains(img, "python") || strings.Contains(img, "django") || strings.Contains(img, "flask") ||
			strings.Contains(img, "php") || strings.Contains(img, "fpm") || strings.Contains(img, "laravel") ||
			strings.Contains(img, "dotnet") || strings.Contains(img, "aspnet") ||
			strings.Contains(img, "golang") || strings.Contains(img, "node:") || strings.Contains(img, "node-") {
			return true
		}
		for _, env := range c.Env {
			envName := strings.ToUpper(env.Name)
			if strings.Contains(envName, "DB_") || strings.Contains(envName, "DATABASE") ||
				strings.Contains(envName, "REDIS") || strings.Contains(envName, "KAFKA") ||
				strings.Contains(envName, "POSTGRES") || strings.Contains(envName, "MONGO") ||
				strings.Contains(envName, "RABBITMQ") || strings.Contains(envName, "SPRING_") {
				return true
			}
		}
	}
	return false
}

func podLooksStaticFrontend(pod *corev1.Pod) bool {
	for _, c := range pod.Spec.Containers {
		img := strings.ToLower(c.Image)
		if strings.Contains(img, "nginx") || strings.Contains(img, "caddy") || strings.Contains(img, "httpd") || strings.Contains(img, "apache") {
			return true
		}
	}
	return false
}

func podMatchesFrontendService(pod *corev1.Pod, frontendServices map[string]bool, services []corev1.Service) bool {
	for _, svc := range services {
		key := pod.Namespace + "/" + svc.Name
		if !frontendServices[key] {
			continue
		}
		if serviceSelectorMatchesPod(svc.Spec.Selector, pod.Labels) {
			return true
		}
	}
	return false
}

func serviceSelectorMatchesPod(selector, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}
