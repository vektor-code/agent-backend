package investigate

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type PodView struct {
	Name       string
	Namespace  string
	IP         string
	Node       string
	Phase      string
	Ready      bool
	Restarts   int
	Workload   string
	Container  string
	Containers []string
	Deleting   bool
	Labels     map[string]string
}

type ServiceView struct {
	Name      string
	Namespace string
	ClusterIP string
	Ports     []int32
}

type EndpointsView struct {
	Service  string
	Ready    []string
	NotReady []string
	Source   string
}

type DeploymentView struct {
	Name    string
	Desired int32
	Ready   int32
}

type NetworkPolicyView struct {
	Name   string
	Select string
}

type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Cluster is the in-cluster Kubernetes surface. Tests fake this.
type Cluster interface {
	GetPod(ctx context.Context, namespace, name string) (*PodView, error)
	FindPodByIP(ctx context.Context, namespace, ip string) (*PodView, error)
	FindRunningPods(ctx context.Context, namespace, workload string) ([]*PodView, error)
	FindServiceByIP(ctx context.Context, namespace, ip string) (*ServiceView, error)
	GetEndpoints(ctx context.Context, namespace, service string) (*EndpointsView, error)
	FindDeployment(ctx context.Context, namespace, workload string) (*DeploymentView, error)
	ListWarningEvents(ctx context.Context, namespace, pod string) ([]string, error)
	ListNetworkPolicies(ctx context.Context, namespace string) ([]NetworkPolicyView, error)
	FindDiagnosticPod(ctx context.Context, namespace string) (*PodView, error)
	Exec(ctx context.Context, namespace, pod, container string, argv []string) (*ExecResult, error)
}

type k8sCluster struct {
	client kubernetes.Interface
	config *rest.Config
}

func newK8sCluster(client kubernetes.Interface, config *rest.Config) *k8sCluster {
	if client == nil {
		return nil
	}
	return &k8sCluster{client: client, config: config}
}

func (a *k8sCluster) GetPod(ctx context.Context, namespace, name string) (*PodView, error) {
	p, err := a.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return podView(p), nil
}

func (a *k8sCluster) FindPodByIP(ctx context.Context, namespace, ip string) (*PodView, error) {
	if ip == "" {
		return nil, nil
	}
	pods, err := a.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.PodIP == ip {
			return podView(p), nil
		}
	}
	return nil, nil
}

func (a *k8sCluster) FindRunningPods(ctx context.Context, namespace, workload string) ([]*PodView, error) {
	pods, err := a.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []*PodView
	wl := strings.ToLower(strings.TrimSpace(workload))
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil || p.Status.Phase != corev1.PodRunning {
			continue
		}
		view := podView(p)
		if isAgentWorkload(view.Workload) || isAgentWorkload(p.Name) {
			continue
		}
		if wl == "" || strings.EqualFold(view.Workload, workload) || strings.Contains(strings.ToLower(p.Name), wl) {
			out = append(out, view)
		}
	}
	return out, nil
}

func (a *k8sCluster) FindServiceByIP(ctx context.Context, namespace, ip string) (*ServiceView, error) {
	if ip == "" {
		return nil, nil
	}
	svcs, err := a.client.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range svcs.Items {
		s := &svcs.Items[i]
		if s.Spec.ClusterIP == ip {
			ports := make([]int32, 0, len(s.Spec.Ports))
			for _, p := range s.Spec.Ports {
				ports = append(ports, p.Port)
			}
			return &ServiceView{Name: s.Name, Namespace: s.Namespace, ClusterIP: s.Spec.ClusterIP, Ports: ports}, nil
		}
	}
	return nil, nil
}

func (a *k8sCluster) GetEndpoints(ctx context.Context, namespace, service string) (*EndpointsView, error) {
	if slices, err := a.client.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "kubernetes.io/service-name=" + service,
	}); err == nil && len(slices.Items) > 0 {
		view := &EndpointsView{Service: service, Source: "EndpointSlice"}
		for _, sl := range slices.Items {
			for _, ep := range sl.Endpoints {
				ready := ep.Conditions.Ready == nil || *ep.Conditions.Ready
				for _, addr := range ep.Addresses {
					if ready {
						view.Ready = append(view.Ready, addr)
					} else {
						view.NotReady = append(view.NotReady, addr)
					}
				}
			}
		}
		return view, nil
	}
	ep, err := a.client.CoreV1().Endpoints(namespace).Get(ctx, service, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	view := &EndpointsView{Service: service, Source: "Endpoints"}
	for _, sub := range ep.Subsets {
		for _, addr := range sub.Addresses {
			view.Ready = append(view.Ready, addr.IP)
		}
		for _, addr := range sub.NotReadyAddresses {
			view.NotReady = append(view.NotReady, addr.IP)
		}
	}
	return view, nil
}

func (a *k8sCluster) FindDeployment(ctx context.Context, namespace, workload string) (*DeploymentView, error) {
	if workload == "" {
		return nil, nil
	}
	d, err := a.client.AppsV1().Deployments(namespace).Get(ctx, workload, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	desired := int32(0)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	return &DeploymentView{Name: d.Name, Desired: desired, Ready: d.Status.ReadyReplicas}, nil
}

func (a *k8sCluster) ListWarningEvents(ctx context.Context, namespace, pod string) ([]string, error) {
	ev, err := a.client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + pod + ",type=Warning",
		Limit:         8,
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ev.Items))
	for _, item := range ev.Items {
		msg := strings.TrimSpace(item.Reason + ": " + item.Message)
		if msg != "" {
			out = append(out, msg)
		}
	}
	return out, nil
}

func (a *k8sCluster) ListNetworkPolicies(ctx context.Context, namespace string) ([]NetworkPolicyView, error) {
	list, err := a.client.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]NetworkPolicyView, 0, len(list.Items))
	for _, np := range list.Items {
		sel := "namespace"
		if len(np.Spec.PodSelector.MatchLabels) > 0 {
			sel = fmt.Sprintf("%v", np.Spec.PodSelector.MatchLabels)
		}
		out = append(out, NetworkPolicyView{Name: np.Name, Select: sel})
	}
	return out, nil
}

func (a *k8sCluster) FindDiagnosticPod(ctx context.Context, namespace string) (*PodView, error) {
	pods, err := a.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=crnet-diagnostics",
	})
	if err != nil {
		return nil, err
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp == nil && p.Status.Phase == corev1.PodRunning {
			return podView(p), nil
		}
	}
	return nil, nil
}

func (a *k8sCluster) Exec(ctx context.Context, namespace, pod, container string, argv []string) (*ExecResult, error) {
	if a.config == nil {
		return nil, fmt.Errorf("pod exec is not configured")
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	req := a.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   argv,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(a.config, http.MethodPost, req.URL())
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	streamCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	err = executor.StreamWithContext(streamCtx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	res := &ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		res.ExitCode = 1
		if strings.Contains(err.Error(), "exit code") {
			fmt.Sscanf(err.Error(), "%*s exit code %d", &res.ExitCode)
		}
		return res, err
	}
	return res, nil
}

func podView(p *corev1.Pod) *PodView {
	restarts := 0
	ready := false
	containers := make([]string, 0, len(p.Spec.Containers))
	appContainer := ""
	for _, c := range p.Spec.Containers {
		containers = append(containers, c.Name)
		if appContainer == "" && !isSidecarName(c.Name) {
			appContainer = c.Name
		}
	}
	if appContainer == "" && len(containers) > 0 {
		appContainer = containers[0]
	}
	for _, st := range p.Status.ContainerStatuses {
		restarts += int(st.RestartCount)
		if st.Name == appContainer {
			ready = st.Ready
		}
	}
	if !ready {
		for _, st := range p.Status.ContainerStatuses {
			if st.Ready && !isSidecarName(st.Name) {
				ready = true
				break
			}
		}
	}
	return &PodView{
		Name:       p.Name,
		Namespace:  p.Namespace,
		IP:         p.Status.PodIP,
		Node:       p.Spec.NodeName,
		Phase:      string(p.Status.Phase),
		Ready:      ready && p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil,
		Restarts:   restarts,
		Workload:   getPodApp(p),
		Container:  appContainer,
		Containers: containers,
		Deleting:   p.DeletionTimestamp != nil,
		Labels:     p.Labels,
	}
}

func getPodApp(p *corev1.Pod) string {
	if app, ok := p.Labels["app.kubernetes.io/name"]; ok {
		return app
	}
	if app, ok := p.Labels["app"]; ok {
		return app
	}
	if app, ok := p.Labels["k8s-app"]; ok {
		return app
	}
	parts := strings.Split(p.Name, "-")
	if len(parts) > 2 {
		return strings.Join(parts[:len(parts)-2], "-")
	}
	return parts[0]
}

func isSidecarName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "opentelemetry") ||
		strings.Contains(n, "istio-proxy") ||
		strings.Contains(n, "instrumentation") ||
		n == "dummy-backend"
}
