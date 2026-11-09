package pool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"
)

// StartHealth verifies statically attached llama.cpp engines independently of
// occupancy. Dynamic agents provide the equivalent authenticated properties.
func (p *Pool) StartHealth() {
	ctx, cancel := context.WithCancel(context.Background())
	p.healthCancel = cancel
	for _, n := range p.nodes {
		if n.Config.EngineProtocol != "llama.cpp" || n.dynamic {
			continue
		}
		transport := n.Client.Transport.(*http.Transport).Clone()
		transport.MaxConnsPerHost = 2
		transport.MaxIdleConnsPerHost = 2
		n.probeClient = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		p.healthWG.Add(1)
		go func(n *Node) {
			defer p.healthWG.Done()
			tick := time.NewTicker(500 * time.Millisecond)
			defer tick.Stop()
			for {
				p.probe(ctx, n)
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		}(n)
	}
}
func (p *Pool) probe(ctx context.Context, n *Node) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	get := func(path string, out any) (int, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", n.Origin()+path, nil)
		if n.Config.KeyEnv != "" {
			req.Header.Set("Authorization", "Bearer "+os.Getenv(n.Config.KeyEnv))
		}
		resp, err := n.probeClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
		return resp.StatusCode, err
	}
	state := "unavailable"
	ready := false
	var health struct {
		Status string `json:"status"`
	}
	status, err := get("/health", &health)
	if err == nil && status == 503 && health.Status != "no slot available" {
		state = "loading"
	}
	if err == nil && (status == 200 || status == 503 && health.Status == "no slot available") {
		var props struct {
			Slots      int    `json:"total_slots"`
			Template   string `json:"chat_template"`
			Generation struct {
				Context int `json:"n_ctx"`
			} `json:"default_generation_settings"`
			Modalities struct {
				Vision bool `json:"vision"`
			} `json:"modalities"`
		}
		code, err := get("/props", &props)
		if err == nil && code == 200 && props.Slots == n.Config.Capacity && props.Generation.Context == n.Config.ContextSize() && props.Modalities.Vision == n.Config.Vision {
			hash := sha256.Sum256([]byte(props.Template))
			if n.Config.TemplateSHA256 == "" || n.Config.TemplateSHA256 == hex.EncodeToString(hash[:]) {
				var models struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				code, err = get("/v1/models", &models)
				if err == nil && code == 200 {
					for _, model := range models.Data {
						if model.ID == n.Config.UpstreamModel {
							ready = true
							state = "ready"
							break
						}
					}
				}
			}
		}
	}
	p.mu.Lock()
	n.ready = ready
	n.lifecycle = state
	n.leaseUntil = time.Now().Add(time.Second)
	if ready {
		n.failedUntil = time.Time{}
	}
	p.notifyLocked()
	p.mu.Unlock()
}
