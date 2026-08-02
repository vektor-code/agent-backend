package controller

import (
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
	Language            string            `json:"language"`
	Instrumented        bool              `json:"instrumented"`
	InstrumentationType string            `json:"instrumentationType"`
	Details             string            `json:"details"`
	DatabaseName        string            `json:"databaseName"`
	DatabaseHost        string            `json:"databaseHost"`
	DatabasePort        string            `json:"databasePort"`
	IsFrontend          bool              `json:"isFrontend"`
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
}

func reportedPodFromK8sPod(
	pod *corev1.Pod,
	frontendServices map[string]bool,
	services []corev1.Service,
	configMapData map[string]map[string]string,
	podMetrics metricsSnapshot,
) ReportedPod {
	isFrontend := isFrontendPod(pod, frontendServices, services)
	lang := detectLanguageFromPodSpec(pod, isFrontend)
	inst, instType, details := getPodInstrumentationStatus(pod)
	dbName, dbHost, dbPort := detectDatabaseInfo(pod, configMapData)
	cpuLimit, memoryLimit := resourceLimitsForPod(pod)
	usage, measured := podMetrics.usage[pod.Namespace+"/"+pod.Name]

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
		Language:            lang,
		Instrumented:        inst,
		InstrumentationType: instType,
		Details:             details,
		DatabaseName:        dbName,
		DatabaseHost:        dbHost,
		DatabasePort:        dbPort,
		IsFrontend:          isFrontend,
	}
}

func reportedNodeFromK8sNode(node *corev1.Node, nodeMetrics nodeMetricsSnapshot) ReportedNode {
	usage, measured := nodeMetrics.usage[node.Name]
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
	}
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
