package controller

import (
	"context"
	"log"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ReportedWorkload is a Deployment/StatefulSet/DaemonSet the agent observed on
// its own cluster. Central Admin previously derived replica/ready counts by
// aggregating reported pods, which undercounts whenever pods are missing from
// the report (evicted, pending, or filtered out). Reporting the controller's
// own Status.ReadyReplicas keeps the Admin "Ready" column truthful for
// agent-managed clusters where central has no kubeconfig to list workloads.
type ReportedWorkload struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	Kind         string `json:"kind"`
	Replicas     int32  `json:"replicas"`
	Ready        int32  `json:"ready"`
	Language     string `json:"language"`
	Instrumented bool   `json:"instrumented"`
}

// discoverReportedWorkloads lists the instrumentable workload kinds in each app
// namespace. Mirrors api-backend's k8s.ListWorkloadsInNamespace so both paths
// produce the same shape; a namespace that fails to list is skipped rather than
// failing the whole sync.
func discoverReportedWorkloads(ctx context.Context, kube kubernetes.Interface, appNamespaces []string) []ReportedWorkload {
	workloads := make([]ReportedWorkload, 0, len(appNamespaces))

	for _, ns := range appNamespaces {
		deployments, err := kube.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			log.Printf("[controller] error listing deployments in %s: %v", ns, err)
		} else {
			for _, d := range deployments.Items {
				workloads = append(workloads, reportedWorkloadFromDeployment(&d, ns))
			}
		}

		statefulSets, err := kube.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			log.Printf("[controller] error listing statefulsets in %s: %v", ns, err)
		} else {
			for _, s := range statefulSets.Items {
				workloads = append(workloads, reportedWorkloadFromStatefulSet(&s, ns))
			}
		}

		daemonSets, err := kube.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			log.Printf("[controller] error listing daemonsets in %s: %v", ns, err)
		} else {
			for _, d := range daemonSets.Items {
				workloads = append(workloads, reportedWorkloadFromDaemonSet(&d, ns))
			}
		}
	}

	return workloads
}

func reportedWorkloadFromDeployment(d *appsv1.Deployment, namespace string) ReportedWorkload {
	return ReportedWorkload{
		Name:      d.Name,
		Namespace: namespace,
		Kind:      "Deployment",
		Replicas:  specReplicas(d.Spec.Replicas),
		Ready:     d.Status.ReadyReplicas,
	}
}

func reportedWorkloadFromStatefulSet(s *appsv1.StatefulSet, namespace string) ReportedWorkload {
	return ReportedWorkload{
		Name:      s.Name,
		Namespace: namespace,
		Kind:      "StatefulSet",
		Replicas:  specReplicas(s.Spec.Replicas),
		Ready:     s.Status.ReadyReplicas,
	}
}

// DaemonSets have no spec.replicas; desired scheduling is the equivalent
// "how many should exist" number.
func reportedWorkloadFromDaemonSet(d *appsv1.DaemonSet, namespace string) ReportedWorkload {
	return ReportedWorkload{
		Name:      d.Name,
		Namespace: namespace,
		Kind:      "DaemonSet",
		Replicas:  d.Status.DesiredNumberScheduled,
		Ready:     d.Status.NumberReady,
	}
}

// enrichWorkloadLanguages copies language and instrumentation state from the
// pods the agent already probed (process cache included) onto their owning
// workload, so central does not have to re-detect from a pod template it
// cannot see.
func enrichWorkloadLanguages(workloads []ReportedWorkload, pods []ReportedPod) []ReportedWorkload {
	if len(workloads) == 0 || len(pods) == 0 {
		return workloads
	}
	for i := range workloads {
		w := &workloads[i]
		for j := range pods {
			p := &pods[j]
			if p.Namespace != w.Namespace || !podBelongsToWorkload(p, w.Name) {
				continue
			}
			if w.Language == "" && p.Language != "" {
				w.Language = p.Language
			}
			if p.Instrumented {
				w.Instrumented = true
			}
		}
	}
	return workloads
}

func podBelongsToWorkload(pod *ReportedPod, workloadName string) bool {
	if workloadName == "" || pod.Name == "" {
		return false
	}
	return pod.Name == workloadName || strings.HasPrefix(pod.Name, workloadName+"-")
}

func specReplicas(replicas *int32) int32 {
	if replicas == nil {
		// An unset spec.replicas means the Kubernetes default of 1.
		return 1
	}
	return *replicas
}
