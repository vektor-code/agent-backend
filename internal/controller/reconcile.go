package controller

import (
	"context"
	"log"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

func reconcileInstrumentations(
	ctx context.Context,
	dynamicClient dynamic.Interface,
	appNamespaces []string,
	enabledMap map[string]bool,
) {
	agentNs := currentAgentNamespace()

	for _, nsName := range appNamespaces {
		name := instrumentationName(nsName)
		enabled := enabledMap[nsName]

		if !enabled {
			deleteInstrumentation(ctx, dynamicClient, nsName, name)
			continue
		}
		applyInstrumentation(ctx, dynamicClient, nsName, name, agentNs)
	}
}

func deleteInstrumentation(ctx context.Context, dynamicClient dynamic.Interface, namespace, name string) {
	err := dynamicClient.Resource(instrumentationGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		log.Printf("[controller/error] failed to delete instrumentation in namespace %s: %v", namespace, err)
		return
	}
	if err == nil {
		log.Printf("[controller] deleted disabled instrumentation %s in namespace %s", name, namespace)
	}
}

func applyInstrumentation(ctx context.Context, dynamicClient dynamic.Interface, namespace, name, agentNamespace string) {
	inst := &unstructured.Unstructured{
		Object: buildInstrumentationObject(namespace, agentNamespace),
	}
	inst.SetName(name)

	existing, err := dynamicClient.Resource(instrumentationGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			createInstrumentation(ctx, dynamicClient, namespace, inst)
			return
		}
		log.Printf("[controller/error] failed to check instrumentation in namespace %s: %v", namespace, err)
		return
	}

	inst.SetResourceVersion(existing.GetResourceVersion())
	_, err = dynamicClient.Resource(instrumentationGVR).Namespace(namespace).Update(ctx, inst, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("[controller/error] failed to update instrumentation in namespace %s: %v", namespace, err)
	}
}

func createInstrumentation(ctx context.Context, dynamicClient dynamic.Interface, namespace string, inst *unstructured.Unstructured) {
	_, err := dynamicClient.Resource(instrumentationGVR).Namespace(namespace).Create(ctx, inst, metav1.CreateOptions{})
	if err != nil {
		log.Printf("[controller/error] failed to create instrumentation in namespace %s: %v", namespace, err)
		return
	}
	log.Printf("[controller] dynamically created instrumentation %s in namespace %s", inst.GetName(), namespace)
}
