package investigate

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type fakeCluster struct {
	mu        sync.Mutex
	pods      map[string]*PodView
	byIP      map[string]*PodView
	svcByIP   map[string]*ServiceView
	endpoints map[string]*EndpointsView
	diagPod   *PodView
	execOut   *ExecResult
	execErr   error
	gets      atomic.Int32
	execs     atomic.Int32
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
func (f *fakeCluster) FindDeployment(context.Context, string, string) (*DeploymentView, error) {
	return nil, nil
}
func (f *fakeCluster) ListWarningEvents(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeCluster) ListNetworkPolicies(context.Context, string) ([]NetworkPolicyView, error) {
	return nil, nil
}
func (f *fakeCluster) FindDiagnosticPod(context.Context, string) (*PodView, error) {
	return f.diagPod, nil
}
func (f *fakeCluster) Exec(context.Context, string, string, string, []string) (*ExecResult, error) {
	f.execs.Add(1)
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
