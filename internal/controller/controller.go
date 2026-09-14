package controller

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/kubetrace/agent-backend/internal/investigate"
	"github.com/kubetrace/agent-backend/internal/platformhealth"
)

func Run(ctx context.Context, centralURL string, client *http.Client, allowed func() bool) {
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

	var lastHealth *platformhealth.Report
	var lastHealthAt time.Time

	for {
		select {
		case <-ctx.Done():
			log.Println("[controller] stopping controller loop.")
			return
		case <-ticker.C:
			if allowed != nil && !allowed() {
				continue
			}
			state, err := discoverClusterState(ctx, clients.kube)
			if err != nil {
				log.Printf("[controller] error discovering cluster state: %v", err)
				continue
			}

			// Refresh APM pod diagnostics periodically so separate-cluster
			// installs can surface agent/operator logs in central Admin.
			if lastHealth == nil || time.Since(lastHealthAt) >= 60*time.Second {
				if report, herr := platformhealth.Collect(ctx, clients.kube, platformhealth.Options{
					Namespace: currentAgentNamespace(),
					TailLines: 200,
				}); herr != nil {
					log.Printf("[controller] platform health collect: %v", herr)
				} else {
					lastHealth = platformhealth.OnlyPresentComponents(platformhealth.TagCluster(report, clusterName))
					lastHealthAt = time.Now()
				}
			}

			crs := listReportedInstrumentations(ctx, clients.dynamic)
			enabledMap, workloads, jobs, err := syncNamespaceConfig(client, configURL, clusterName, state.appNamespaces, state.reportedPods, state.reportedNodes, crs, lastHealth)
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
			// Workload-level enable also needs a live Instrumentation CR
			// ({ns}-instrumentation) or the operator webhook fails with NotFound.
			for _, ns := range ensureInstrumentationForWorkloads(ctx, clients.dynamic, workloads) {
				restartNamespaces = append(restartNamespaces, ns)
			}
			// Apply per-service inject annotations first, then roll namespaces
			// whose Instrumentation CR was created/toggled so pods are admitted
			// with the webhook against a live CR.
			reconcileWorkloadInstrumentation(ctx, clients.kube, workloads)
			seenRestart := map[string]bool{}
			for _, ns := range restartNamespaces {
				if ns == "" || seenRestart[ns] {
					continue
				}
				seenRestart[ns] = true
				restartAnnotatedWorkloads(ctx, clients.kube, ns)
			}
			prevEnabled = enabledMap
		}
	}
}
