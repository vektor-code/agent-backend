package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	colpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

type DiskBuffer struct {
	dir         string
	maxFiles    int64
	fileCounter int64
	mu          sync.Mutex
}

func NewDiskBuffer(dir string, maxFiles int64) *DiskBuffer {
	_ = os.MkdirAll(dir, 0755)
	
	// Scan directory to initialize fileCounter based on existing files
	var count int64 = 0
	files, err := os.ReadDir(dir)
	if err == nil {
		count = int64(len(files))
	}

	return &DiskBuffer{
		dir:         dir,
		maxFiles:    maxFiles,
		fileCounter: count,
	}
}

// Spool serializes the Export request and writes it to disk
func (db *DiskBuffer) Spool(req *colpb.ExportTraceServiceRequest) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Guard against infinite disk consumption
	files, err := os.ReadDir(db.dir)
	if err == nil && int64(len(files)) >= db.maxFiles {
		return fmt.Errorf("disk spool buffer is full (max files: %d)", db.maxFiles)
	}

	data, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("protobuf marshal: %w", err)
	}

	id := atomic.AddInt64(&db.fileCounter, 1)
	filename := filepath.Join(db.dir, fmt.Sprintf("%d_%d.spool", time.Now().UnixNano(), id))
	
	err = os.WriteFile(filename, data, 0644)
	if err != nil {
		return fmt.Errorf("write spool file: %w", err)
	}

	return nil
}

// PopNext reads, deserializes, and deletes the oldest spooled request on disk
func (db *DiskBuffer) PopNext() (*colpb.ExportTraceServiceRequest, string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	files, err := os.ReadDir(db.dir)
	if err != nil || len(files) == 0 {
		return nil, "", nil // Buffer is empty
	}

	// ReadDir returns entries sorted by name. Since files are named by unix nano timestamp,
	// the first entry is always the oldest (FIFO queue).
	oldest := files[0]
	path := filepath.Join(db.dir, oldest.Name())

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read spool file: %w", err)
	}

	var req colpb.ExportTraceServiceRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		// Corrupted spool file, remove it to prevent deadlock
		_ = os.Remove(path)
		return nil, "", fmt.Errorf("unmarshal spool protobuf: %w", err)
	}

	return &req, path, nil
}

func (db *DiskBuffer) Remove(path string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	_ = os.Remove(path)
}

func (db *DiskBuffer) Size() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	files, err := os.ReadDir(db.dir)
	if err != nil {
		return 0
	}
	return len(files)
}
