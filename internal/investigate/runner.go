package investigate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/kubetrace/agent-backend/internal/centralauth"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Config struct {
	CentralURL     string
	HTTP           *http.Client
	Kube           kubernetes.Interface
	REST           *rest.Config
	ClusterName    string
	AgentNamespace string
	IsAppNamespace func(string) bool
}

type Runner struct {
	cfg     Config
	exec    *Executor
	jobsURL string
	postURL string
}

func NewRunner(cfg Config) *Runner {
	cluster := newK8sCluster(cfg.Kube, cfg.REST)
	return &Runner{
		cfg:     cfg,
		exec:    NewExecutor(cluster, cfg.IsAppNamespace, cfg.AgentNamespace),
		jobsURL: deriveURL(cfg.CentralURL, "/v1/investigations/jobs"),
		postURL: deriveURL(cfg.CentralURL, "/v1/investigations/results"),
	}
}

func (r *Runner) Poll(ctx context.Context) {
	if r == nil {
		return
	}
	log.Println("[investigate] starting agent investigation poller")
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.pullAndRun(ctx)
		}
	}
}

func (r *Runner) Handle(ctx context.Context, intents []Intent) {
	for _, in := range intents {
		in := in
		go r.runOne(ctx, in)
	}
}

func (r *Runner) HandleRaw(ctx context.Context, raw []byte) {
	if err := RejectRemoteCommand(raw); err != nil {
		log.Printf("[investigate] rejected payload: %v", err)
		return
	}
	var in Intent
	if err := json.Unmarshal(raw, &in); err != nil {
		return
	}
	go r.runOne(ctx, in)
}

func (r *Runner) pullAndRun(ctx context.Context) {
	if r.jobsURL == "" || r.cfg.HTTP == nil {
		return
	}
	url := r.jobsURL + "?cluster=" + r.cfg.ClusterName + "&limit=5"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	centralauth.Apply(req)
	resp, err := r.cfg.HTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return
	}
	var jobs []json.RawMessage
	if err := json.Unmarshal(body, &jobs); err != nil {
		return
	}
	for _, raw := range jobs {
		if err := RejectRemoteCommand(raw); err != nil {
			log.Printf("[investigate] refused API intent: %v", err)
			continue
		}
		var in Intent
		if err := json.Unmarshal(raw, &in); err != nil {
			continue
		}
		go r.runOne(ctx, in)
	}
}

func (r *Runner) runOne(ctx context.Context, in Intent) {
	if in.ClusterID != "" && r.cfg.ClusterName != "" && in.ClusterID != r.cfg.ClusterName && in.ClusterID != "default" {
		return
	}
	if in.ExpiresAt.After(time.Time{}) && time.Now().After(in.ExpiresAt) {
		return
	}
	res := r.exec.Run(ctx, in)
	if err := r.postResult(ctx, res); err != nil {
		log.Printf("[investigate] post result: %v", err)
	}
}

func (r *Runner) postResult(ctx context.Context, res Result) error {
	if r.cfg.HTTP == nil || r.postURL == "" {
		return nil
	}
	payload, err := json.Marshal(res)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.postURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	centralauth.Apply(req)
	resp, err := r.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func deriveURL(central, path string) string {
	u := strings.Replace(central, "/v1/traces", path, 1)
	if u != central {
		return u
	}
	return strings.Replace(central, "/api/traces", path, 1)
}
