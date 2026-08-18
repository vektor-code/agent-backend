package controller

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/kubetrace/agent-backend/internal/investigate"
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

	runner := investigate.NewRunner(investigate.Config{
		CentralURL:     centralURL,
		HTTP:           client,
		Kube:           clients.kube,
		REST:           clients.config,
		ClusterName:    clusterName,
		AgentNamespace: currentAgentNamespace(),
		IsAppNamespace: isAppNamespace,
	})
	go runner.Poll(ctx)

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

			enabledMap, workloads, jobs, err := syncNamespaceConfig(client, configURL, clusterName, state.appNamespaces, state.reportedPods, state.reportedNodes)
			if err != nil {
				log.Printf("[controller] error syncing configurations: %v", err)
				continue
			}
			for _, raw := range jobs {
				runner.HandleRaw(ctx, raw)
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
