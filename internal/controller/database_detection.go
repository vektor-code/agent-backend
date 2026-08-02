package controller

import (
	"os"
	"strings"
	"sync"

	"github.com/kubetrace/shared/connstr"
	corev1 "k8s.io/api/core/v1"
)

// Environment variables that override which pod env keys are inspected, as
// comma-separated lists. Sites with their own naming conventions configure them
// here instead of the names being compiled in.
const (
	DBHostKeysEnv = "KUBETRACE_DB_HOST_ENV_KEYS"
	DBPortKeysEnv = "KUBETRACE_DB_PORT_ENV_KEYS"
	DBNameKeysEnv = "KUBETRACE_DB_NAME_ENV_KEYS"
	DBURLKeysEnv  = "KUBETRACE_DB_URL_ENV_KEYS"
)

var defaultDBEnvKeys = struct {
	host, port, name, url []string
}{
	host: []string{"DB_HOST", "POSTGRES_HOST", "MYSQL_HOST", "DATABASE_HOST", "PGHOST", "MSSQL_HOST", "ORACLE_HOST"},
	port: []string{"DB_PORT", "POSTGRES_PORT", "MYSQL_PORT", "DATABASE_PORT", "PGPORT", "MSSQL_PORT", "ORACLE_PORT"},
	name: []string{"DB_NAME", "POSTGRES_DB", "MYSQL_DATABASE", "DB_DATABASE", "DATABASE_NAME", "PGDATABASE"},
	url: []string{
		"DATABASE_URL", "SPRING_DATASOURCE_URL", "DB_CONNECTION_STRING", "DB_URL", "DB_DSN",
		"JDBC_DATABASE_URL", "POSTGRES_URL", "MYSQL_URL", "MONGODB_URI", "REDIS_URL",
	},
}

var (
	dbEnvKeysOnce sync.Once
	dbHostKeys    []string
	dbPortKeys    []string
	dbNameKeys    []string
	dbURLKeys     []string
)

func loadDBEnvKeys() {
	dbEnvKeysOnce.Do(func() {
		dbHostKeys = envKeyList(DBHostKeysEnv, defaultDBEnvKeys.host)
		dbPortKeys = envKeyList(DBPortKeysEnv, defaultDBEnvKeys.port)
		dbNameKeys = envKeyList(DBNameKeysEnv, defaultDBEnvKeys.name)
		dbURLKeys = envKeyList(DBURLKeysEnv, defaultDBEnvKeys.url)
	})
}

// envKeyList reads a comma-separated override, falling back to the defaults.
func envKeyList(envVar string, defaults []string) []string {
	raw := strings.TrimSpace(os.Getenv(envVar))
	if raw == "" {
		return defaults
	}
	var out []string
	for _, key := range strings.Split(raw, ",") {
		if key = strings.ToUpper(strings.TrimSpace(key)); key != "" {
			out = append(out, key)
		}
	}
	if len(out) == 0 {
		return defaults
	}
	return out
}

// detectDatabaseInfo reads a pod's environment for the database it talks to.
// Discrete host/port/name variables win over a connection string, since they
// need no parsing; anything still missing is recovered from the URL forms.
func detectDatabaseInfo(pod *corev1.Pod, configMaps map[string]map[string]string) (dbName, dbHost, dbPort string) {
	loadDBEnvKeys()

	envs := podEnvMap(pod, configMaps)
	dbHost = firstEnvValue(envs, dbHostKeys...)
	dbPort = firstEnvValue(envs, dbPortKeys...)
	dbName = firstEnvValue(envs, dbNameKeys...)

	if dbName != "" && dbHost != "" && dbPort != "" {
		return dbName, dbHost, dbPort
	}

	for _, key := range dbURLKeys {
		value := envs[key]
		if value == "" {
			continue
		}
		info := connstr.Parse(value)
		if dbName == "" {
			dbName = info.Database
		}
		if dbHost == "" {
			dbHost = info.Host
		}
		if dbPort == "" {
			dbPort = info.Port
		}
		if dbName != "" && dbHost != "" && dbPort != "" {
			break
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
