package investigate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type fakeCluster struct {
	mu            sync.Mutex
	pods          map[string]*PodView
	byIP          map[string]*PodView
	svcByIP       map[string]*ServiceView
	endpoints     map[string]*EndpointsView
	deps          map[string]*DeploymentView
	events        map[string][]string
	diagPod       *PodView
	nodes         map[string]*NodeView
	endpointsByIP map[string]*EndpointOwner
	execOut       *ExecResult
	execErr       error
	execFn        func(namespace, pod, container string, argv []string) (*ExecResult, error)
	gets          atomic.Int32
	execs         atomic.Int32
}

func (f *fakeCluster) GetPod(_ context.Context, ns, name string) (*PodView, error) {
	f.gets.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pods[ns+"/"+name], nil
}
func (f *fakeCluster) FindPodByIP(_ context.Context, _, ip string) (*PodView, error) {
	f.gets.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byIP[ip], nil
}
func (f *fakeCluster) FindRunningPods(_ context.Context, ns, workload string) ([]*PodView, error) {
	f.gets.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*PodView
	for _, p := range f.pods {
		if p.Namespace == ns && (workload == "" || p.Workload == workload) && p.Phase == "Running" {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakeCluster) FindServiceByIP(_ context.Context, _, ip string) (*ServiceView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.svcByIP[ip], nil
}
func (f *fakeCluster) GetEndpoints(_ context.Context, ns, service string) (*EndpointsView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.endpoints[ns+"/"+service], nil
}
func (f *fakeCluster) FindDeployment(_ context.Context, ns, workload string) (*DeploymentView, error) {
	if f.deps == nil {
		return nil, nil
	}
	return f.deps[ns+"/"+workload], nil
}
func (f *fakeCluster) ListWarningEvents(_ context.Context, ns, pod string) ([]string, error) {
	if f.events == nil {
		return nil, nil
	}
	return f.events[ns+"/"+pod], nil
}
func (f *fakeCluster) ListNetworkPolicies(context.Context, string) ([]NetworkPolicyView, error) {
	return nil, nil
}
func (f *fakeCluster) FindNodeByIP(_ context.Context, ip string) (*NodeView, error) {
	if f.nodes == nil {
		return nil, nil
	}
	return f.nodes[ip], nil
}
func (f *fakeCluster) FindEndpointOwnerByIP(_ context.Context, ip string) (*EndpointOwner, error) {
	if f.endpointsByIP == nil {
		return nil, nil
	}
	return f.endpointsByIP[ip], nil
}
func (f *fakeCluster) FindDiagnosticPod(context.Context, string) (*PodView, error) {
	return f.diagPod, nil
}
func (f *fakeCluster) Exec(_ context.Context, ns, name, container string, argv []string) (*ExecResult, error) {
	f.execs.Add(1)
	if f.execFn != nil {
		return f.execFn(ns, name, container, argv)
	}
	return f.execOut, f.execErr
}

func healthzIntent() Intent {
	return Intent{
		InvestigationType: TypeDownstreamHTTPFailure,
		ClusterID:         "crtnet-ext-k8s",
		Namespace:         "highping-dev",
		SourceWorkload:    "gtm-preview",
		SourcePod:         "gtm-preview-abc",
		Destination:       "10.233.115.249:8080",
		DestinationURL:    "http://10.233.115.249:8080/healthz",
		DestinationType:   "private_ip",
		RecordedHTTP:      503,
		Checks:            []string{CheckPodStatus, CheckServiceResolution, CheckEndpointHealth, CheckEvents, CheckNetworkPolicy, CheckHTTPRequest},
		MaxLevel:          3,
		TraceID:           "t-503",
		Fingerprint:       "fp-503",
	}
}

func TestRejectRemoteCommand(t *testing.T) {
	if err := RejectRemoteCommand([]byte(`{"investigationType":"downstream_http_failure","command":"kubectl exec -it foo"}`)); err == nil {
		t.Fatal("command field must be refused")
	}
	if err := RejectRemoteCommand([]byte(`{"investigationType":"downstream_http_failure","checks":["pod_status"]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestAdmitRejectsUnknownCheckAndKubeSystem(t *testing.T) {
	in := healthzIntent()
	in.Checks = append(in.Checks, "rm_rf")
	in.Namespace = "kube-system"
	if _, err := admit(in, func(ns string) bool { return ns != "kube-system" }, "crnet-apm"); err == nil {
		t.Fatal("kube-system must be refused")
	}
	in.Namespace = "highping-dev"
	admitted, err := admit(in, func(string) bool { return true }, "crnet-apm")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range admitted.Checks {
		if c == "rm_rf" {
			t.Fatal("unknown checks must be dropped")
		}
	}
}

func TestAdmitClientErrorDropsHTTPRequest(t *testing.T) {
	in := healthzIntent()
	in.InvestigationType = TypeClientError
	in.MaxLevel = 1
	admitted, err := admit(in, func(string) bool { return true }, "crnet-apm")
	if err != nil {
		t.Fatal(err)
	}
	if admitted.MaxLevel != 1 {
		t.Fatalf("maxLevel=%d", admitted.MaxLevel)
	}
	if wants(admitted.Checks, CheckHTTPRequest) {
		t.Fatal("http_request must not run for client_error")
	}
}

func TestExecutorConfirms503FromExistingPod(t *testing.T) {
	pod := &PodView{Name: "gtm-preview-abc", Namespace: "highping-dev", IP: "10.233.115.249", Phase: "Running", Ready: true, Workload: "gtm-preview", Container: "gtm-preview"}
	fake := &fakeCluster{
		pods:      map[string]*PodView{"highping-dev/gtm-preview-abc": pod},
		byIP:      map[string]*PodView{"10.233.115.249": pod},
		svcByIP:   map[string]*ServiceView{"10.233.115.249": {Name: "gtm-preview", Namespace: "highping-dev", ClusterIP: "10.233.115.249"}},
		endpoints: map[string]*EndpointsView{"highping-dev/gtm-preview": {Service: "gtm-preview", Ready: []string{"10.233.115.249"}}},
		execOut:   &ExecResult{Stdout: "HTTP/1.1 503 Service Unavailable\n"},
	}
	ex := NewExecutor(fake, func(string) bool { return true }, "crnet-apm")
	res := ex.Run(context.Background(), healthzIntent())
	if res.Status != StatusComplete && res.Status != StatusPartial {
		t.Fatalf("status=%s %+v", res.Status, res)
	}
	if fake.execs.Load() != 1 {
		t.Fatalf("expected 1 exec, got %d", fake.execs.Load())
	}
	if res.Inference == "" || res.Confidence != "HIGH" {
		t.Fatalf("expected HIGH inference, got %q %s", res.Inference, res.Confidence)
	}
	var sawObserved, sawInference bool
	for _, o := range res.Observations {
		if o.Kind == KindObserved && o.Code == "http_request" && o.OK != nil && *o.OK {
			sawObserved = true
		}
		if o.Kind == KindInference {
			sawInference = true
		}
	}
	if !sawObserved || !sawInference {
		t.Fatalf("must distinguish observed vs inference: %+v", res.Observations)
	}
}

func TestExecutorCachesFingerprint(t *testing.T) {
	pod := &PodView{Name: "gtm-preview-abc", Namespace: "highping-dev", IP: "10.233.115.249", Phase: "Running", Ready: true, Workload: "gtm-preview", Container: "gtm-preview"}
	fake := &fakeCluster{
		pods:    map[string]*PodView{"highping-dev/gtm-preview-abc": pod},
		byIP:    map[string]*PodView{"10.233.115.249": pod},
		execOut: &ExecResult{Stdout: "HTTP/1.1 503\n"},
	}
	ex := NewExecutor(fake, func(string) bool { return true }, "crnet-apm")
	first := ex.Run(context.Background(), healthzIntent())
	second := healthzIntent()
	second.TraceID = "t-other"
	got := ex.Run(context.Background(), second)
	if fake.execs.Load() != 1 {
		t.Fatalf("cache should reuse exec, got %d", fake.execs.Load())
	}
	if got.Fingerprint != first.Fingerprint {
		t.Fatalf("fingerprints %q %q", first.Fingerprint, got.Fingerprint)
	}
}

func TestExecutorUsesDiagnosticWorkerWhenNoSourcePod(t *testing.T) {
	worker := &PodView{Name: "crnet-diag-0", Namespace: "highping-dev", Phase: "Running", Ready: true, Workload: "crnet-diagnostics", Container: "diag"}
	dest := &PodView{Name: "gtm-preview-abc", Namespace: "highping-dev", IP: "10.233.115.249", Phase: "Running", Ready: true, Workload: "gtm-preview", Container: "gtm-preview"}
	fake := &fakeCluster{
		byIP:    map[string]*PodView{"10.233.115.249": dest},
		diagPod: worker,
		execOut: &ExecResult{Stdout: "HTTP/1.1 503\n"},
	}
	ex := NewExecutor(fake, func(string) bool { return true }, "crnet-apm")
	in := healthzIntent()
	in.SourcePod = ""
	res := ex.Run(context.Background(), in)
	found := false
	for _, o := range res.Observations {
		if o.Code == "http_request" && o.Level == 3 && strings.Contains(o.Message, "diagnostic worker") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected level-3 reusable worker, got %+v", res.Observations)
	}
}

func TestExecutorExternalTargetSkipsServiceChecksAndMarksNotReproduced(t *testing.T) {
	pod := &PodView{Name: "reverse-proxy-abc", Namespace: "highping-client", Phase: "Running", Ready: true, Workload: "reverse-proxy", Container: "proxy"}
	fake := &fakeCluster{
		pods:    map[string]*PodView{"highping-client/reverse-proxy-abc": pod},
		execOut: &ExecResult{Stdout: "HTTP/1.1 400 Bad Request\n"},
	}
	ex := NewExecutor(fake, func(string) bool { return true }, "crnet-apm")
	in := Intent{
		InvestigationType: TypeNetworkTimeout,
		ClusterID:         "crtnet-ext-k8s",
		Namespace:         "highping-client",
		SourceWorkload:    "reverse-proxy",
		SourcePod:         "reverse-proxy-abc",
		Destination:       "sgtm.superead.com:56388",
		DestinationURL:    "http://sgtm.superead.com:56388/g/collect",
		DestinationType:   "external_dns",
		RecordedHTTP:      0,
		Checks:            []string{CheckPodStatus, CheckServiceResolution, CheckEndpointHealth, CheckNetworkPolicy, CheckHTTPRequest},
		MaxLevel:          3,
		TraceID:           "t-ext",
		Fingerprint:       "fp-ext",
	}
	res := ex.Run(context.Background(), in)
	if res.CurrentState != "Not reproduced" {
		t.Fatalf("current=%q", res.CurrentState)
	}
	if res.OriginalState != "Transport / upstream connectivity" {
		t.Fatalf("original=%q", res.OriginalState)
	}
	foundExternal := false
	for _, o := range res.Observations {
		if o.Code == "destination_context" && strings.Contains(o.Message, "outside the cluster") {
			foundExternal = true
		}
		if o.Code == "service_resolution" || o.Code == "endpoint_health" || o.Code == "network_policy" {
			t.Fatalf("external destination should skip kubernetes service checks: %+v", res.Observations)
		}
	}
	if !foundExternal {
		t.Fatalf("missing external target observation: %+v", res.Observations)
	}
}

func TestExecutorExternalTargetTimeoutStillFailing(t *testing.T) {
	pod := &PodView{Name: "ilstsdujaxvwapq-7b9655647-j8rng", Namespace: "highping-client", Phase: "Running", Ready: true, Workload: "ilstsdujaxvwapq", Container: "app"}
	fake := &fakeCluster{
		pods:    map[string]*PodView{"highping-client/ilstsdujaxvwapq-7b9655647-j8rng": pod},
		execOut: &ExecResult{Stderr: "Connecting to sgtm.biopet.az:52766 (104.21.12.16:52766)\nwget: download timed out\n"},
	}
	ex := NewExecutor(fake, func(string) bool { return true }, "crnet-apm")
	in := Intent{
		InvestigationType: TypeNetworkTimeout,
		ClusterID:         "crtnet-ext-k8s",
		Namespace:         "highping-client",
		SourceWorkload:    "ilstsdujaxvwapq",
		SourcePod:         "ilstsdujaxvwapq-7b9655647-j8rng",
		Destination:       "sgtm.biopet.az:52766",
		DestinationURL:    "http://sgtm.biopet.az:52766/g/collect",
		DestinationType:   "external_dns",
		RecordedHTTP:      0,
		Checks:            []string{CheckPodStatus, CheckHTTPRequest},
		MaxLevel:          3,
		TraceID:           "t-ext-timeout",
		Fingerprint:       "fp-ext-timeout",
	}
	res := ex.Run(context.Background(), in)
	if res.CurrentState != "Still failing" {
		t.Fatalf("current=%q inference=%q", res.CurrentState, res.Inference)
	}
	if res.OriginalState != "Transport / upstream connectivity" {
		t.Fatalf("original=%q", res.OriginalState)
	}
}

func TestHttpProbeRejectsInjection(t *testing.T) {
	if _, err := httpProbeArgv("javascript:alert(1)"); err == nil {
		t.Fatal("non-http schemes must be rejected")
	}
	argv, err := httpProbeArgv("http://10.233.115.249:8080/healthz")
	if err != nil {
		t.Fatal(err)
	}
	script := argv[2]
	if !strings.Contains(script, `"http://10.233.115.249:8080/healthz"`) {
		t.Fatalf("URL must be shell-quoted, got %s", script)
	}
	if strings.Contains(script, "apk upgrade") || strings.Contains(script, "apt-get update") {
		t.Fatal("probe must not upgrade packages")
	}
	cmds, err := probeCommands("https://www.googletagmanager.com/sgtm/a")
	if err != nil {
		t.Fatal(err)
	}
	foundNode := false
	foundPHP := false
	foundRuby := false
	for _, cmd := range cmds {
		joined := strings.Join(cmd, " ")
		if strings.Contains(joined, "nodejs/bin/node") {
			foundNode = true
			if strings.Contains(cmd[2], "googletagmanager") {
				t.Fatal("node probe script must not interpolate the URL")
			}
			if cmd[len(cmd)-1] != "https://www.googletagmanager.com/sgtm/a" {
				t.Fatalf("node probe must pass URL as argv, got %v", cmd)
			}
		}
		if len(cmd) > 0 && cmd[0] == "php" {
			foundPHP = true
			if cmd[len(cmd)-1] != "https://www.googletagmanager.com/sgtm/a" {
				t.Fatalf("php probe must pass URL as argv, got %v", cmd)
			}
		}
		if len(cmd) > 0 && cmd[0] == "ruby" {
			foundRuby = true
			if cmd[len(cmd)-1] != "https://www.googletagmanager.com/sgtm/a" {
				t.Fatalf("ruby probe must pass URL as argv, got %v", cmd)
			}
		}
	}
	if !foundNode {
		t.Fatal("expected a node argv fallback for distroless images")
	}
	if !foundPHP || !foundRuby {
		t.Fatalf("expected php and ruby argv fallbacks, php=%v ruby=%v", foundPHP, foundRuby)
	}
}

func TestProbeRejectsMetadataTarget(t *testing.T) {
	if _, err := httpProbeArgv("http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Fatal("link-local metadata must be refused")
	}
	if _, err := tcpProbeCommands("169.254.169.254", "80"); err == nil {
		t.Fatal("TCP metadata target must be refused")
	}
	if _, err := httpProbeArgv("http://metadata.google.internal/"); err == nil {
		t.Fatal("metadata hostname must be refused")
	}
}

func TestExecutorNoShellUsesNode(t *testing.T) {
	src := &PodView{Name: "gtm-server-a", Namespace: "highping-dev", Phase: "Running", Ready: true, Workload: "gtm-server", Container: "gtm"}
	fake := &fakeCluster{
		pods: map[string]*PodView{"highping-dev/gtm-server-a": src},
		execFn: func(_, name, _ string, argv []string) (*ExecResult, error) {
			if len(argv) == 0 {
				return nil, fmt.Errorf("empty argv")
			}
			switch argv[0] {
			case "/nodejs/bin/node", "node":
				return &ExecResult{Stdout: "HTTP_STATUS 404\n"}, nil
			default:
				return nil, fmt.Errorf(`exec: %q: stat %s: no such file or directory`, argv[0], argv[0])
			}
		},
	}
	ex := NewExecutor(fake, func(string) bool { return true }, "crnet-apm")
	in := Intent{
		InvestigationType: TypeNetworkTimeout,
		Namespace:         "highping-dev",
		SourceWorkload:    "gtm-server",
		SourcePod:         "gtm-server-a",
		Destination:       "www.googletagmanager.com:443",
		DestinationURL:    "https://www.googletagmanager.com/",
		DestinationType:   "external_dns",
		Checks:            []string{CheckPodStatus, CheckHTTPRequest},
		MaxLevel:          3,
		Fingerprint:       "fp-node-probe",
	}
	res := ex.Run(context.Background(), in)
	if res.CurrentState != "Not reproduced" {
		t.Fatalf("current=%q inference=%q obs=%+v", res.CurrentState, res.Inference, res.Observations)
	}
	found := false
	for _, o := range res.Observations {
		if o.Code == "http_request" && strings.Contains(o.Message, "HTTP 404") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected node fallback probe, got %+v", res.Observations)
	}
}

func TestExecutorExternalSourceNotReadyNoShell(t *testing.T) {
	pod := &PodView{
		Name: "gtm-server-df95b5b4b-wqflv", Namespace: "highping-dev", Node: "crtnet-ext-k8s-w05",
		Phase: "Running", Ready: false, Workload: "gtm-server", Container: "gtm-server",
	}
	fake := &fakeCluster{
		pods: map[string]*PodView{"highping-dev/gtm-server-df95b5b4b-wqflv": pod},
		deps: map[string]*DeploymentView{"highping-dev/gtm-server": {Name: "gtm-server", Desired: 1, Ready: 0}},
		events: map[string][]string{
			"highping-dev/gtm-server-df95b5b4b-wqflv": {
				`Unhealthy: Readiness probe failed: Get "http://10.233.115.194:80/healthz": dial tcp 10.233.115.194:80: connect: connection refused`,
			},
		},
		execErr: fmt.Errorf(`exec failed: unable to start container process: exec: "/bin/sh": stat /bin/sh: no such file or directory: unknown`),
	}
	ex := NewExecutor(fake, func(string) bool { return true }, "crnet-apm")
	in := Intent{
		InvestigationType: TypeNetworkTimeout,
		ClusterID:         "crtnet-ext-k8s",
		Namespace:         "highping-dev",
		SourceWorkload:    "gtm-server",
		SourcePod:         "gtm-server-df95b5b4b-wqflv",
		Destination:       "www.googletagmanager.com:443",
		DestinationURL:    "https://www.googletagmanager.com/",
		DestinationType:   "external_dns",
		RecordedHTTP:      0,
		Checks:            []string{CheckPodStatus, CheckServiceResolution, CheckEndpointHealth, CheckEvents, CheckNetworkPolicy, CheckHTTPRequest},
		MaxLevel:          3,
		TraceID:           "t-gtm",
		Fingerprint:       "fp-gtm",
	}
	res := ex.Run(context.Background(), in)
	if res.CurrentState != "Live retry not possible" {
		t.Fatalf("current=%q inference=%q", res.CurrentState, res.Inference)
	}
	if strings.Contains(strings.ToLower(res.Inference), "target pod is not ready") {
		t.Fatalf("must not treat source pod as the external target: %q", res.Inference)
	}
	if !strings.Contains(res.Inference, "no usable HTTP client") && !strings.Contains(res.Inference, "no /bin/sh") {
		t.Fatalf("inference should mention missing probe client: %q", res.Inference)
	}
	sawSource, sawDestPod, sawHTTPSkip, sawExternal := false, false, false, false
	for _, o := range res.Observations {
		if o.Code == "source_pod_status" && strings.Contains(o.Message, "Ready=false") {
			sawSource = true
		}
		if o.Code == "pod_status" && strings.Contains(o.Message, "Pod "+pod.Name) {
			sawDestPod = true
		}
		if o.Code == "http_request" && (strings.Contains(o.Message, "no /bin/sh") || strings.Contains(o.Message, "no usable HTTP client")) {
			sawHTTPSkip = true
		}
		if o.Code == "destination_context" && strings.Contains(o.Message, "www.googletagmanager.com:443") {
			sawExternal = true
		}
		if o.Code == "service_resolution" || o.Code == "endpoint_health" || o.Code == "network_policy" {
			t.Fatalf("external destination should skip kubernetes service checks: %+v", res.Observations)
		}
	}
	if !sawSource || sawDestPod || !sawHTTPSkip || !sawExternal {
		t.Fatalf("source=%v destPod=%v skip=%v external=%v obs=%+v", sawSource, sawDestPod, sawHTTPSkip, sawExternal, res.Observations)
	}
}

func TestExecutorNoShellFallsBackToDiagnosticWorker(t *testing.T) {
	src := &PodView{Name: "gtm-server-a", Namespace: "highping-dev", Phase: "Running", Ready: true, Workload: "gtm-server", Container: "gtm"}
	worker := &PodView{Name: "crnet-diag-0", Namespace: "highping-dev", Phase: "Running", Ready: true, Workload: "crnet-diagnostics", Container: "diag"}
	fake := &fakeCluster{
		pods:    map[string]*PodView{"highping-dev/gtm-server-a": src},
		diagPod: worker,
		execFn: func(_, name, _ string, _ []string) (*ExecResult, error) {
			if name == "gtm-server-a" {
				return nil, fmt.Errorf(`exec: "/bin/sh": stat /bin/sh: no such file or directory`)
			}
			return &ExecResult{Stdout: "HTTP/1.1 200 OK\n"}, nil
		},
	}
	ex := NewExecutor(fake, func(string) bool { return true }, "crnet-apm")
	in := Intent{
		InvestigationType: TypeNetworkTimeout,
		Namespace:         "highping-dev",
		SourceWorkload:    "gtm-server",
		SourcePod:         "gtm-server-a",
		Destination:       "www.googletagmanager.com:443",
		DestinationURL:    "https://www.googletagmanager.com/",
		DestinationType:   "external_dns",
		Checks:            []string{CheckPodStatus, CheckHTTPRequest},
		MaxLevel:          3,
		Fingerprint:       "fp-noshell-worker",
	}
	res := ex.Run(context.Background(), in)
	if fake.execs.Load() < 2 {
		t.Fatalf("expected source exec then worker exec, got %d", fake.execs.Load())
	}
	if res.CurrentState != "Not reproduced" {
		t.Fatalf("current=%q inference=%q", res.CurrentState, res.Inference)
	}
	found := false
	for _, o := range res.Observations {
		if o.Code == "http_request" && strings.Contains(o.Message, "diagnostic worker") && strings.Contains(o.Message, "HTTP 200") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected worker probe after missing shell, got %+v", res.Observations)
	}
}

func TestLimiterBlocksSecondDestination(t *testing.T) {
	l := newLimiter(Limits{Global: 10, Namespace: 3, Workload: 1, Destination: 1})
	release, ok := l.acquire("ns", "wl", "10.1.1.1:8080")
	if !ok {
		t.Fatal("first acquire")
	}
	if _, ok := l.acquire("ns", "wl", "10.1.1.1:8080"); ok {
		t.Fatal("same destination must be limited to 1")
	}
	release()
	if _, ok := l.acquire("ns", "wl", "10.1.1.1:8080"); !ok {
		t.Fatal("after release should allow")
	}
}

func postgresIntent() Intent {
	return Intent{
		InvestigationType:   TypeNetworkTimeout,
		ClusterID:           "crtnet-ext-k8s",
		Namespace:           "troni-prod",
		SourceWorkload:      "marketdatagw-backend",
		SourcePod:           "marketdatagw-backend-abc",
		Destination:         "172.16.45.31:5432",
		DestinationURL:      "http://172.16.45.31:5432/",
		DestinationType:     "private_ip",
		DestinationProtocol: "postgresql",
		Checks:              []string{CheckPodStatus, CheckServiceResolution, CheckEndpointHealth, CheckNetworkPolicy, CheckHTTPRequest},
		MaxLevel:            3,
		TraceID:             "t-pg",
		Fingerprint:         "fp-pg",
	}
}

func TestProtocolOfPostgresPortIsTCP(t *testing.T) {
	p := protocolOf(Intent{Destination: "172.16.45.31:5432", DestinationURL: "http://172.16.45.31:5432/"})
	if p.Mode != ProbeTCP || p.ID != "postgresql" {
		t.Fatalf("protocol=%+v", p)
	}
	if _, err := tcpProbeCommands("172.16.45.31", "5432"); err != nil {
		t.Fatal(err)
	}
	if _, err := tcpProbeCommands("172.16.45.31; rm -rf /", "5432"); err == nil {
		t.Fatal("must reject injected host")
	}
}

func TestExecutorPostgresUnmappedUsesTCPNotHTTP(t *testing.T) {
	src := &PodView{Name: "marketdatagw-backend-abc", Namespace: "troni-prod", Phase: "Running", Ready: true, Workload: "marketdatagw-backend", Container: "app"}
	fake := &fakeCluster{
		pods:    map[string]*PodView{"troni-prod/marketdatagw-backend-abc": src},
		execOut: &ExecResult{Stdout: "TCP_OPEN\n"},
		execFn: func(_ string, _ string, _ string, argv []string) (*ExecResult, error) {
			joined := strings.Join(argv, " ")
			if strings.Contains(joined, "wget") || strings.Contains(joined, "curl") || strings.Contains(joined, "HTTP_STATUS") {
				t.Fatalf("must not HTTP-probe postgres: %v", argv)
			}
			return &ExecResult{Stdout: "TCP_OPEN\n"}, nil
		},
	}
	ex := NewExecutor(fake, func(string) bool { return true }, "crnet-apm")
	res := ex.Run(context.Background(), postgresIntent())
	if res.CurrentState != "L4 reachable (not a cluster object)" {
		t.Fatalf("current=%q inference=%q obs=%+v", res.CurrentState, res.Inference, res.Observations)
	}
	if res.Confidence != "MEDIUM" {
		t.Fatalf("confidence=%s", res.Confidence)
	}
	var sawUnmapped, sawTCP, sawHTTP bool
	for _, o := range res.Observations {
		if o.Code == "dest_unmapped" && strings.Contains(o.Message, "PostgreSQL") {
			sawUnmapped = true
		}
		if o.Code == "tcp_connect" && strings.Contains(o.Message, "TCP open") {
			sawTCP = true
		}
		if o.Code == "http_request" {
			sawHTTP = true
		}
	}
	if !sawUnmapped || !sawTCP || sawHTTP {
		t.Fatalf("unmapped=%v tcp=%v http=%v obs=%+v", sawUnmapped, sawTCP, sawHTTP, res.Observations)
	}
}

func TestExecutorMapsEndpointOwnerInsteadOfUnmapped(t *testing.T) {
	src := &PodView{Name: "marketdatagw-backend-abc", Namespace: "troni-prod", Phase: "Running", Ready: true, Workload: "marketdatagw-backend", Container: "app"}
	fake := &fakeCluster{
		pods: map[string]*PodView{"troni-prod/marketdatagw-backend-abc": src},
		endpointsByIP: map[string]*EndpointOwner{
			"172.16.45.31": {Namespace: "data", Service: "iam-db", Source: "EndpointSlice"},
		},
		execOut: &ExecResult{Stdout: "TCP_OPEN\n"},
	}
	ex := NewExecutor(fake, func(string) bool { return true }, "crnet-apm")
	res := ex.Run(context.Background(), postgresIntent())
	if res.CurrentState != "L4 reachable" {
		t.Fatalf("current=%q obs=%+v", res.CurrentState, res.Observations)
	}
	found := false
	for _, o := range res.Observations {
		if o.Code == "dest_identity" && strings.Contains(o.Message, "data/iam-db") {
			found = true
		}
		if o.Code == "dest_unmapped" {
			t.Fatalf("should not be unmapped: %+v", res.Observations)
		}
	}
	if !found {
		t.Fatalf("missing endpoint identity: %+v", res.Observations)
	}
}

func TestNetworkPolicyPresentIsNotClaimedAllow(t *testing.T) {
	obs := concludeNetworkPolicyCheck([]NetworkPolicyView{{Name: "default-deny", IsolatesAll: true}})
	if obs.OK == nil || *obs.OK {
		t.Fatalf("namespace-wide policy must not be marked allowed: %+v", obs)
	}
	if !strings.Contains(obs.Message, "not simulated") && !strings.Contains(obs.Message, "may be restricted") {
		t.Fatalf("message=%s", obs.Message)
	}
}

func concludeNetworkPolicyCheck(nps []NetworkPolicyView) Observation {
	names := make([]string, 0, len(nps))
	isolates := false
	for _, np := range nps {
		names = append(names, np.Name)
		if np.IsolatesAll {
			isolates = true
		}
	}
	msg := strings.Join(names, ", ")
	if isolates {
		return observed("network_policy", "namespace-wide selector: "+msg+". Egress may be restricted; path was not simulated.", 1, false, "")
	}
	return observedInfo("network_policy", msg+". Path was not simulated.", 1, "")
}
