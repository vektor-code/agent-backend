package investigate

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Executor struct {
	cluster      Cluster
	isAppNS      func(string) bool
	agentNS      string
	cache        *ttlCache
	limit        *limiter
	flight       *flight
}

func NewExecutor(cluster Cluster, isAppNS func(string) bool, agentNS string) *Executor {
	return &Executor{
		cluster: cluster,
		isAppNS: isAppNS,
		agentNS: agentNS,
		cache:   newTTLCache(resultTTL),
		limit:   newLimiter(DefaultLimits()),
		flight:  newFlight(),
	}
}

func (e *Executor) Run(ctx context.Context, in Intent) Result {
	start := time.Now()
	key := in.Fingerprint
	if key == "" {
		key = in.Namespace + "|" + in.SourceWorkload + "|" + in.Destination + "|" + in.InvestigationType
	}
	if cached, ok := e.cache.get(key); ok {
		cached.TraceID = in.TraceID
		return cached
	}
	out := e.flight.do(key, func() Result {
		if cached, ok := e.cache.get(key); ok {
			return cached
		}
		res := e.execute(ctx, in)
		if res.Status == StatusComplete || res.Status == StatusPartial {
			e.cache.set(key, res)
		}
		return res
	})
	out.TraceID = in.TraceID
	out.Fingerprint = key
	out.DurationMs = time.Since(start).Milliseconds()
	return out
}

func (e *Executor) execute(ctx context.Context, in Intent) Result {
	res := Result{Fingerprint: in.Fingerprint, TraceID: in.TraceID}
	admitted, err := admit(in, e.isAppNS, e.agentNS)
	if err != nil {
		res.Status = StatusSkipped
		res.SkipReason = err.Error()
		return res
	}
	if e.cluster == nil {
		res.Status = StatusUnavailable
		res.SkipReason = "No Kubernetes access in this agent"
		return res
	}
	release, ok := e.limit.acquire(in.Namespace, in.SourceWorkload, in.Destination)
	if !ok {
		res.Status = StatusRateLimited
		res.SkipReason = "Live verification deferred: another check is already in flight for this workload or destination"
		return res
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	host, _ := splitHostPort(in.Destination)
	if wants(admitted.Checks, CheckPodStatus) || wants(admitted.Checks, CheckEvents) {
		res.Observations = append(res.Observations, e.level1Pod(ctx, in, host)...)
		res.LevelReached = max(res.LevelReached, 1)
	}
	if wants(admitted.Checks, CheckServiceResolution) || wants(admitted.Checks, CheckEndpointHealth) {
		res.Observations = append(res.Observations, e.level1Service(ctx, in, host)...)
		res.LevelReached = max(res.LevelReached, 1)
	}
	if wants(admitted.Checks, CheckNetworkPolicy) {
		res.Observations = append(res.Observations, e.level1NetworkPolicy(ctx, in)...)
		res.LevelReached = max(res.LevelReached, 1)
	}
	if wants(admitted.Checks, CheckHTTPRequest) && admitted.MaxLevel >= 2 {
		obs, level := e.httpRequest(ctx, in, admitted.MaxLevel)
		if obs != nil {
			res.Observations = append(res.Observations, *obs)
			res.LevelReached = max(res.LevelReached, level)
		}
	}

	res.Inference, res.Confidence = conclude(in, res.Observations)
	if res.Inference != "" {
		res.Observations = append(res.Observations, inference("conclusion", res.Inference))
	}
	res.Status = StatusComplete
	if !anyObservedOK(res.Observations) {
		res.Status = StatusPartial
	}
	return res
}

func (e *Executor) level1Pod(ctx context.Context, in Intent, host string) []Observation {
	var out []Observation
	destPod, _ := e.cluster.FindPodByIP(ctx, in.Namespace, host)
	if destPod == nil && in.SourcePod != "" {
		if p, err := e.cluster.GetPod(ctx, in.Namespace, in.SourcePod); err == nil {
			destPod = p
		}
	}
	if destPod == nil {
		out = append(out, observed("pod_status", "Could not map "+in.Destination+" to a pod in "+in.Namespace, 1, false, ""))
		return out
	}
	detail := fmt.Sprintf("Pod %s on node %s is %s, Ready=%v, restarts=%d", destPod.Name, destPod.Node, destPod.Phase, destPod.Ready, destPod.Restarts)
	out = append(out, observed("pod_status", detail, 1, destPod.Ready, destPod.Name))
	if dep, err := e.cluster.FindDeployment(ctx, in.Namespace, destPod.Workload); err == nil && dep != nil {
		ok := dep.Ready > 0
		out = append(out, observed("deployment_status", fmt.Sprintf("Deployment %s Ready %d/%d", dep.Name, dep.Ready, dep.Desired), 1, ok, destPod.Name))
	}
	if wants(in.Checks, CheckEvents) {
		if ev, err := e.cluster.ListWarningEvents(ctx, destPod.Namespace, destPod.Name); err == nil && len(ev) > 0 {
			out = append(out, observed("events", "Recent warnings: "+strings.Join(ev, "; "), 1, false, destPod.Name))
		}
	}
	return out
}

func (e *Executor) level1Service(ctx context.Context, in Intent, host string) []Observation {
	var out []Observation
	svc, _ := e.cluster.FindServiceByIP(ctx, in.Namespace, host)
	if svc == nil {
		if pod, _ := e.cluster.FindPodByIP(ctx, in.Namespace, host); pod != nil {
			out = append(out, observed("service_resolution", fmt.Sprintf("%s belongs to pod %s (not a Service ClusterIP)", host, pod.Name), 1, true, pod.Name))
			return out
		}
		out = append(out, observed("service_resolution", "Could not map "+host+" to a Service in "+in.Namespace, 1, false, ""))
		return out
	}
	out = append(out, observed("service_resolution", fmt.Sprintf("Service %s ClusterIP %s", svc.Name, svc.ClusterIP), 1, true, ""))
	if !wants(in.Checks, CheckEndpointHealth) {
		return out
	}
	ep, err := e.cluster.GetEndpoints(ctx, svc.Namespace, svc.Name)
	if err != nil || ep == nil {
		out = append(out, observed("endpoint_health", "Endpoints for "+svc.Name+" could not be read", 1, false, ""))
		return out
	}
	ok := containsIP(ep.Ready, host) || len(ep.Ready) > 0
	src := ep.Source
	if src == "" {
		src = "Endpoints"
	}
	out = append(out, observed("endpoint_health", fmt.Sprintf("%s %s has %d ready endpoint(s)", src, svc.Name, len(ep.Ready)), 1, ok, ""))
	return out
}

func (e *Executor) level1NetworkPolicy(ctx context.Context, in Intent) []Observation {
	nps, err := e.cluster.ListNetworkPolicies(ctx, in.Namespace)
	if err != nil {
		return []Observation{observed("network_policy", "NetworkPolicy list failed: "+err.Error(), 1, false, "")}
	}
	if len(nps) == 0 {
		return []Observation{observed("network_policy", "No NetworkPolicy objects in "+in.Namespace, 1, true, "")}
	}
	names := make([]string, 0, len(nps))
	for _, np := range nps {
		names = append(names, np.Name)
	}
	return []Observation{observed("network_policy", fmt.Sprintf("%d NetworkPolicy object(s) in %s: %s", len(nps), in.Namespace, strings.Join(names, ", ")), 1, true, "")}
}

func (e *Executor) httpRequest(ctx context.Context, in Intent, maxLevel int) (*Observation, int) {
	cmd, err := httpProbeArgv(in.DestinationURL)
	if err != nil {
		obs := observed("http_request", "Destination URL is not a permitted probe target", 2, false, "")
		return &obs, 1
	}
	pod, level, how := e.probePod(ctx, in, maxLevel)
	if pod == nil {
		obs := observed("http_request", how, 2, false, "")
		return &obs, 1
	}
	if level >= 3 {
		release, ok := e.limit.acquireWorker(in.Namespace)
		if !ok {
			obs := observed("http_request", "exec skipped: diagnostic worker already in use in "+in.Namespace, 2, false, "")
			return &obs, 1
		}
		defer release()
	}
	res, execErr := e.cluster.Exec(ctx, pod.Namespace, pod.Name, pod.Container, cmd)
	detail := how
	status := parseHTTPStatus(res)
	ok := false
	if execErr != nil && (res == nil || strings.TrimSpace(res.Stdout+res.Stderr) == "") {
		detail += "; exec failed: " + execErr.Error()
	} else {
		if status > 0 {
			detail += fmt.Sprintf("; request from %s returned HTTP %d", pod.Name, status)
			ok = in.RecordedHTTP == 0 || status == in.RecordedHTTP || (in.RecordedHTTP >= 500 && status >= 500)
		} else if res != nil {
			snippet := strings.TrimSpace(res.Stdout + " " + res.Stderr)
			if len(snippet) > 240 {
				snippet = snippet[:240]
			}
			detail += "; probe output: " + snippet
		}
		if res != nil && strings.Contains(res.Stdout+res.Stderr, "PROBE_INSTALLED") {
			detail += "; ephemeral wget was installed for the check and removed afterward"
		}
	}
	obs := observed("http_request", detail, level, ok, pod.Name)
	return &obs, level
}

func (e *Executor) probePod(ctx context.Context, in Intent, maxLevel int) (*PodView, int, string) {
	if in.SourcePod != "" {
		if p, err := e.cluster.GetPod(ctx, in.Namespace, in.SourcePod); err == nil && p != nil && p.Phase == "Running" && !p.Deleting && !isAgentWorkload(p.Name) && !isAgentWorkload(p.Workload) {
			return p, 2, "exec into existing source pod " + p.Name
		}
	}
	if pods, err := e.cluster.FindRunningPods(ctx, in.Namespace, in.SourceWorkload); err == nil && len(pods) > 0 {
		return pods[0], 2, "exec into existing " + in.SourceWorkload + " pod " + pods[0].Name
	}
	if maxLevel >= 3 {
		if d, err := e.cluster.FindDiagnosticPod(ctx, in.Namespace); err == nil && d != nil {
			return d, 3, "no application pod available; reused diagnostic worker " + d.Name + " in " + in.Namespace
		}
		return nil, 1, "exec skipped: no running source pod and no reusable diagnostic worker in " + in.Namespace
	}
	return nil, 1, "exec skipped: no running source pod in " + in.Namespace
}

func conclude(in Intent, obs []Observation) (string, string) {
	var probe *Observation
	var ready *Observation
	for i := range obs {
		o := &obs[i]
		if o.Kind != KindObserved {
			continue
		}
		if o.Code == "http_request" {
			probe = o
		}
		if o.Code == "pod_status" {
			ready = o
		}
	}
	if probe != nil && probe.OK != nil && *probe.OK && in.InvestigationType == TypeDownstreamHTTPFailure {
		return "backend is likely responsible", "HIGH"
	}
	if probe != nil && probe.OK != nil && !*probe.OK && strings.Contains(probe.Message, "returned HTTP") {
		return "A live request from the source workload did not reproduce the recorded HTTP status; the failure may be transient", "MEDIUM"
	}
	if ready != nil && ready.OK != nil && *ready.OK {
		return "The target workload is Ready; the recorded failure is consistent with application-level behavior rather than a missing endpoint", "MEDIUM"
	}
	if ready != nil && ready.OK != nil && !*ready.OK {
		return "The target pod is not Ready; infrastructure state may explain the recorded failure", "MEDIUM"
	}
	return "Live Kubernetes inspection completed; see observed evidence", "LOW"
}

func anyObservedOK(obs []Observation) bool {
	for _, o := range obs {
		if o.Kind == KindObserved && o.OK != nil && *o.OK {
			return true
		}
	}
	return len(obs) == 0
}

func containsIP(list []string, ip string) bool {
	for _, item := range list {
		if item == ip {
			return true
		}
	}
	return false
}

func splitHostPort(dest string) (string, string) {
	host, port, err := net.SplitHostPort(dest)
	if err != nil {
		return dest, ""
	}
	return host, port
}

var httpStatusRe = regexp.MustCompile(`(?i)HTTP/[0-9.]+ (\d{3})|HTTP_STATUS\s+(\d{3})`)

func parseHTTPStatus(res *ExecResult) int {
	if res == nil {
		return 0
	}
	m := httpStatusRe.FindStringSubmatch(res.Stdout + "\n" + res.Stderr)
	if len(m) < 2 {
		return 0
	}
	for _, g := range m[1:] {
		if g == "" {
			continue
		}
		n, _ := strconv.Atoi(g)
		return n
	}
	return 0
}
