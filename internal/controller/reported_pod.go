package controller

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

type ReportedPod struct {
	Name                string            `json:"name"`
	Namespace           string            `json:"namespace"`
	NodeName            string            `json:"nodeName"`
	Labels              map[string]string `json:"labels"`
	Phase               string            `json:"phase"`
	CpuUsage            float64           `json:"cpuUsage"`
	CpuLimit            float64           `json:"cpuLimit"`
	MemoryUsage         float64           `json:"memoryUsage"`
	MemoryLimit         float64           `json:"memoryLimit"`
	RestartCount        int               `json:"restartCount"`
	Ready               bool              `json:"ready"`
	// StatusReason / StatusMessage explain why Ready is false (ImagePullBackOff,
	// CrashLoopBackOff, …). Empty when the pod is Ready.
	StatusReason        string            `json:"statusReason,omitempty"`
	StatusMessage       string            `json:"statusMessage,omitempty"`
	Language            string            `json:"language"`
	Instrumented        bool              `json:"instrumented"`
	InstrumentationType string            `json:"instrumentationType"`
	Details             string            `json:"details"`
	DatabaseName        string            `json:"databaseName"`
	DatabaseHost        string            `json:"databaseHost"`
	DatabasePort        string            `json:"databasePort"`
	IsFrontend          bool              `json:"isFrontend"`
	// ContainerImages lists app container images (init containers omitted).
	ContainerImages []string `json:"containerImages,omitempty"`
	// MetricsAvailable is false when metrics-server did not report this pod.
	// The dashboard uses it to show "unavailable" rather than a zero that
	// looks like idleness.
	MetricsAvailable bool `json:"metricsAvailable"`
}

type ReportedNode struct {
	Name              string  `json:"name"`
	Role              string  `json:"role"`
	CpuCapacity       float64 `json:"cpuCapacity"`
	CpuAllocatable    float64 `json:"cpuAllocatable"`
	MemoryCapacity    float64 `json:"memoryCapacity"`
	MemoryAllocatable float64 `json:"memoryAllocatable"`
	PodCapacity       int     `json:"podCapacity"`
	PodAllocatable    int     `json:"podAllocatable"`
	CpuUsage          float64 `json:"cpuUsage"`
	MemoryUsage       float64 `json:"memoryUsage"`
	MetricsAvailable  bool    `json:"metricsAvailable"`
	CloudProvider     string  `json:"cloudProvider,omitempty"`
	Region            string  `json:"region,omitempty"`
	Zone              string  `json:"zone,omitempty"`
	InstanceType      string  `json:"instanceType,omitempty"`
	// Host facts from node.Status.NodeInfo — used to show Ubuntu/etc on on-prem
	// and OS/arch even when cloud labels are present.
	OperatingSystem  string `json:"operatingSystem,omitempty"`
	OsImage          string `json:"osImage,omitempty"`
	KernelVersion    string `json:"kernelVersion,omitempty"`
	Architecture     string `json:"architecture,omitempty"`
	ContainerRuntime string `json:"containerRuntime,omitempty"`
	KubeletVersion   string `json:"kubeletVersion,omitempty"`
}

func reportedPodFromK8sPod(
	pod *corev1.Pod,
	configMapData map[string]map[string]string,
	podMetrics metricsSnapshot,
	processCmdline string,
) ReportedPod {
	lang := detectLanguage(pod, processCmdline)
	if lang == "" {
		lang = languageFromProcessCache(pod)
	}
	isFrontend := isStaticHTTPStack(lang)
	inst, instType, details := getPodInstrumentationStatus(pod)
	dbName, dbHost, dbPort := detectDatabaseInfo(pod, configMapData)
	cpuLimit, memoryLimit := resourceLimitsForPod(pod)
	usage, measured := podMetrics.usage[pod.Namespace+"/"+pod.Name]
	statusReason, statusMessage := podStatusHint(pod)

	return ReportedPod{
		Name:                pod.Name,
		Namespace:           pod.Namespace,
		NodeName:            pod.Spec.NodeName,
		Labels:              pod.Labels,
		Phase:               string(pod.Status.Phase),
		CpuUsage:            usage.CPUMilli,
		CpuLimit:            cpuLimit,
		MemoryUsage:         usage.MemoryMi,
		MemoryLimit:         memoryLimit,
		MetricsAvailable:    measured && podMetrics.available,
		RestartCount:        restartCount(pod),
		Ready:               podReady(pod),
		StatusReason:        statusReason,
		StatusMessage:       statusMessage,
		Language:            lang,
		Instrumented:        inst,
		InstrumentationType: instType,
		Details:             details,
		DatabaseName:        dbName,
		DatabaseHost:        dbHost,
		DatabasePort:        dbPort,
		IsFrontend:          isFrontend,
		ContainerImages:     containerImagesFromPod(pod),
	}
}

func reportedNodeFromK8sNode(node *corev1.Node, nodeMetrics nodeMetricsSnapshot) ReportedNode {
	usage, measured := nodeMetrics.usage[node.Name]
	info := node.Status.NodeInfo
	return ReportedNode{
		Name:              node.Name,
		Role:              nodeRole(node.Labels),
		CpuCapacity:       float64(node.Status.Capacity.Cpu().MilliValue()),
		CpuAllocatable:    float64(node.Status.Allocatable.Cpu().MilliValue()),
		MemoryCapacity:    float64(node.Status.Capacity.Memory().Value()) / (1024 * 1024),
		MemoryAllocatable: float64(node.Status.Allocatable.Memory().Value()) / (1024 * 1024),
		PodCapacity:       int(node.Status.Capacity.Pods().Value()),
		PodAllocatable:    int(node.Status.Allocatable.Pods().Value()),
		CpuUsage:          usage.CPUMilli,
		MemoryUsage:       usage.MemoryMi,
		MetricsAvailable:  measured && nodeMetrics.available,
		CloudProvider:     inferCloudProvider(node.Labels),
		Region:            nodeRegion(node.Labels),
		Zone:              nodeZone(node.Labels),
		InstanceType:      nodeInstanceType(node.Labels),
		OperatingSystem:   strings.TrimSpace(info.OperatingSystem),
		OsImage:           normalizeOsImage(info.OSImage),
		KernelVersion:     strings.TrimSpace(info.KernelVersion),
		Architecture:      strings.TrimSpace(info.Architecture),
		ContainerRuntime:  strings.TrimSpace(info.ContainerRuntimeVersion),
		KubeletVersion:    strings.TrimSpace(info.KubeletVersion),
	}
}

// normalizeOsImage keeps a short human label (e.g. "Ubuntu 22.04.3 LTS") from
// kubelet OSImage strings that sometimes include long build suffixes.
func normalizeOsImage(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// Prefer a friendly distro+version when present in the string.
	lower := strings.ToLower(s)
	for _, name := range []string{"ubuntu", "debian", "centos", "rocky", "almalinux", "fedora", "rhel", "red hat", "suse", "amazon linux", "container-optimized os", "flatcar", "bottlerocket"} {
		if i := strings.Index(lower, name); i >= 0 {
			rest := strings.TrimSpace(s[i:])
			// Cap length so the UI popover stays readable.
			if len(rest) > 64 {
				rest = strings.TrimSpace(rest[:64])
			}
			return rest
		}
	}
	if len(s) > 64 {
		return strings.TrimSpace(s[:64])
	}
	return s
}

func containerImagesFromPod(pod *corev1.Pod) []string {
	if pod == nil || len(pod.Spec.Containers) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(pod.Spec.Containers))
	images := make([]string, 0, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		img := strings.TrimSpace(c.Image)
		if img == "" || seen[img] {
			continue
		}
		seen[img] = true
		images = append(images, img)
	}
	return images
}

func nodeZone(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	if z := labels["topology.kubernetes.io/zone"]; z != "" {
		return z
	}
	return labels["failure-domain.beta.kubernetes.io/zone"]
}

func nodeRegion(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	if r := labels["topology.kubernetes.io/region"]; r != "" {
		return r
	}
	return labels["failure-domain.beta.kubernetes.io/region"]
}

func nodeInstanceType(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	if t := labels["node.kubernetes.io/instance-type"]; t != "" {
		return t
	}
	return labels["beta.kubernetes.io/instance-type"]
}

// inferCloudProvider reads well-known node label prefixes; empty means on-prem / unknown.
func inferCloudProvider(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	for key := range labels {
		switch {
		case strings.HasPrefix(key, "kubernetes.azure.com/"), strings.HasPrefix(key, "azure.workload.identity/"):
			return "azure"
		case strings.HasPrefix(key, "eks.amazonaws.com/"), strings.HasPrefix(key, "topology.k8s.aws/"),
			strings.HasPrefix(key, "alpha.eksctl.io/"):
			return "aws"
		case strings.HasPrefix(key, "cloud.google.com/"), strings.HasPrefix(key, "topology.gke.io/"):
			return "gcp"
		case strings.HasPrefix(key, "node.openshift.io/"):
			return "ocp"
		case strings.HasPrefix(key, "ibm-cloud.kubernetes.io/"):
			return "ibm"
		case strings.HasPrefix(key, "oci.oraclecloud.com/"):
			return "oci"
		case strings.HasPrefix(key, "digitalocean.com/"):
			return "digitalocean"
		case strings.HasPrefix(key, "linode.com/"):
			return "linode"
		case strings.Contains(key, "vsphere"):
			return "vsphere"
		}
	}
	return ""
}

// languageFromProcessCache fills language from live process probes when the pod
// template and cmdline passed into reportedPodFromK8sPod did not resolve one.
func languageFromProcessCache(pod *corev1.Pod) string {
	for _, wKey := range workloadKeysFromPod(pod) {
		ns, name, ok := strings.Cut(wKey, "/")
		if !ok {
			continue
		}
		if lang := getProcessLangByWorkload(ns, name); lang != "" {
			return lang
		}
	}
	return ""
}

func nodeRole(labels map[string]string) string {
	if labels == nil {
		return "worker"
	}
	if _, ok := labels["node-role.kubernetes.io/control-plane"]; ok {
		return "master"
	}
	if _, ok := labels["node-role.kubernetes.io/master"]; ok {
		return "master"
	}
	return "worker"
}
