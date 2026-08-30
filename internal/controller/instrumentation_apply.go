package controller

import (
	"context"
	"log"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

func instrumentationSpecRejected(err error) bool {
	if err == nil {
		return false
	}
	if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown field") ||
		strings.Contains(msg, "strict decoding") ||
		strings.Contains(msg, "failed to unmarshal")
}

func upsertInstrumentation(ctx context.Context, dyn dynamic.Interface, namespace, name string, obj map[string]interface{}) error {
	inst := &unstructured.Unstructured{Object: obj}
	inst.SetName(name)
	existing, err := dyn.Resource(instrumentationGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = dyn.Resource(instrumentationGVR).Namespace(namespace).Create(ctx, inst, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	inst.SetResourceVersion(existing.GetResourceVersion())
	_, err = dyn.Resource(instrumentationGVR).Namespace(namespace).Update(ctx, inst, metav1.UpdateOptions{})
	return err
}

func applyInstrumentationSpec(ctx context.Context, dyn dynamic.Interface, namespace, agentNamespace string) error {
	name := instrumentationName(namespace)
	full := buildInstrumentationObject(namespace, agentNamespace)
	err := upsertInstrumentation(ctx, dyn, namespace, name, full)
	if err != nil && instrumentationSpecRejected(err) {
		log.Printf("[controller] instrumentation extensions rejected in %s (%v); retrying without java extensions", namespace, err)
		err = upsertInstrumentation(ctx, dyn, namespace, name, stripJavaExtensions(buildInstrumentationObject(namespace, agentNamespace)))
	}
	if err != nil && instrumentationSpecRejected(err) {
		log.Printf("[controller] full instrumentation spec rejected in %s (%v); retrying compatible spec", namespace, err)
		err = upsertInstrumentation(ctx, dyn, namespace, name, buildCompatibleInstrumentationObject(namespace, agentNamespace))
	}
	return err
}
