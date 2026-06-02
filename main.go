package main

import (
	"bytes"
	"context"
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
		grpcAddr     = flag.String("grpc-addr", "127.0.0.1:4317", "gRPC listen address for applications")
		centralURL   = flag.String("central-url", "http://127.0.0.1:8080/v1/traces", "KubeTrace gateway endpoint")
		bufferDir    = flag.String("buffer-dir", filepath.Join(os.TempDir(), "kubetrace-agent-spool"), "Spool directory path")
		queueLimit   = flag.Int("queue-limit", 500, "Maximum memory queue items")
		diskMaxFiles = flag.Int64("disk-limit-files", 2000, "Maximum spool files on disk")
	)
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("=== KubeTrace Host Agent ===")
	log.Printf("Listening on: %s", *grpcAddr)
	log.Printf("Forwarding to: %s", *centralURL)
	log.Printf("Disk spool path: %s", *bufferDir)

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

	// Graceful shutdown on signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down KubeTrace agent...")
	grpcServer.GracefulStop()
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
