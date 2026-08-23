package controller

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

func detectLanguageFromPodSpec(pod *corev1.Pod) string {
	images, commands := imagesAndCommands(pod.Spec.Containers)
	if lang := languageFromAnnotations(pod.Annotations); lang != "" {
		return resolveInject(lang, images, commands)
	}
	if lang := languageFromLabels(pod.Labels); lang != "" {
		return lang
	}
	return resolveInject("", images, commands)
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
		if strings.Contains(key, "inject-dotnet") && value != "" {
			return "dotnet"
		}
		if strings.Contains(key, "inject-php") && value != "" {
			return "php"
		}
		if strings.Contains(key, "inject-go") && value != "" {
			return "go"
		}
		if strings.Contains(key, "inject-nginx") && value != "" {
			return "nginx"
		}
		if strings.Contains(key, "inject-apache-httpd") && value != "" {
			return "apache-httpd"
		}
		if strings.Contains(key, "inject-ruby") && value != "" {
			return "ruby"
		}
		if strings.Contains(key, "inject-sdk") && value != "" && value != "false" {
			return "sdk"
		}
	}
	return ""
}

func languageFromLabels(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	for _, key := range []string{
		"language",
		"tech-stack",
		"app.kubernetes.io/language",
		"tags.datadoghq.com/language",
		"instrumentation.opentelemetry.io/container-language",
	} {
		if val := strings.TrimSpace(labels[key]); val != "" {
			return val
		}
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
		if lang := languageFromImage(c.Image); lang != "" {
			return lang
		}
		if lang := languageFromCommand(c); lang != "" {
			return lang
		}
		for _, env := range c.Env {
			name := strings.ToUpper(env.Name)
			if strings.Contains(name, "JAVA_") || strings.Contains(name, "JDK_") || name == "JAVA_TOOL_OPTIONS" {
				return "java"
			}
			if name == "NODE_ENV" || strings.HasPrefix(name, "NODE_") {
				return "nodejs"
			}
			if name == "PYTHONPATH" || strings.HasPrefix(name, "PYTHON_") || name == "UVICORN_HOST" {
				return "python"
			}
			if strings.Contains(name, "PHP_") || name == "PHP_INI_SCAN_DIR" {
				return "php"
			}
			if strings.Contains(name, "RUBY") || name == "BUNDLE_PATH" || name == "RAILS_ENV" {
				return "ruby"
			}
			if strings.Contains(name, "DOTNET_") || strings.Contains(name, "ASPNETCORE_") {
				return "dotnet"
			}
			if strings.Contains(name, "GOPATH") || strings.Contains(name, "GOROOT") {
				return "go"
			}
		}
	}
	return ""
}

func languageFromImage(image string) string {
	img := strings.ToLower(image)
	switch {
	case containsAny(img, "openjdk", "eclipse-temurin", "temurin", "amazoncorretto", "corretto", "microsoft-openjdk", "ibm-semeru", "liberica", "sapmachine", "graalvm", "distroless/java", "tomcat", "wildfly", "jre", "jdk", "maven", "spring-boot"):
		return "java"
	case containsAny(img, "node:", "node@", "/node", "nodejs", "npm", "pm2", "distroless/nodejs"):
		return "nodejs"
	case containsAny(img, "python", "gunicorn", "uvicorn", "flask", "django", "distroless/python"):
		return "python"
	case containsAny(img, "php-fpm", "php:", "/php", "wordpress", "laravel"):
		return "php"
	case containsAny(img, "ruby", "rails", "puma"):
		return "ruby"
	case containsAny(img, "dotnet", "aspnet", "microsoft-dotnet"):
		return "dotnet"
	case containsAny(img, "nginx", "openresty"):
		return "nginx"
	case strings.Contains(img, "httpd") || strings.Contains(img, "apache2"):
		return "apache-httpd"
	case containsAny(img, "golang", "/go:", "/go@", "distroless/static", "distroless/base"):
		// distroless/static|base is commonly Go scratch; still a heuristic.
		if strings.Contains(img, "golang") || strings.Contains(img, "/go:") || strings.Contains(img, "/go@") || strings.HasPrefix(img, "go:") {
			return "go"
		}
	}
	return ""
}

func languageFromCommand(c corev1.Container) string {
	parts := append(append([]string{}, c.Command...), c.Args...)
	for _, part := range parts {
		base := strings.ToLower(filepathBase(part))
		switch {
		case base == "java" || strings.HasSuffix(base, ".jar"):
			return "java"
		case base == "node" || strings.HasSuffix(base, ".js"):
			return "nodejs"
		case base == "python" || base == "python3" || base == "gunicorn" || base == "uvicorn":
			return "python"
		case base == "php" || base == "php-fpm":
			return "php"
		case base == "ruby" || base == "bundle" || base == "puma" || base == "rails":
			return "ruby"
		case base == "dotnet":
			return "dotnet"
		case base == "nginx":
			return "nginx"
		case base == "httpd" || base == "apache2":
			return "apache-httpd"
		}
	}
	return ""
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func filepathBase(value string) string {
	value = strings.TrimSpace(value)
	if i := strings.LastIndexAny(value, "/\\"); i >= 0 {
		return value[i+1:]
	}
	return value
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
	if strings.Contains(podName, "ruby") || strings.Contains(podName, "rails") {
		return "ruby"
	}
	if strings.Contains(podName, "golang") || strings.HasPrefix(podName, "go-") || strings.Contains(podName, "-go-") {
		return "go"
	}
	return ""
}
