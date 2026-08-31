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
	cluster Cluster
	isAppNS func(string) bool
	agentNS string
	cache   *ttlCache
	limit   *limiter
	flight  *flight
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
	proto := protocolOf(in)
	if proto.ID != "" && proto.ID != "tcp" && proto.ID != "http" && proto.ID != "https" {
		res.Observations = append(res.Observations, observedInfo("dest_protocol",
			fmt.Sprintf("Destination protocol is %s (%s). An HTTP GET is not a valid probe for this port.", proto.Label, proto.Source), 1, ""))
	}
	if msg := destinationContextMessage(in.DestinationType, in.Destination); msg != "" {
		res.Observations = append(res.Observations, observed("destination_context", msg, 1, true, ""))
	}
	if wants(admitted.Checks, CheckPodStatus) || wants(admitted.Checks, CheckEvents) {
		res.Observations = append(res.Observations, e.level1Pod(ctx, in, host, proto)...)
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
		obs, level := e.liveProbe(ctx, in, admitted.MaxLevel, proto)
		if obs != nil {
			res.Observations = append(res.Observations, *obs)
			res.LevelReached = max(res.LevelReached, level)
		}
	}

	res.OriginalState, res.CurrentState, res.Inference, res.Confidence = conclude(in, res.Observations, proto)
	if res.Inference != "" {
		res.Observations = append(res.Observations, inference("conclusion", res.Inference))
	}
	res.Status = StatusComplete
	if !anyObservedOK(res.Observations) {
		res.Status = StatusPartial
	}
	return res
}

func (e *Executor) level1Pod(ctx context.Context, in Intent, host string, proto Protocol) []Observation {
	if destIsExternalOrLocal(in.DestinationType, host) {
		return e.sourcePodStatus(ctx, in)
	}
	var out []Observation
	destPod, _ := e.cluster.FindPodByIP(ctx, in.Namespace, host)
	if destPod != nil {
		if destPod.Namespace != "" && destPod.Namespace != in.Namespace {
			out = append(out, observed("dest_identity", fmt.Sprintf("%s is pod %s in namespace %s (source namespace is %s)", host, destPod.Name, destPod.Namespace, in.Namespace), 1, true, destPod.Name))
		}
		out = append(out, e.podFacts(ctx, in, destPod, "pod_status", "Pod")...)
		if in.SourcePod != "" && in.SourcePod != destPod.Name {
			out = append(out, e.sourcePodStatus(ctx, in)...)
		}
		return out
	}
	if owner, _ := e.cluster.FindEndpointOwnerByIP(ctx, host); owner != nil {
		out = append(out, observed("dest_identity", fmt.Sprintf("%s is an Endpoint of %s/%s (%s). This is a common pattern for a managed database registered into the cluster.", host, owner.Namespace, owner.Service, owner.Source), 1, true, ""))
		out = append(out, e.sourcePodStatus(ctx, in)...)
		return out
	}
	if node, _ := e.cluster.FindNodeByIP(ctx, host); node != nil {
		out = append(out, observed("dest_identity", fmt.Sprintf("%s is node %s (%s). Traffic may be hostNetwork, NodePort, or a process on the node — not a pod IP.", host, node.Name, node.Type), 1, true, ""))
		out = append(out, e.sourcePodStatus(ctx, in)...)
		return out
	}
	out = append(out, observed("dest_unmapped", unmappedMessage(in, host, proto), 1, false, ""))
	out = append(out, e.sourcePodStatus(ctx, in)...)
	return out
}

func unmappedMessage(in Intent, host string, proto Protocol) string {
	dest := in.Destination
	if dest == "" {
		dest = host
	}
	base := "Could not map " + dest + " to a Pod, Service ClusterIP, Endpoint, or Node"
	if in.Namespace != "" {
		base += " (searched cluster-wide, not only " + in.Namespace + ")"
	}
	if proto.Kind == "database" || proto.Kind == "cache" || proto.Kind == "messaging" {
		return base + ". Port belongs to " + proto.Label + " — this is likely a VPC or managed " + proto.Label + " address, so Kubernetes Service mapping does not apply."
	}
	return base + "."
}

func (e *Executor) sourcePodStatus(ctx context.Context, in Intent) []Observation {
	if in.SourcePod == "" {
		return nil
	}
	p, err := e.cluster.GetPod(ctx, in.Namespace, in.SourcePod)
	if err != nil || p == nil {
		return []Observation{observed("source_pod_status", "Source pod "+in.SourcePod+" was not found in "+in.Namespace, 1, false, in.SourcePod)}
	}
	return e.podFacts(ctx, in, p, "source_pod_status", "Source pod")
}

func (e *Executor) podFacts(ctx context.Context, in Intent, pod *PodView, code, label string) []Observation {
	var out []Observation
	detail := fmt.Sprintf("%s %s is %s on %s (Ready=%v)", label, pod.Name, pod.Phase, pod.Node, pod.Ready)
	out = append(out, observed(code, detail, 1, pod.Ready, pod.Name))
	if dep, err := e.cluster.FindDeployment(ctx, pod.Namespace, pod.Workload); err == nil && dep != nil {
		ok := dep.Ready > 0
		obs := observed("deployment_status", fmt.Sprintf("Deployment %s is Ready %d/%d", dep.Name, dep.Ready, dep.Desired), 1, ok, pod.Name)
		if label == "Source pod" {
			obs.Hop = HopSource
		}
		out = append(out, obs)
	}
	if wants(in.Checks, CheckEvents) {
		if ev, err := e.cluster.ListWarningEvents(ctx, pod.Namespace, pod.Name); err == nil && len(ev) > 0 {
			out = append(out, observed("events", "Recent warnings: "+strings.Join(ev, "; "), 1, false, pod.Name))
		}
	}
	return out
}

func (e *Executor) level1Service(ctx context.Context, in Intent, host string) []Observation {
	var out []Observation
	if isLocalhostTarget(host) {
		out = append(out, observed("context_local_target", "This call stayed inside the same pod, so Kubernetes Service mapping does not apply", 1, true, ""))
		return out
	}
	svc, _ := e.cluster.FindServiceByIP(ctx, in.Namespace, host)
	if svc == nil {
		if pod, _ := e.cluster.FindPodByIP(ctx, in.Namespace, host); pod != nil {
			out = append(out, observed("service_resolution", fmt.Sprintf("%s belongs to pod %s/%s (not a Service ClusterIP)", host, pod.Namespace, pod.Name), 1, true, pod.Name))
			return out
		}
		out = append(out, observed("service_resolution", "Could not map "+host+" to a Service ClusterIP, ExternalIP, or LoadBalancer address", 1, false, ""))
		return out
	}
	where := svc.Name
	if svc.Namespace != "" {
		where = svc.Namespace + "/" + svc.Name
	}
	out = append(out, observed("service_resolution", fmt.Sprintf("Service %s ClusterIP %s", where, svc.ClusterIP), 1, true, ""))
	if !wants(in.Checks, CheckEndpointHealth) {
		return out
	}
	epNS := svc.Namespace
	if epNS == "" {
		epNS = in.Namespace
	}
	ep, err := e.cluster.GetEndpoints(ctx, epNS, svc.Name)
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
		return []Observation{observed("network_policy", "No NetworkPolicy objects in "+in.Namespace+" (Kubernetes default-allow; a CNI such as Cilium or Calico may still enforce policy outside this API).", 1, true, "")}
	}
	names := make([]string, 0, len(nps))
	isolates := false
	for _, np := range nps {
		names = append(names, np.Name)
		if np.IsolatesAll {
			isolates = true
		}
	}
	msg := fmt.Sprintf("%d NetworkPolicy object(s) in %s: %s. Path was not simulated, so this does not prove traffic is allowed.", len(nps), in.Namespace, strings.Join(names, ", "))
	if isolates {
		msg = fmt.Sprintf("%d NetworkPolicy object(s) in %s include a namespace-wide selector: %s. Egress to this destination may be restricted; path was not simulated.", len(nps), in.Namespace, strings.Join(names, ", "))
		return []Observation{observed("network_policy", msg, 1, false, "")}
	}
	return []Observation{observedInfo("network_policy", msg, 1, "")}
}

func (e *Executor) liveProbe(ctx context.Context, in Intent, maxLevel int, proto Protocol) (*Observation, int) {
	if proto.Mode == ProbeTCP {
		return e.tcpConnect(ctx, in, maxLevel, proto)
	}
	return e.httpRequest(ctx, in, maxLevel)
}

func (e *Executor) tcpConnect(ctx context.Context, in Intent, maxLevel int, proto Protocol) (*Observation, int) {
	host, port := splitHostPort(in.Destination)
	cmds, err := tcpProbeCommands(host, port)
	if err != nil || len(cmds) == 0 {
		obs := observed("tcp_connect", "Destination is not a permitted TCP probe target", 2, false, "")
		return &obs, 1
	}
	pod, level, how := e.probePod(ctx, in, maxLevel)
	if pod == nil {
		obs := observed("tcp_connect", how, 2, false, "")
		return &obs, 1
	}
	label := proto.Label
	if label == "" {
		label = "TCP"
	}
	for _, cmd := range cmds {
		obs, missing := e.execTCP(ctx, in, pod, level, how, cmd, label)
		if missing {
			continue
		}
		return obs, level
	}
	if maxLevel >= 3 && (pod.Labels == nil || pod.Labels["app"] != "crnet-diagnostics") {
		if worker, err := e.cluster.FindDiagnosticPod(ctx, in.Namespace); err == nil && worker != nil && worker.Name != pod.Name {
			for _, cmd := range cmds {
				wobs, missing := e.execTCP(ctx, in, worker, 3, "source container has no shell; reused diagnostic worker "+worker.Name, cmd, label)
				if missing {
					continue
				}
				return wobs, 3
			}
		}
	}
	skip := observed("tcp_connect", "Live TCP probe skipped: container has no usable TCP client for this runtime", level, false, pod.Name)
	return &skip, level
}

func (e *Executor) execTCP(ctx context.Context, in Intent, pod *PodView, level int, how string, cmd []string, label string) (*Observation, bool) {
	if level >= 3 {
		release, ok := e.limit.acquireWorker(in.Namespace)
		if !ok {
			obs := observed("tcp_connect", "Live retry skipped: diagnostic worker already in use in "+in.Namespace, 2, false, "")
			return &obs, false
		}
		defer release()
	}
	res, execErr := e.cluster.Exec(ctx, pod.Namespace, pod.Name, pod.Container, cmd)
	if missingExecBinary(execErr, res) {
		return nil, true
	}
	from := "source pod " + pod.Name
	if strings.Contains(how, "diagnostic worker") {
		from = "diagnostic worker " + pod.Name
	}
	detail := fmt.Sprintf("Live %s TCP check from %s to %s", label, from, in.Destination)
	ok := false
	body := ""
	if res != nil {
		body = strings.TrimSpace(res.Stdout + " " + res.Stderr)
	}
	lower := strings.ToLower(body + " " + errString(execErr))
	switch {
	case strings.Contains(lower, "tcp_open") || (execErr == nil && !strings.Contains(lower, "tcp_fail") && !strings.Contains(lower, "refused") && !strings.Contains(lower, "timed out") && !strings.Contains(lower, "timeout")):
		detail += ": TCP open (no application handshake)"
		ok = true
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "econnrefused"):
		detail += ": connection refused"
	case strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout") || strings.Contains(lower, "etimedout") || strings.Contains(lower, "i/o timeout"):
		detail += ": timed out"
	case execErr != nil && body == "":
		detail += " failed: " + execErr.Error()
	default:
		if len(body) > 180 {
			body = body[:180]
		}
		if body != "" {
			detail += "; probe output: " + body
		} else if execErr != nil {
			detail += " failed: " + execErr.Error()
		}
	}
	obs := observed("tcp_connect", detail, level, ok, pod.Name)
	return &obs, false
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (e *Executor) httpRequest(ctx context.Context, in Intent, maxLevel int) (*Observation, int) {
	cmds, err := probeCommands(in.DestinationURL)
	if err != nil || len(cmds) == 0 {
		obs := observed("http_request", "Destination URL is not a permitted probe target", 2, false, "")
		return &obs, 1
	}
	pod, level, how := e.probePod(ctx, in, maxLevel)
	if pod == nil {
		obs := observed("http_request", how, 2, false, "")
		return &obs, 1
	}
	obs, ran := e.runProbeCommands(ctx, in, pod, level, how, cmds)
	if ran {
		return obs, level
	}
	if maxLevel >= 3 && (pod.Labels == nil || pod.Labels["app"] != "crnet-diagnostics") {
		if worker, err := e.cluster.FindDiagnosticPod(ctx, in.Namespace); err == nil && worker != nil && worker.Name != pod.Name {
			wobs, wRan := e.runProbeCommands(ctx, in, worker, 3, "source container has no shell; reused diagnostic worker "+worker.Name, cmds)
			if wRan {
				return wobs, 3
			}
		}
	}
	skip := observed("http_request", "Live HTTP probe skipped: container has no usable HTTP client for this runtime", level, false, pod.Name)
	return &skip, level
}

func (e *Executor) runProbeCommands(ctx context.Context, in Intent, pod *PodView, level int, how string, cmds [][]string) (*Observation, bool) {
	for _, cmd := range cmds {
		obs, missing := e.execProbe(ctx, in, pod, level, how, cmd)
		if missing {
			continue
		}
		return obs, true
	}
	return nil, false
}

func (e *Executor) execProbe(ctx context.Context, in Intent, pod *PodView, level int, how string, cmd []string) (*Observation, bool) {
	if level >= 3 {
		release, ok := e.limit.acquireWorker(in.Namespace)
		if !ok {
			obs := observed("http_request", "Live retry skipped: diagnostic worker already in use in "+in.Namespace, 2, false, "")
			return &obs, false
		}
		defer release()
	}
	res, execErr := e.cluster.Exec(ctx, pod.Namespace, pod.Name, pod.Container, cmd)
	if missingExecBinary(execErr, res) {
		return nil, true
	}
	from := "source pod " + pod.Name
	if strings.Contains(how, "diagnostic worker") {
		from = "diagnostic worker " + pod.Name
	}
	target := in.Destination
	detail := "Live retry from " + from
	if target != "" {
		detail += " to " + target
	}
	status := parseHTTPStatus(res)
	ok := false
	if execErr != nil && (res == nil || strings.TrimSpace(res.Stdout+res.Stderr) == "") {
		detail += " failed: " + execErr.Error()
	} else if status > 0 {
		detail += fmt.Sprintf(" returned HTTP %d", status)
		ok = in.RecordedHTTP > 0 && (status == in.RecordedHTTP || (in.RecordedHTTP >= 500 && status >= 500))
		if !ok {
			if in.RecordedHTTP > 0 {
				detail += fmt.Sprintf(" (recorded failure was HTTP %d)", in.RecordedHTTP)
			} else {
				detail += " (did not match the recorded transport failure)"
			}
		}
	} else if res != nil {
		snippet := strings.TrimSpace(res.Stdout + " " + res.Stderr)
		if len(snippet) > 240 {
			snippet = snippet[:240]
		}
		detail += "; probe output: " + snippet
		if strings.Contains(res.Stdout+res.Stderr, "PROBE_INSTALLED") {
			detail += "; an ephemeral client was installed for the check and removed afterward"
		}
	}
	obs := observed("http_request", detail, level, ok, pod.Name)
	return &obs, false
}

func (e *Executor) probePod(ctx context.Context, in Intent, maxLevel int) (*PodView, int, string) {
	if in.SourcePod != "" {
		if p, err := e.cluster.GetPod(ctx, in.Namespace, in.SourcePod); err == nil && p != nil && p.Phase == "Running" && !p.Deleting && !isAgentWorkload(p.Name) && !isAgentWorkload(p.Workload) {
			return p, 2, "source pod " + p.Name
		}
	}
	if pods, err := e.cluster.FindRunningPods(ctx, in.Namespace, in.SourceWorkload); err == nil && len(pods) > 0 {
		return pods[0], 2, "source pod " + pods[0].Name
	}
	if maxLevel >= 3 {
		if d, err := e.cluster.FindDiagnosticPod(ctx, in.Namespace); err == nil && d != nil {
			return d, 3, "no application pod available; reused diagnostic worker " + d.Name + " in " + in.Namespace
		}
		return nil, 1, "Live retry skipped: no running source pod and no reusable diagnostic worker in " + in.Namespace
	}
	return nil, 1, "Live retry skipped: no running source pod in " + in.Namespace
}

func conclude(in Intent, obs []Observation, proto Protocol) (string, string, string, string) {
	var probe *Observation
	var destReady *Observation
	var sourceReady *Observation
	var destUnmapped *Observation
	var destIdentity *Observation
	for i := range obs {
		o := &obs[i]
		if o.Kind != KindObserved {
			continue
		}
		switch o.Code {
		case "http_request", "tcp_connect":
			probe = o
		case "pod_status":
			destReady = o
		case "source_pod_status":
			sourceReady = o
		case "dest_unmapped":
			destUnmapped = o
		case "dest_identity":
			destIdentity = o
		}
	}
	original := originalStateFor(in)
	if probe != nil && probe.Code == "tcp_connect" {
		open := probe.OK != nil && *probe.OK
		if open && destUnmapped != nil {
			label := proto.Label
			if label == "" {
				label = "the remote port"
			}
			return original, "L4 reachable (not a cluster object)",
				fmt.Sprintf("A live TCP check from the source pod reached this address. It is not a Pod, Service, Endpoint, or Node in the cluster. Port is %s, so treat this as an external or VPC %s — the recorded timeout is wait on that backend, not a missing Kubernetes endpoint.", label, label),
				"MEDIUM"
		}
		if open && destIdentity != nil {
			return original, "L4 reachable",
				"A live TCP check from the source pod reached this address. Kubernetes identity was resolved; the original failure was not an L4 drop at investigation time.",
				"MEDIUM"
		}
		if open {
			return original, "L4 reachable",
				"A live TCP check from the source pod reached this address. The recorded timeout is not a current TCP connect failure.",
				"MEDIUM"
		}
		if probeShowsTransportFailure(probe.Message) {
			return original, "Still failing",
				"A live TCP check from the source pod still could not complete to this destination. That supports a transport / upstream connectivity classification.",
				"MEDIUM"
		}
	}
	if probe != nil && probe.OK != nil && *probe.OK && in.InvestigationType == TypeDownstreamHTTPFailure && probe.Code == "http_request" {
		return original, "Reproduced", "The live retry reproduced the recorded HTTP failure from the same workload context", "HIGH"
	}
	if probe != nil && probe.OK != nil && !*probe.OK && strings.Contains(probe.Message, "returned HTTP") {
		msg := "The original request experienced a transport-level or upstream failure. A live retry from the same source pod returned a different HTTP response, so the failure was not reproduced at investigation time"
		if strings.Contains(probe.Message, "diagnostic worker") {
			msg = "A live retry from a diagnostic worker returned an HTTP response. That does not replay the original source-container path, so the recorded failure was not reproduced"
		}
		return original, "Not reproduced", msg, "MEDIUM"
	}
	if probe != nil && probe.OK != nil && !*probe.OK && in.InvestigationType == TypeNetworkTimeout && probeShowsTransportFailure(probe.Message) {
		return original, "Still failing", "A live retry from the same source pod still could not complete an HTTP response to this destination. That supports the original transport / upstream connectivity classification", "MEDIUM"
	}
	if probeSkippedNoClient(probe) {
		inf := "Live probe could not run because the source container has no /bin/sh and no usable client. Original classification is unchanged"
		if probe != nil && probe.Code == "http_request" {
			inf = "Live HTTP probe could not run because the source container has no /bin/sh and no usable HTTP client. Original classification is unchanged"
		}
		if sourceNotReady(sourceReady) {
			inf += ". The source pod is currently not Ready; that is live source state and does not by itself explain the recorded external upstream failure"
		}
		return original, "Live retry not possible", inf, "MEDIUM"
	}
	if destReady != nil && destReady.OK != nil && !*destReady.OK && strings.Contains(destReady.Message, "Ready=") {
		return original, "Currently degraded", "The destination pod is not Ready; infrastructure state may explain the recorded failure", "MEDIUM"
	}
	if destIsExternalOrLocal(in.DestinationType, "") && sourceNotReady(sourceReady) {
		return original, "Source not ready", "The source pod is currently not Ready. That is live source state and does not by itself prove the external destination failed", "MEDIUM"
	}
	if destUnmapped != nil && (proto.Kind == "database" || proto.Kind == "cache" || proto.Kind == "messaging") {
		label := proto.Label
		if label == "" {
			label = "this protocol"
		}
		return original, "Not a cluster object",
			fmt.Sprintf("This address is not a Pod, Service, Endpoint, or Node in the cluster. Port is %s, so Kubernetes verification cannot prove a missing workload. Investigate the %s instance (VPC, managed service, or ExternalName).", label, label),
			"MEDIUM"
	}
	if probe == nil && destReady != nil && destReady.OK != nil && *destReady.OK {
		return original, "Not verified", "Destination workload is currently Ready; no live HTTP probe evidence was collected", "LOW"
	}
	if probe == nil && sourceReady != nil && sourceReady.OK != nil && *sourceReady.OK {
		return original, "Not verified", "Source workload is currently Ready; no live HTTP probe evidence was collected", "LOW"
	}
	return original, "Inconclusive", "Live verification completed; see observed evidence", "LOW"
}

func probeSkippedNoClient(probe *Observation) bool {
	if probe == nil {
		return false
	}
	msg := strings.ToLower(probe.Message)
	return strings.Contains(msg, "no usable http client") ||
		strings.Contains(msg, "no /bin/sh") ||
		strings.Contains(msg, "no usable HTTP client") ||
		strings.Contains(msg, "no usable tcp client")
}

func probeShowsTransportFailure(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "tcp_fail") ||
		strings.Contains(lower, "no route to host") ||
		strings.Contains(lower, "network is unreachable")
}

func sourceNotReady(o *Observation) bool {
	return o != nil && o.OK != nil && !*o.OK
}

func destIsExternalOrLocal(destType, host string) bool {
	switch destType {
	case "localhost", "external_dns", "external_ip":
		return true
	}
	return isLocalhostTarget(host)
}

func missingExecBinary(err error, res *ExecResult) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if res != nil {
		s += " " + strings.ToLower(res.Stdout+" "+res.Stderr)
	}
	return strings.Contains(s, "no such file") ||
		strings.Contains(s, "executable file not found") ||
		strings.Contains(s, "stat /bin/sh")
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

func isLocalhostTarget(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func destinationContextMessage(destType, destination string) string {
	host, _ := splitHostPort(destination)
	if destType == "localhost" && host != "" && !isLocalhostTarget(host) {
		destType = "external_dns"
	}
	switch destType {
	case "localhost":
		return "This call stayed inside the same pod, so Kubernetes Service mapping does not apply"
	case "external_dns":
		return "Target is outside the cluster: " + destination
	case "external_ip":
		return "Target is an external IP: " + destination
	default:
		return ""
	}
}

func originalStateFor(in Intent) string {
	switch in.InvestigationType {
	case TypeNetworkTimeout:
		return "Transport / upstream connectivity"
	case TypeDownstreamHTTPFailure:
		return "Application / downstream HTTP failure"
	case TypeClientError:
		return "Application-level HTTP 4xx"
	default:
		return "Unknown"
	}
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
