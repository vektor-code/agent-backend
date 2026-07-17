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

			enabledMap, err := syncNamespaceConfig(client, configURL, clusterName, state.appNamespaces, state.reportedPods)
			if err != nil {
				log.Printf("[controller] error syncing configurations: %v", err)
				continue
			}

			reconcileInstrumentations(ctx, clients.dynamic, state.appNamespaces, enabledMap)
		}
	}
}
