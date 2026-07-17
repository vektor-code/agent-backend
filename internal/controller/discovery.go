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
}

func discoverClusterState(ctx context.Context, client *kubernetes.Clientset) (clusterState, error) {
	nsList, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterState{}, fmt.Errorf("list namespaces: %w", err)
	}

	appNamespaces := appNamespaceNames(nsList.Items)
	configMapData := discoverConfigMapData(ctx, client)
	frontendServices := discoverFrontendServices(ctx, client, appNamespaces)
	servicesByNamespace := discoverServices(ctx, client, appNamespaces)
	reportedPods := discoverReportedPods(ctx, client, frontendServices, servicesByNamespace, configMapData)

	return clusterState{
		appNamespaces: appNamespaces,
		reportedPods:  reportedPods,
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

func discoverFrontendServices(ctx context.Context, client *kubernetes.Clientset, appNamespaces []string) map[string]bool {
	frontendServices := make(map[string]bool)
	for _, ns := range appNamespaces {
		ingresses, err := client.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for _, ing := range ingresses.Items {
			if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
				frontendServices[ns+"/"+ing.Spec.DefaultBackend.Service.Name] = true
			}
			for _, rule := range ing.Spec.Rules {
				if rule.HTTP == nil {
					continue
				}
				for _, path := range rule.HTTP.Paths {
					if path.Path == "/" || path.Path == "" || path.Path == "/*" {
						if path.Backend.Service != nil {
							frontendServices[ns+"/"+path.Backend.Service.Name] = true
						}
					}
				}
			}
		}
	}
	return frontendServices
}

func discoverServices(ctx context.Context, client *kubernetes.Clientset, appNamespaces []string) map[string][]corev1.Service {
	servicesByNamespace := make(map[string][]corev1.Service)
	for _, ns := range appNamespaces {
		services, err := client.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
		if err == nil {
			servicesByNamespace[ns] = services.Items
		}
	}
	return servicesByNamespace
}

func discoverReportedPods(
	ctx context.Context,
	client *kubernetes.Clientset,
	frontendServices map[string]bool,
	servicesByNamespace map[string][]corev1.Service,
	configMapData map[string]map[string]string,
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
		reportedPods = append(reportedPods, reportedPodFromK8sPod(
			&pod,
			frontendServices,
			servicesByNamespace[pod.Namespace],
			configMapData,
		))
	}
	return reportedPods
}
