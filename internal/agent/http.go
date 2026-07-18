package agent

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	colpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func (a *Agent) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.maxHTTPBodyBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, a.maxHTTPBodyBytes)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	req, err := decodeTraceRequest(body, r.Header.Get("Content-Type"))
	if err != nil {
		log.Printf("[agent/error] failed to parse request: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_, _ = a.Export(r.Context(), req)
	w.WriteHeader(http.StatusOK)
}

func decodeTraceRequest(body []byte, contentType string) (*colpb.ExportTraceServiceRequest, error) {
	var req colpb.ExportTraceServiceRequest
	if contentType == "application/json" || contentType == "application/json; charset=utf-8" {
		if err := protojson.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return &req, nil
	}

	if err := proto.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid protobuf: %w", err)
	}
	return &req, nil
}
