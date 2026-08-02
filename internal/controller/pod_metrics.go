package controller

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// podUsage is the measured resource consumption of one pod.
type podUsage struct {
	CPUMilli float64
	MemoryMi float64
}

// metricsSnapshot holds one sweep of the metrics API, keyed by namespace/name.
type metricsSnapshot struct {
	usage     map[string]podUsage
	fetchedAt time.Time
	available bool
}

type nodeMetricsSnapshot struct {
	usage     map[string]podUsage
	fetchedAt time.Time
	available bool
}

var (
	metricsCacheMu sync.RWMutex
	metricsCache   metricsSnapshot

	nodeMetricsCacheMu sync.RWMutex
	nodeMetricsCache   nodeMetricsSnapshot
)

// metricsCacheTTL bounds how often the metrics API is swept. Metrics-server
// only refreshes every 15s or so, and the reconcile loop runs more often than
// that.
const metricsCacheTTL = 20 * time.Second

// fetchPodMetrics reads live usage from the metrics.k8s.io API.
//
// This replaces a previous implementation that derived CPU and memory from
// `time.Now().UnixNano() % 100` and presented the result in the dashboard as
// though it were measured. Fabricated numbers are worse than absent ones: an
// operator cannot tell they are looking at noise. When metrics-server is not
// installed, usage is now reported as unavailable instead.
func fetchPodMetrics(ctx context.Context, client *kubernetes.Clientset) metricsSnapshot {
	metricsCacheMu.RLock()
	cached := metricsCache
	metricsCacheMu.RUnlock()
	if !cached.fetchedAt.IsZero() && time.Since(cached.fetchedAt) < metricsCacheTTL {
		return cached
	}

	snapshot := metricsSnapshot{usage: map[string]podUsage{}, fetchedAt: time.Now()}

	raw, err := client.RESTClient().Get().
		AbsPath("/apis/metrics.k8s.io/v1beta1/pods").
		DoRaw(ctx)
	if err != nil {
		// metrics-server is optional; report unavailable rather than invent.
		log.Printf("[controller] pod metrics unavailable (is metrics-server installed?): %v", err)
		metricsCacheMu.Lock()
		metricsCache = snapshot
		metricsCacheMu.Unlock()
		return snapshot
	}

	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Containers []struct {
				Usage struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"usage"`
			} `json:"containers"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		log.Printf("[controller] could not parse pod metrics: %v", err)
		metricsCacheMu.Lock()
		metricsCache = snapshot
		metricsCacheMu.Unlock()
		return snapshot
	}

	for _, item := range list.Items {
		var usage podUsage
		// Sum across containers: a pod's usage is all of its containers, not
		// just the first one.
		for _, container := range item.Containers {
			usage.CPUMilli += parseCPUToMilli(container.Usage.CPU)
			usage.MemoryMi += parseMemoryToMi(container.Usage.Memory)
		}
		snapshot.usage[item.Metadata.Namespace+"/"+item.Metadata.Name] = usage
	}
	snapshot.available = true

	metricsCacheMu.Lock()
	metricsCache = snapshot
	metricsCacheMu.Unlock()
	return snapshot
}

func fetchNodeMetrics(ctx context.Context, client *kubernetes.Clientset) nodeMetricsSnapshot {
	nodeMetricsCacheMu.RLock()
	cached := nodeMetricsCache
	nodeMetricsCacheMu.RUnlock()
	if !cached.fetchedAt.IsZero() && time.Since(cached.fetchedAt) < metricsCacheTTL {
		return cached
	}

	snapshot := nodeMetricsSnapshot{usage: map[string]podUsage{}, fetchedAt: time.Now()}

	raw, err := client.RESTClient().Get().
		AbsPath("/apis/metrics.k8s.io/v1beta1/nodes").
		DoRaw(ctx)
	if err != nil {
		log.Printf("[controller] node metrics unavailable (is metrics-server installed?): %v", err)
		nodeMetricsCacheMu.Lock()
		nodeMetricsCache = snapshot
		nodeMetricsCacheMu.Unlock()
		return snapshot
	}

	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Usage struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"usage"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		log.Printf("[controller] could not parse node metrics: %v", err)
		nodeMetricsCacheMu.Lock()
		nodeMetricsCache = snapshot
		nodeMetricsCacheMu.Unlock()
		return snapshot
	}

	for _, item := range list.Items {
		snapshot.usage[item.Metadata.Name] = podUsage{
			CPUMilli: parseCPUToMilli(item.Usage.CPU),
			MemoryMi: parseMemoryToMi(item.Usage.Memory),
		}
	}
	snapshot.available = true

	nodeMetricsCacheMu.Lock()
	nodeMetricsCache = snapshot
	nodeMetricsCacheMu.Unlock()
	return snapshot
}

// parseCPUToMilli converts a Kubernetes CPU quantity to millicores.
// The metrics API reports CPU in nanocores ("123456789n") most of the time.
func parseCPUToMilli(value string) float64 {
	number, suffix := splitQuantity(value)
	switch suffix {
	case "n":
		return number / 1e6
	case "u":
		return number / 1e3
	case "m":
		return number
	case "":
		return number * 1000
	default:
		return 0
	}
}

// parseMemoryToMi converts a Kubernetes memory quantity to mebibytes.
func parseMemoryToMi(value string) float64 {
	number, suffix := splitQuantity(value)
	switch suffix {
	case "Ki":
		return number / 1024
	case "Mi":
		return number
	case "Gi":
		return number * 1024
	case "Ti":
		return number * 1024 * 1024
	case "K", "k":
		return number * 1000 / (1024 * 1024)
	case "M":
		return number * 1e6 / (1024 * 1024)
	case "G":
		return number * 1e9 / (1024 * 1024)
	case "":
		return number / (1024 * 1024)
	default:
		return 0
	}
}

// splitQuantity separates the numeric part of a Kubernetes quantity from its
// unit suffix.
func splitQuantity(value string) (float64, string) {
	idx := 0
	for idx < len(value) && (value[idx] == '.' || value[idx] == '-' || (value[idx] >= '0' && value[idx] <= '9')) {
		idx++
	}
	if idx == 0 {
		return 0, ""
	}
	var number float64
	if err := json.Unmarshal([]byte(value[:idx]), &number); err != nil {
		return 0, ""
	}
	return number, value[idx:]
}

// resourceLimitsForPod sums the configured limits across a pod's containers.
// A zero limit means "unlimited", which is reported as 0 rather than a
// fabricated default.
func resourceLimitsForPod(pod *corev1.Pod) (cpuLimit, memoryLimit float64) {
	for _, container := range pod.Spec.Containers {
		if limit, ok := container.Resources.Limits["cpu"]; ok {
			cpuLimit += float64(limit.MilliValue())
		}
		if limit, ok := container.Resources.Limits["memory"]; ok {
			memoryLimit += float64(limit.Value()) / (1024 * 1024)
		}
	}
	return cpuLimit, memoryLimit
}

// restartCount sums restarts across every container in the pod. The previous
// version read only ContainerStatuses[0], so a crash-looping sidecar was
// invisible.
func restartCount(pod *corev1.Pod) int {
	total := 0
	for _, status := range pod.Status.ContainerStatuses {
		total += int(status.RestartCount)
	}
	for _, status := range pod.Status.InitContainerStatuses {
		total += int(status.RestartCount)
	}
	return total
}
