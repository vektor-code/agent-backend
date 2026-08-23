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

func discoverClusterState(ctx context.Context, client *kubernetes.Clientset) (clusterState, error) {
	nsList, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterState{}, fmt.Errorf("list namespaces: %w", err)
	}

	appNamespaces := appNamespaceNames(nsList.Items)
	configMapData := discoverConfigMapData(ctx, client)
	podMetrics := fetchPodMetrics(ctx, client)
	nodeMetrics := fetchNodeMetrics(ctx, client)
	reportedPods := discoverReportedPods(ctx, client, configMapData, podMetrics)
	reportedNodes := discoverReportedNodes(ctx, client, nodeMetrics)

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
	client *kubernetes.Clientset,
	configMapData map[string]map[string]string,
	podMetrics metricsSnapshot,
) []ReportedPod {
	podList, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("[controller] error listing pods: %v", err)
		return nil
	}

	reportedPods := make([]ReportedPod, 0, len(podList.Items))
	for _, pod := range podList.Items {
		if !isAppNamespace(pod.Namespace) {
			continue
		}
		reportedPods = append(reportedPods, reportedPodFromK8sPod(&pod, configMapData, podMetrics))
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
