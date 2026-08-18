package investigate

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	jobTimeout          = 25 * time.Second
	execTimeout         = 8 * time.Second
	resultTTL           = 30 * time.Second
	diagnosticWorkerMax = 1
)

// Limits are agent-side hard caps, not API suggestions.
type Limits struct {
	Global      int
	Namespace   int
	Workload    int
	Destination int
}

func DefaultLimits() Limits {
	return Limits{Global: 10, Namespace: 3, Workload: 1, Destination: 1}
}

var allowedChecks = map[string]int{
	CheckPodStatus:         1,
	CheckServiceResolution: 1,
	CheckEndpointHealth:    1,
	CheckEvents:            1,
	CheckNetworkPolicy:     1,
	CheckHTTPRequest:       2,
}

var allowedTypes = map[string]int{
	TypeDownstreamHTTPFailure: 3,
	TypeNetworkTimeout:        3,
	TypeClientError:           1,
}

var forbiddenCommandFields = []string{"command", "argv", "exec", "kubectl", "script"}

// RejectRemoteCommand refuses any payload that looks like remote command execution.
func RejectRemoteCommand(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(raw, &extra); err != nil {
		return fmt.Errorf("invalid intent")
	}
	for _, key := range forbiddenCommandFields {
		if _, ok := extra[key]; ok {
			return fmt.Errorf("refusing remote command field %q", key)
		}
	}
	return nil
}

type admission struct {
	Intent   Intent
	Checks   []string
	MaxLevel int
	Reason   string
}

func admit(in Intent, isAppNS func(string) bool, agentNS string) (admission, error) {
	maxForType, ok := allowedTypes[in.InvestigationType]
	if !ok {
		return admission{}, fmt.Errorf("investigation type %q is not allowlisted", in.InvestigationType)
	}
	ns := strings.TrimSpace(in.Namespace)
	if ns == "" {
		return admission{}, fmt.Errorf("namespace is required")
	}
	if isAppNS != nil && !isAppNS(ns) {
		return admission{}, fmt.Errorf("namespace %q is outside the agent's investigation boundary", ns)
	}
	maxLevel := in.MaxLevel
	if maxLevel <= 0 || maxLevel > maxForType {
		maxLevel = maxForType
	}
	if maxLevel > 3 {
		maxLevel = 3
	}
	var checks []string
	seen := map[string]bool{}
	for _, name := range in.Checks {
		level, allowed := allowedChecks[name]
		if !allowed {
			continue
		}
		if level > maxLevel {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		checks = append(checks, name)
	}
	if len(checks) == 0 {
		return admission{}, fmt.Errorf("no allowlisted checks remain")
	}
	checks = filterChecksForDestination(in.DestinationType, checks)
	if len(checks) == 0 {
		return admission{}, fmt.Errorf("no relevant checks remain for destination type %q", in.DestinationType)
	}
	if seen[CheckHTTPRequest] {
		if err := validateProbeURL(in.DestinationURL); err != nil {
			checks = without(checks, CheckHTTPRequest)
		}
	}
	if in.SourceWorkload != "" && agentNS != "" && ns == agentNS && isAgentWorkload(in.SourceWorkload) {
		return admission{}, fmt.Errorf("refusing to investigate the agent workload")
	}
	return admission{Intent: in, Checks: checks, MaxLevel: maxLevel}, nil
}

func validateProbeURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme")
	}
	if u.Host == "" || u.User != nil {
		return fmt.Errorf("invalid host")
	}
	return nil
}

func isAgentWorkload(name string) bool {
	n := strings.ToLower(name)
	return n == "agent-backend" || strings.Contains(n, "agent-backend")
}

func without(in []string, drop string) []string {
	out := in[:0]
	for _, v := range in {
		if v != drop {
			out = append(out, v)
		}
	}
	return out
}

func wants(checks []string, name string) bool {
	for _, c := range checks {
		if c == name {
			return true
		}
	}
	return false
}

func filterChecksForDestination(destType string, checks []string) []string {
	switch destType {
	case "localhost":
		return withoutMany(checks, CheckServiceResolution, CheckEndpointHealth, CheckNetworkPolicy)
	case "external_dns", "external_ip":
		return withoutMany(checks, CheckServiceResolution, CheckEndpointHealth, CheckNetworkPolicy)
	default:
		return checks
	}
}

func withoutMany(in []string, drops ...string) []string {
	for _, drop := range drops {
		in = without(in, drop)
	}
	return in
}
