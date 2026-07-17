package controller

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

func detectDatabaseInfo(pod *corev1.Pod, configMaps map[string]map[string]string) (dbName, dbHost, dbPort string) {
	envs := podEnvMap(pod, configMaps)
	dbHost = firstEnvValue(envs, "DB_HOST", "POSTGRES_HOST", "MYSQL_HOST", "DATABASE_HOST", "DB_HOST_DEV", "DB_HOST_UAT")
	dbPort = firstEnvValue(envs, "DB_PORT", "POSTGRES_PORT", "MYSQL_PORT", "DATABASE_PORT", "DB_PORT_DEV", "DB_PORT_UAT")
	dbName = firstEnvValue(envs, "DB_NAME", "POSTGRES_DB", "MYSQL_DATABASE", "DB_DATABASE", "DATABASE_NAME", "DB_NAME_DEV", "DB_NAME_UAT")

	for _, key := range []string{"DATABASE_URL", "SPRING_DATASOURCE_URL", "DB_CONNECTION_STRING", "DB_URL", "DB_DSN", "JDBC_DATABASE_URL"} {
		value := envs[key]
		if value == "" {
			continue
		}
		if dbName == "" {
			dbName = extractDbNameFromConnStr(value)
		}
		if dbHost == "" || dbPort == "" {
			host, port := parseHostPortFromConnStr(value)
			if dbHost == "" && host != "" {
				dbHost = host
			}
			if dbPort == "" && port != "" {
				dbPort = port
			}
		}
	}
	return dbName, dbHost, dbPort
}

func podEnvMap(pod *corev1.Pod, configMaps map[string]map[string]string) map[string]string {
	envs := make(map[string]string)
	for _, c := range pod.Spec.Containers {
		for _, env := range c.Env {
			envs[strings.ToUpper(env.Name)] = env.Value
		}
		for _, envFrom := range c.EnvFrom {
			if envFrom.ConfigMapRef == nil {
				continue
			}
			key := pod.Namespace + "/" + envFrom.ConfigMapRef.Name
			data, ok := configMaps[key]
			if !ok {
				continue
			}
			for k, v := range data {
				envs[strings.ToUpper(k)] = v
			}
		}
	}
	return envs
}

func firstEnvValue(envs map[string]string, keys ...string) string {
	for _, key := range keys {
		if val := envs[key]; val != "" {
			return val
		}
	}
	return ""
}

func parseHostPortFromConnStr(connStr string) (string, string) {
	connStr = strings.TrimSpace(connStr)
	if connStr == "" {
		return "", ""
	}
	if strings.Contains(connStr, "://") {
		parts := strings.SplitN(connStr, "://", 2)
		if len(parts) == 2 {
			rem := parts[1]
			atIdx := strings.LastIndex(rem, "@")
			if atIdx != -1 {
				rem = rem[atIdx+1:]
			}
			slashIdx := strings.Index(rem, "/")
			if slashIdx != -1 {
				rem = rem[:slashIdx]
			}
			if strings.Contains(rem, ":") {
				hParts := strings.SplitN(rem, ":", 2)
				return hParts[0], hParts[1]
			}
			return rem, ""
		}
	}
	if strings.HasPrefix(connStr, "jdbc:") {
		rem := strings.TrimPrefix(connStr, "jdbc:")
		if strings.Contains(rem, "://") {
			return parseHostPortFromConnStr(rem)
		}
		parts := strings.Split(rem, "/")
		if len(parts) >= 3 {
			hostPort := parts[2]
			if strings.Contains(hostPort, ":") {
				hParts := strings.SplitN(hostPort, ":", 2)
				return hParts[0], hParts[1]
			}
			return hostPort, ""
		}
	}
	return "", ""
}

func extractDbNameFromConnStr(connStr string) string {
	connStr = strings.TrimSpace(connStr)
	if connStr == "" {
		return ""
	}
	if strings.Contains(connStr, "://") {
		parts := strings.SplitN(connStr, "://", 2)
		if len(parts) == 2 {
			rem := parts[1]
			atIdx := strings.LastIndex(rem, "@")
			if atIdx != -1 {
				rem = rem[atIdx+1:]
			}
			slashIdx := strings.Index(rem, "/")
			if slashIdx != -1 {
				dbPart := rem[slashIdx+1:]
				return cleanDbNamePart(dbPart)
			}
		}
	}
	if strings.HasPrefix(connStr, "jdbc:") {
		slashIdx := strings.LastIndex(connStr, "/")
		if slashIdx != -1 {
			return cleanDbNamePart(connStr[slashIdx+1:])
		}
	}
	return ""
}

func cleanDbNamePart(dbPart string) string {
	if qIdx := strings.Index(dbPart, "?"); qIdx != -1 {
		dbPart = dbPart[:qIdx]
	}
	if semicolonIdx := strings.Index(dbPart, ";"); semicolonIdx != -1 {
		dbPart = dbPart[:semicolonIdx]
	}
	return dbPart
}
