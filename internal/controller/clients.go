package controller

import (
	"fmt"
	"log"
	"os"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type controllerClients struct {
	kube    *kubernetes.Clientset
	dynamic dynamic.Interface
}

var instrumentationGVR = schema.GroupVersionResource{
	Group:    "opentelemetry.io",
	Version:  "v1alpha1",
	Resource: "instrumentations",
}

func newControllerClients() (*controllerClients, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Printf("[controller] in-cluster config not found, trying kubeconfig: %v", err)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath())
		if err != nil {
			return nil, fmt.Errorf("kubeconfig not found: %w", err)
		}
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	return &controllerClients{
		kube:    kubeClient,
		dynamic: dynamicClient,
	}, nil
}

func kubeconfigPath() string {
	if path := os.Getenv("KUBECONFIG"); path != "" {
		return path
	}
	return os.Getenv("HOME") + "/.kube/config"
}
