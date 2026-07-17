package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func syncNamespaceConfig(client *http.Client, url, clusterName string, namespaces []string, pods []ReportedPod) (map[string]bool, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"cluster":        clusterName,
		"agentNamespace": currentAgentNamespace(),
		"namespaces":     namespaces,
		"pods":           pods,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	var data struct {
		Enabled []string `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	res := make(map[string]bool)
	for _, ns := range data.Enabled {
		res[ns] = true
	}
	return res, nil
}
