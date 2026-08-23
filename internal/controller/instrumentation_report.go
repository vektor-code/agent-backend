package controller

import (
	"context"
	"log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// ReportedInstrumentation is the CR snapshot the agent posts so the API can
// list Auto-Instrumentation rules without a kube token on the workload cluster.
type ReportedInstrumentation struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Endpoint  string `json:"endpoint"`
	Sampler   string `json:"sampler"`
}

func listReportedInstrumentations(ctx context.Context, dyn dynamic.Interface) []ReportedInstrumentation {
	if dyn == nil {
		return nil
	}
	list, err := dyn.Resource(instrumentationGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("[controller] listing instrumentations: %v", err)
		return nil
	}
	out := make([]ReportedInstrumentation, 0, len(list.Items))
	for _, item := range list.Items {
		ns := item.GetNamespace()
		if !isAppNamespace(ns) {
			continue
		}
		out = append(out, reportedInstrumentationFromUnstructured(item))
	}
	return out
}

func reportedInstrumentationFromUnstructured(item unstructured.Unstructured) ReportedInstrumentation {
	spec, found, _ := unstructured.NestedMap(item.Object, "spec")
	var endpoint, sampler string
	if found {
		if exporter, ok, _ := unstructured.NestedMap(spec, "exporter"); ok {
			endpoint, _, _ = unstructured.NestedString(exporter, "endpoint")
		}
		if samplerMap, ok, _ := unstructured.NestedMap(spec, "sampler"); ok {
			sampler, _, _ = unstructured.NestedString(samplerMap, "type")
		}
	}
	return ReportedInstrumentation{
		Name:      item.GetName(),
		Namespace: item.GetNamespace(),
		Endpoint:  endpoint,
		Sampler:   sampler,
	}
}
