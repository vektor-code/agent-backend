package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	colpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Agent struct {
	colpb.UnimplementedTraceServiceServer
	centralURL    string
	memoryQueue   chan *colpb.ExportTraceServiceRequest
	diskBuffer    *DiskBuffer
	hostMetadata  []*commonpb.KeyValue
	client        *http.Client
}

func main() {
	var (
		grpcAddr     = flag.String("grpc-addr", getEnv("GRPC_ADDR", "0.0.0.0:4317"), "gRPC listen address for applications")
		httpAddr     = flag.String("http-addr", getEnv("HTTP_ADDR", "0.0.0.0:4318"), "HTTP listen address for applications")
		centralURL   = flag.String("central-url", getEnv("CENTRAL_URL", "http://127.0.0.1:8080/v1/traces"), "KubeTrace gateway endpoint")
		bufferDir    = flag.String("buffer-dir", getEnv("BUFFER_DIR", filepath.Join(os.TempDir(), "kubetrace-agent-spool")), "Spool directory path")
		queueLimit   = flag.Int("queue-limit", getEnvInt("QUEUE_LIMIT", 500), "Maximum memory queue items")
		diskMaxFiles = flag.Int64("disk-limit-files", int64(getEnvInt("DISK_LIMIT_FILES", 2000)), "Maximum spool files on disk")
	)
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("=== KubeTrace Host Agent ===")
	log.Printf("Listening on gRPC: %s", *grpcAddr)
	log.Printf("Listening on HTTP: %s", *httpAddr)
	log.Printf("Forwarding to: %s", *centralURL)
	log.Printf("Disk spool path: %s", *bufferDir)

	skipTLSVerify := os.Getenv("SKIP_TLS_VERIFY") == "true"
	if skipTLSVerify {
		log.Println("[agent] skipping TLS certificate verification (SKIP_TLS_VERIFY=true)")
	}

	agent := &Agent{
		centralURL:   *centralURL,
		memoryQueue:  make(chan *colpb.ExportTraceServiceRequest, *queueLimit),
		diskBuffer:   NewDiskBuffer(*bufferDir, *diskMaxFiles),
		hostMetadata: getHostMetadata(),
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: skipTLSVerify,
				},
			},
		},
	}

	// Start Background Forwarder Pipeline
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.runForwarder(ctx)

	// Set up gRPC listener
	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	colpb.RegisterTraceServiceServer(grpcServer, agent)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server: %v", err)
		}
	}()

	// Set up HTTP listener for OTLP/HTTP (port 4318)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", agent.HandleHTTP)
	httpServer := &http.Server{
		Addr:    *httpAddr,
		Handler: mux,
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server: %v", err)
		}
	}()

	// Graceful shutdown on signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down KubeTrace agent...")
	grpcServer.GracefulStop()
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := httpServer.Shutdown(ctxShutdown); err != nil {
		log.Printf("[agent/error] HTTP server shutdown: %v", err)
	}
	log.Println("KubeTrace agent stopped.")
}

// Export implements OTLP TraceServiceServer interface to receive traces from SDKs
func (a *Agent) Export(ctx context.Context, req *colpb.ExportTraceServiceRequest) (*colpb.ExportTraceServiceResponse, error) {
	// Inject host metadata attributes
	a.injectMetadata(req)

	select {
	case a.memoryQueue <- req:
		// Successfully queued in memory
	default:
		// Queue full. Spool to disk asynchronously to prevent blocking SDK
		go func() {
			if err := a.diskBuffer.Spool(req); err != nil {
				log.Printf("[agent/error] queue full & disk spool failed: %v", err)
			}
		}()
	}

	return &colpb.ExportTraceServiceResponse{}, nil
}

func (a *Agent) injectMetadata(req *colpb.ExportTraceServiceRequest) {
	for _, rs := range req.ResourceSpans {
		if rs.Resource == nil {
			rs.Resource = &resourcev1.Resource{}
		}
		
		// Map existing keys to avoid duplicates
		exists := make(map[string]bool, len(rs.Resource.Attributes))
		for _, attr := range rs.Resource.Attributes {
			exists[attr.Key] = true
		}

		for _, meta := range a.hostMetadata {
			if !exists[meta.Key] {
				rs.Resource.Attributes = append(rs.Resource.Attributes, meta)
			}
		}
	}
}

// runForwarder handles transmission of traces from memory queue or disk buffer to the gateway
func (a *Agent) runForwarder(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case req := <-a.memoryQueue:
			if err := a.sendWithRetry(req); err != nil {
				log.Printf("[agent/error] forward memory failed, spooling to disk: %v", err)
				_ = a.diskBuffer.Spool(req)
			}
		case <-ticker.C:
			// Memory queue empty. Try processing older traces from disk buffer
			if a.diskBuffer.Size() > 0 {
				req, path, err := a.diskBuffer.PopNext()
				if err != nil {
					log.Printf("[agent/error] pop disk buffer: %v", err)
					continue
				}
				if req == nil {
					continue
				}

				if err := a.sendWithRetry(req); err != nil {
					log.Printf("[agent/error] forward disk spool failed, holding queue: %v", err)
				} else {
					a.diskBuffer.Remove(path)
				}
			}
		}
	}
}

// sendWithRetry attempts to HTTP POST the request to KubeTrace Gateway with backing off retry
func (a *Agent) sendWithRetry(req *colpb.ExportTraceServiceRequest) error {
	data, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	backoff := 500 * time.Millisecond
	maxBackoff := 8 * time.Second
	attempts := 3

	for i := 0; i < attempts; i++ {
		success, err := a.postPayload(data)
		if success {
			return nil
		}

		log.Printf("[agent/warning] post attempt %d failed: %v. Retrying in %v...", i+1, err, backoff)
		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	return fmt.Errorf("exhausted retries")
}

func (a *Agent) postPayload(data []byte) (bool, error) {
	req, err := http.NewRequest("POST", a.centralURL, bytes.NewReader(data))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := a.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("gateway returned status %d: %s", resp.StatusCode, string(body))
	}

	return true, nil
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

// HandleHTTP handles OTLP/HTTP POST requests on /v1/traces
func (a *Agent) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var req colpb.ExportTraceServiceRequest
	contentType := r.Header.Get("Content-Type")

	if contentType == "application/json" || contentType == "application/json; charset=utf-8" {
		if err := protojson.Unmarshal(body, &req); err != nil {
			log.Printf("[agent/error] failed to parse JSON request: %v", err)
			http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
			return
		}
	} else {
		// Default to protobuf
		if err := proto.Unmarshal(body, &req); err != nil {
			log.Printf("[agent/error] failed to parse Protobuf request: %v", err)
			http.Error(w, fmt.Sprintf("Invalid protobuf: %v", err), http.StatusBadRequest)
			return
		}
	}

	_, _ = a.Export(r.Context(), &req)

	w.WriteHeader(http.StatusOK)
}

