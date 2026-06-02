package main

import (
	"os"
	"path/filepath"
	"testing"

	colpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestDiskBuffer_SpoolAndPop(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kubetrace-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	db := NewDiskBuffer(tempDir, 5)

	// Verify buffer starts empty
	if db.Size() != 0 {
		t.Errorf("expected initial size to be 0, got %d", db.Size())
	}

	// 1. Spool a mock OTLP request
	req := &colpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Spans: []*tracepb.Span{
							{Name: "test-span-spool", TraceId: []byte("1234567890123456")},
						},
					},
				},
			},
		},
	}

	err = db.Spool(req)
	if err != nil {
		t.Fatalf("failed to spool: %v", err)
	}

	if db.Size() != 1 {
		t.Errorf("expected size to be 1, got %d", db.Size())
	}

	// 2. Pop the request back
	poppedReq, path, err := db.PopNext()
	if err != nil {
		t.Fatalf("failed to pop: %v", err)
	}

	if poppedReq == nil {
		t.Fatal("expected popped request to not be nil")
	}

	if len(poppedReq.ResourceSpans) != 1 || poppedReq.ResourceSpans[0].ScopeSpans[0].Spans[0].Name != "test-span-spool" {
		t.Errorf("popped request span content mismatch")
	}

	if filepath.Dir(path) != tempDir {
		t.Errorf("expected path to be inside tempDir, got %s", path)
	}

	// 3. Remove the popped file
	db.Remove(path)

	if db.Size() != 0 {
		t.Errorf("expected size to be 0 after removal, got %d", db.Size())
	}
}

func TestDiskBuffer_Limit(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kubetrace-agent-limit-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Set limit to 2 files
	db := NewDiskBuffer(tempDir, 2)

	req := &colpb.ExportTraceServiceRequest{}

	// Spool 1
	if err := db.Spool(req); err != nil {
		t.Fatalf("failed spool 1: %v", err)
	}
	// Spool 2
	if err := db.Spool(req); err != nil {
		t.Fatalf("failed spool 2: %v", err)
	}

	// Spool 3 should fail
	err = db.Spool(req)
	if err == nil {
		t.Error("expected error due to size limit, but got none")
	}
}
