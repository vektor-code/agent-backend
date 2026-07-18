package agent

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/kubetrace/agent-backend/internal/buffer"
	colpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

type Agent struct {
	colpb.UnimplementedTraceServiceServer
	centralURL       string
	memoryQueue      chan *colpb.ExportTraceServiceRequest
	spoolQueue       chan *colpb.ExportTraceServiceRequest
	diskBuffer       *buffer.DiskBuffer
	hostMetadata     []*commonpb.KeyValue
	client           *http.Client
	maxHTTPBodyBytes int64
}

func New(cfg Config) *Agent {
	return &Agent{
		centralURL:       cfg.CentralURL,
		memoryQueue:      make(chan *colpb.ExportTraceServiceRequest, cfg.QueueLimit),
		spoolQueue:       make(chan *colpb.ExportTraceServiceRequest, max(1, cfg.QueueLimit/4)),
		diskBuffer:       buffer.New(cfg.BufferDir, cfg.DiskMaxFiles),
		hostMetadata:     getHostMetadata(),
		maxHTTPBodyBytes: cfg.HTTPMaxBodyBytes,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: cfg.SkipTLSVerify,
				},
			},
		},
	}
}
