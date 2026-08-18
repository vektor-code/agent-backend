package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// workloadConfig is a per-service instrumentation override the operator set in
// the dashboard: which workload, and the explicitly chosen language stack.
type workloadConfig struct {
	Namespace    string `json:"namespace"`
	WorkloadName string `json:"workloadName"`
	WorkloadKind string `json:"workloadKind"`
	Enabled      bool   `json:"enabled"`
	Language     string `json:"language"`
}

func syncNamespaceConfig(client *http.Client, url, clusterName string, namespaces []string, pods []ReportedPod, nodes []ReportedNode) (map[string]bool, []workloadConfig, []json.RawMessage, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"cluster":        clusterName,
		"agentNamespace": currentAgentNamespace(),
		"namespaces":     namespaces,
		"pods":           pods,
		"nodes":          nodes,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	req, err := http.NewRequest("POST", url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, nil, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	var data struct {
		Enabled         []string          `json:"enabled"`
		Workloads       []workloadConfig  `json:"workloads"`
		Investigations  []json.RawMessage `json:"investigations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, nil, nil, err
	}

	res := make(map[string]bool)
	for _, ns := range data.Enabled {
		res[ns] = true
	}
	return res, data.Workloads, data.Investigations, nil
}
