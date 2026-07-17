package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	colpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

func (a *Agent) StartForwarders(ctx context.Context, workers int) {
	if workers <= 0 {
		workers = defaultForwarderWorkers
	}
	for i := 0; i < workers; i++ {
		go a.runForwarder(ctx)
	}
}

// Export implements OTLP TraceServiceServer interface to receive traces from SDKs.
func (a *Agent) Export(ctx context.Context, req *colpb.ExportTraceServiceRequest) (*colpb.ExportTraceServiceResponse, error) {
	a.injectMetadata(req)

	select {
	case a.memoryQueue <- req:
	default:
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
			a.flushDiskBuffer()
		}
	}
}

func (a *Agent) flushDiskBuffer() {
	if a.diskBuffer.Size() == 0 {
		return
	}

	req, path, err := a.diskBuffer.PopNext()
	if err != nil {
		log.Printf("[agent/error] pop disk buffer: %v", err)
		return
	}
	if req == nil {
		return
	}

	if err := a.sendWithRetry(req); err != nil {
		log.Printf("[agent/error] forward disk spool failed, holding queue: %v", err)
		return
	}
	a.diskBuffer.Remove(path)
}

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
