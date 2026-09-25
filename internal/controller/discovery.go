package controller

import (
	"context"
	"fmt"
	"log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type clusterState struct {
	appNamespaces []string
	reportedPods  []ReportedPod
	reportedNodes []ReportedNode
}

func discoverClusterState(ctx context.Context, clients *controllerClients) (clusterState, error) {
	nsList, err := clients.kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterState{}, fmt.Errorf("list namespaces: %w", err)
	}

	appNamespaces := appNamespaceNames(nsList.Items)
	configMapData := discoverConfigMapData(ctx, clients.kube)
	podMetrics := fetchPodMetrics(ctx, clients.kube)
	nodeMetrics := fetchNodeMetrics(ctx, clients.kube)
	reportedPods := discoverReportedPods(ctx, clients, configMapData, podMetrics)
	reportedNodes := discoverReportedNodes(ctx, clients.kube, nodeMetrics)

	return clusterState{
		appNamespaces: appNamespaces,
		reportedPods:  reportedPods,
		reportedNodes: reportedNodes,
	}, nil
}

func appNamespaceNames(namespaces []corev1.Namespace) []string {
	appNamespaces := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		if isAppNamespace(ns.Name) {
			appNamespaces = append(appNamespaces, ns.Name)
		}
	}
	return appNamespaces
}

func discoverConfigMapData(ctx context.Context, client *kubernetes.Clientset) map[string]map[string]string {
	configMapData := make(map[string]map[string]string)
	cmList, err := client.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return configMapData
	}
	for _, cm := range cmList.Items {
		configMapData[cm.Namespace+"/"+cm.Name] = cm.Data
	}
	return configMapData
}

func discoverReportedPods(
	ctx context.Context,
	clients *controllerClients,
	configMapData map[string]map[string]string,
	podMetrics metricsSnapshot,
) []ReportedPod {
	podList, err := clients.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("[controller] error listing pods: %v", err)
		return nil
	}

	appPods := make([]corev1.Pod, 0, len(podList.Items))
	for _, pod := range podList.Items {
		if isAppNamespace(pod.Namespace) {
			appPods = append(appPods, pod)
		}
	}
	cmdlines := probeProcessCmdlines(ctx, clients, appPods)

	reportedPods := make([]ReportedPod, 0, len(appPods))
	for i := range appPods {
		pod := &appPods[i]
		cmdline := cmdlines[pod.Namespace+"/"+pod.Name]
		reportedPods = append(reportedPods, reportedPodFromK8sPod(pod, configMapData, podMetrics, cmdline))
	}
	return reportedPods
}

func discoverReportedNodes(ctx context.Context, client *kubernetes.Clientset, nodeMetrics nodeMetricsSnapshot) []ReportedNode {
	nodeList, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("[controller] error listing nodes: %v", err)
		return nil
	}

	reportedNodes := make([]ReportedNode, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		reportedNodes = append(reportedNodes, reportedNodeFromK8sNode(&node, nodeMetrics))
	}
	return reportedNodes
}
