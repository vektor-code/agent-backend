package investigate

import "time"

const (
	StatusSkipped     = "skipped"
	StatusComplete    = "complete"
	StatusPartial     = "partial"
	StatusUnavailable = "unavailable"
	StatusRateLimited = "rate_limited"

	TypeDownstreamHTTPFailure = "downstream_http_failure"
	TypeNetworkTimeout        = "network_timeout"
	TypeClientError           = "client_error"

	CheckPodStatus         = "pod_status"
	CheckServiceResolution = "service_resolution"
	CheckEndpointHealth    = "endpoint_health"
	CheckEvents            = "events"
	CheckNetworkPolicy     = "network_policy"
	CheckHTTPRequest       = "http_request"

	KindObserved  = "observed"
	KindInference = "inference"
)

// Intent is the structured investigation request from the API. The agent
// never treats this as a shell command.
type Intent struct {
	InvestigationType string    `json:"investigationType"`
	ClusterID         string    `json:"clusterId"`
	Namespace         string    `json:"namespace"`
	SourceWorkload    string    `json:"sourceWorkload"`
	SourcePod         string    `json:"sourcePod,omitempty"`
	Destination       string    `json:"destination"`
	DestinationURL    string    `json:"destinationUrl,omitempty"`
	DestinationType   string    `json:"destinationType,omitempty"`
	RecordedHTTP      int       `json:"recordedHttp,omitempty"`
	Checks            []string  `json:"checks"`
	MaxLevel          int       `json:"maxLevel"`
	TraceID           string    `json:"traceId"`
	Fingerprint       string    `json:"fingerprint"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type Observation struct {
	Kind    string `json:"kind"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Level   int    `json:"level,omitempty"`
	OK      *bool  `json:"ok,omitempty"`
	Pod     string `json:"pod,omitempty"`
}

type Result struct {
	Fingerprint  string        `json:"fingerprint"`
	TraceID      string        `json:"traceId,omitempty"`
	Status       string        `json:"status"`
	LevelReached int           `json:"levelReached"`
	SkipReason   string        `json:"skipReason,omitempty"`
	Inference    string        `json:"inference,omitempty"`
	OriginalState string       `json:"originalState,omitempty"`
	CurrentState  string       `json:"currentState,omitempty"`
	Confidence   string        `json:"confidence,omitempty"`
	Observations []Observation `json:"observations,omitempty"`
	DurationMs   int64         `json:"durationMs,omitempty"`
}

func boolPtr(v bool) *bool { return &v }

func observed(code, message string, level int, ok bool, pod string) Observation {
	return Observation{Kind: KindObserved, Code: code, Message: message, Level: level, OK: boolPtr(ok), Pod: pod}
}

func inference(code, message string) Observation {
	return Observation{Kind: KindInference, Code: code, Message: message}
}
