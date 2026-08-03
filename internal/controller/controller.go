package controller

import (
	"context"
	"log"
	"net/http"
	"time"
)

func Run(ctx context.Context, centralURL string, client *http.Client) {
	log.Println("[controller] Starting background auto-instrumentation reconciliation worker...")

	clients, err := newControllerClients()
	if err != nil {
		log.Printf("[controller] Kubernetes controller disabled: %v", err)
		return
	}

	configURL := namespaceConfigURL(centralURL)
	clusterName := currentClusterName()
	prevEnabled := map[string]bool{}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[controller] stopping controller loop.")
			return
		case <-ticker.C:
			state, err := discoverClusterState(ctx, clients.kube)
			if err != nil {
				log.Printf("[controller] error discovering cluster state: %v", err)
				continue
			}

			enabledMap, workloads, err := syncNamespaceConfig(client, configURL, clusterName, state.appNamespaces, state.reportedPods, state.reportedNodes)
			if err != nil {
				log.Printf("[controller] error syncing configurations: %v", err)
				continue
			}

			restartNamespaces := reconcileInstrumentations(
				ctx,
				clients.dynamic,
				clients.kube,
				state.appNamespaces,
				enabledMap,
				prevEnabled,
			)
			// Apply per-service inject annotations first, then roll namespaces
			// whose Instrumentation CR was created/toggled so pods are admitted
			// with the webhook against a live CR.
			reconcileWorkloadInstrumentation(ctx, clients.kube, workloads)
			for _, ns := range restartNamespaces {
				restartAnnotatedWorkloads(ctx, clients.kube, ns)
			}
			prevEnabled = enabledMap
		}
	}
}
