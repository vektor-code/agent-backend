package controller

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

func detectLanguageFromPodSpec(pod *corev1.Pod, isFrontend bool) string {
	if isFrontend {
		return ""
	}
	if lang := languageFromAnnotations(pod.Annotations); lang != "" {
		return lang
	}
	if lang := languageFromLabels(pod.Labels); lang != "" {
		return lang
	}
	if lang := languageFromContainers(pod); lang != "" {
		return lang
	}
	return languageFromPodName(pod.Name)
}

func languageFromAnnotations(annotations map[string]string) string {
	for key, value := range annotations {
		if strings.Contains(key, "inject-java") && value != "" {
			return "java"
		}
		if strings.Contains(key, "inject-nodejs") && value != "" {
			return "nodejs"
		}
		if strings.Contains(key, "inject-python") && value != "" {
			return "python"
		}
		if strings.Contains(key, "inject-go") && value != "" {
			return "go"
		}
		if strings.Contains(key, "inject-sdk") {
			return value
		}
	}
	return ""
}

func languageFromLabels(labels map[string]string) string {
	if val, ok := labels["language"]; ok {
		return val
	}
	if val, ok := labels["tech-stack"]; ok {
		return val
	}
	if val, ok := labels["app"]; ok {
		lowerVal := strings.ToLower(val)
		if strings.Contains(lowerVal, "java") {
			return "java"
		}
		if strings.Contains(lowerVal, "node") {
			return "nodejs"
		}
	}
	return ""
}

func languageFromContainers(pod *corev1.Pod) string {
	for _, c := range pod.Spec.Containers {
		img := strings.ToLower(c.Image)
		if strings.Contains(img, "java") || strings.Contains(img, "openjdk") || strings.Contains(img, "tomcat") || strings.Contains(img, "wildfly") || strings.Contains(img, "maven") {
			return "java"
		}
		if strings.Contains(img, "node") || strings.Contains(img, "npm") || strings.Contains(img, "pm2") {
			return "nodejs"
		}
		if strings.Contains(img, "python") || strings.Contains(img, "gunicorn") || strings.Contains(img, "flask") || strings.Contains(img, "django") {
			return "python"
		}
		if strings.Contains(img, "php") || strings.Contains(img, "wordpress") || strings.Contains(img, "apache") || strings.Contains(img, "fpm") {
			return "php"
		}
		if strings.Contains(img, "go") || strings.Contains(img, "golang") {
			return "go"
		}
		if strings.Contains(img, "dotnet") || strings.Contains(img, "aspnet") || strings.Contains(img, "microsoft-dotnet") {
			return "dotnet"
		}

		for _, env := range c.Env {
			name := strings.ToUpper(env.Name)
			if strings.Contains(name, "JAVA_") || strings.Contains(name, "JDK_") {
				return "java"
			}
			if name == "NODE_ENV" || strings.Contains(name, "NODE_") {
				return "nodejs"
			}
			if name == "PYTHONPATH" || strings.Contains(name, "PYTHON_") {
				return "python"
			}
			if strings.Contains(name, "PHP_") {
				return "php"
			}
			if strings.Contains(name, "DOTNET_") || strings.Contains(name, "ASPNETCORE_") {
				return "dotnet"
			}
		}
	}
	return ""
}

func languageFromPodName(name string) string {
	podName := strings.ToLower(name)
	if strings.Contains(podName, "java") {
		return "java"
	}
	if strings.Contains(podName, "node") {
		return "nodejs"
	}
	if strings.Contains(podName, "python") {
		return "python"
	}
	if strings.Contains(podName, "php") {
		return "php"
	}
	if strings.Contains(podName, "go") {
		return "go"
	}
	return ""
}
