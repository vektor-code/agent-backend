package agent

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const (
	defaultForwarderWorkers = 8
	defaultHTTPMaxBodyMB    = 20
)

type Config struct {
	GRPCAddr         string
	HTTPAddr         string
	CentralURL       string
	BufferDir        string
	QueueLimit       int
	DiskMaxFiles     int64
	SkipTLSVerify    bool
	ForwarderWorkers int
	HTTPMaxBodyBytes int64
}

func ParseConfig() Config {
	grpcAddr := flag.String("grpc-addr", getEnv("GRPC_ADDR", "0.0.0.0:4317"), "gRPC listen address for applications")
	httpAddr := flag.String("http-addr", getEnv("HTTP_ADDR", "0.0.0.0:4318"), "HTTP listen address for applications")
	centralURL := flag.String("central-url", getEnv("CENTRAL_URL", "http://127.0.0.1:8080/v1/traces"), "KubeTrace gateway endpoint")
	bufferDir := flag.String("buffer-dir", getEnv("BUFFER_DIR", filepath.Join(os.TempDir(), "kubetrace-agent-spool")), "Spool directory path")
	queueLimit := flag.Int("queue-limit", getEnvInt("QUEUE_LIMIT", 10000), "Maximum memory queue items")
	diskMaxFiles := flag.Int64("disk-limit-files", int64(getEnvInt("DISK_LIMIT_FILES", 2000)), "Maximum spool files on disk")
	forwarderWorkers := flag.Int("forwarder-workers", getEnvInt("FORWARDER_WORKERS", defaultForwarderWorkers), "Number of parallel forwarding workers")
	httpMaxBodyMB := flag.Int("http-max-body-mb", getEnvInt("HTTP_MAX_BODY_MB", defaultHTTPMaxBodyMB), "Maximum OTLP HTTP request body size in MiB")
	flag.Parse()

	if *queueLimit <= 0 {
		*queueLimit = 10000
	}
	if *diskMaxFiles <= 0 {
		*diskMaxFiles = 2000
	}
	if *forwarderWorkers <= 0 {
		*forwarderWorkers = defaultForwarderWorkers
	}
	if *httpMaxBodyMB <= 0 {
		*httpMaxBodyMB = defaultHTTPMaxBodyMB
	}

	return Config{
		GRPCAddr:         *grpcAddr,
		HTTPAddr:         *httpAddr,
		CentralURL:       *centralURL,
		BufferDir:        *bufferDir,
		QueueLimit:       *queueLimit,
		DiskMaxFiles:     *diskMaxFiles,
		SkipTLSVerify:    os.Getenv("SKIP_TLS_VERIFY") == "true",
		ForwarderWorkers: *forwarderWorkers,
		HTTPMaxBodyBytes: int64(*httpMaxBodyMB) << 20,
	}
}

func (cfg Config) Log() {
	log.Println("=== KubeTrace Host Agent ===")
	log.Printf("Listening on gRPC: %s", cfg.GRPCAddr)
	log.Printf("Listening on HTTP: %s", cfg.HTTPAddr)
	log.Printf("Forwarding to: %s", cfg.CentralURL)
	log.Printf("Disk spool path: %s", cfg.BufferDir)
	if cfg.SkipTLSVerify {
		log.Println("[agent] skipping TLS certificate verification (SKIP_TLS_VERIFY=true)")
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
		return n
	}
	return def
}
