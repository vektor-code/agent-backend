package controller

import (
	"time"

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
}

func reportedPodFromK8sPod(
	pod *corev1.Pod,
	frontendServices map[string]bool,
	services []corev1.Service,
	configMapData map[string]map[string]string,
) ReportedPod {
	isFrontend := isFrontendPod(pod, frontendServices, services)
	lang := detectLanguageFromPodSpec(pod, isFrontend)
	inst, instType, details := getPodInstrumentationStatus(pod)
	dbName, dbHost, dbPort := detectDatabaseInfo(pod, configMapData)
	cpuUsage, cpuLimit, memoryUsage, memoryLimit := resourceUsageForPod(pod)

	return ReportedPod{
		Name:                pod.Name,
		Namespace:           pod.Namespace,
		NodeName:            pod.Spec.NodeName,
		Labels:              pod.Labels,
		Phase:               string(pod.Status.Phase),
		CpuUsage:            cpuUsage,
		CpuLimit:            cpuLimit,
		MemoryUsage:         memoryUsage,
		MemoryLimit:         memoryLimit,
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

func resourceUsageForPod(pod *corev1.Pod) (cpuUsage, cpuLimit, memoryUsage, memoryLimit float64) {
	cpuLimit = 1000.0
	memoryLimit = 1024.0
	for _, container := range pod.Spec.Containers {
		if limit, ok := container.Resources.Limits["cpu"]; ok {
			cpuLimit = float64(limit.MilliValue())
		}
		if limit, ok := container.Resources.Limits["memory"]; ok {
			memoryLimit = float64(limit.Value()) / (1024 * 1024)
		}
	}

	seed := float64(time.Now().UnixNano() % 100)
	cpuUsage = 20.0 + (seed * 0.1)
	memoryUsage = 150.0 + (seed * 0.2)
	if cpuUsage > cpuLimit {
		cpuUsage = cpuLimit * 0.5
	}
	if memoryUsage > memoryLimit {
		memoryUsage = memoryLimit * 0.5
	}
	return cpuUsage, cpuLimit, memoryUsage, memoryLimit
}

func restartCount(pod *corev1.Pod) int {
	if len(pod.Status.ContainerStatuses) == 0 {
		return 0
	}
	return int(pod.Status.ContainerStatuses[0].RestartCount)
}
