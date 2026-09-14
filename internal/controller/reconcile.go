package controller

import (
	"context"
	"log"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// reconcileInstrumentations creates/deletes per-namespace Instrumentation CRs.
// It returns namespaces that should be rolled after workload annotations are
// applied — typically when instrumentation was just created or the namespace
// was toggled enabled/disabled — so pods pick up (or drop) injection.
func reconcileInstrumentations(
	ctx context.Context,
	dynamicClient dynamic.Interface,
	kube kubernetes.Interface,
	appNamespaces []string,
	enabledMap map[string]bool,
	prevEnabled map[string]bool,
) []string {
	agentNs := currentAgentNamespace()
	var restartNamespaces []string
	seenRestart := map[string]bool{}

	markRestart := func(ns string) {
		if ns == "" || seenRestart[ns] {
			return
		}
		seenRestart[ns] = true
		restartNamespaces = append(restartNamespaces, ns)
	}

	for _, nsName := range appNamespaces {
		name := instrumentationName(nsName)
		enabled := enabledMap[nsName]
		wasEnabled, seen := prevEnabled[nsName]

		if !enabled {
			deleted := deleteInstrumentation(ctx, dynamicClient, nsName, name)
			stripped := stripInjectAnnotationsInNamespace(ctx, kube, nsName)
			if deleted || stripped || (seen && wasEnabled) {
				markRestart(nsName)
			}
			continue
		}

		created := applyInstrumentation(ctx, dynamicClient, nsName, name, agentNs)
		// New CR, or namespace just flipped to enabled → roll annotated apps
		// so the webhook injects against the live Instrumentation.
		if created || (seen && !wasEnabled) {
			markRestart(nsName)
		}
	}
	return restartNamespaces
}

func deleteInstrumentation(ctx context.Context, dynamicClient dynamic.Interface, namespace, name string) bool {
	err := dynamicClient.Resource(instrumentationGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		log.Printf("[controller/error] failed to delete instrumentation in namespace %s: %v", namespace, err)
		return false
	}
	if err == nil {
		log.Printf("[controller] deleted disabled instrumentation %s in namespace %s", name, namespace)
		return true
	}
	return false
}

// applyInstrumentation returns true when a new Instrumentation CR was created.
func applyInstrumentation(ctx context.Context, dynamicClient dynamic.Interface, namespace, name, agentNamespace string) bool {
	_, err := dynamicClient.Resource(instrumentationGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	created := apierrors.IsNotFound(err)
	if err != nil && !created {
		log.Printf("[controller/error] failed to check instrumentation in namespace %s: %v", namespace, err)
		return false
	}

	if err := applyInstrumentationSpec(ctx, dynamicClient, namespace, agentNamespace); err != nil {
		log.Printf("[controller/error] failed to apply instrumentation in namespace %s: %v", namespace, err)
		return false
	}
	if created {
		log.Printf("[controller] dynamically created instrumentation %s in namespace %s", name, namespace)
	}
	return created
}

// ensureInstrumentationForWorkloads creates missing Instrumentation CRs for any
// namespace that has an enabled per-workload config (even if the namespace-level
// toggle is off). Returns namespaces where a CR was newly created.
func ensureInstrumentationForWorkloads(ctx context.Context, dynamicClient dynamic.Interface, workloads []workloadConfig) []string {
	agentNs := currentAgentNamespace()
	needed := map[string]bool{}
	for _, w := range workloads {
		if w.Enabled && w.Namespace != "" {
			needed[w.Namespace] = true
		}
	}
	var created []string
	for ns := range needed {
		name := instrumentationName(ns)
		if applyInstrumentation(ctx, dynamicClient, ns, name, agentNs) {
			created = append(created, ns)
		}
	}
	return created
}
