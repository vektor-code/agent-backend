package controller

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// isFrontendPod is true only when the running process is a static HTTP
// server (nginx/apache). Workload names are ignored — the assigned tech
// stack (and the container image/command) is the source of truth.
func isFrontendPod(pod *corev1.Pod, _ map[string]bool, _ []corev1.Service) bool {
	images, commands := imagesAndCommands(pod.Spec.Containers)
	return isStaticHTTPStack(resolveInject("", images, commands))
}

func isStaticHTTPStack(lang string) bool {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "nginx", "apache-httpd", "apache", "httpd":
		return true
	}
	return false
}

func imagesAndCommands(containers []corev1.Container) (images, commands []string) {
	for _, c := range containers {
		images = append(images, c.Image)
		commands = append(commands, c.Command...)
		commands = append(commands, c.Args...)
	}
	return images, commands
}
