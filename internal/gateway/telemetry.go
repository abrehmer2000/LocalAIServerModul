package gateway

import (
	"context"
	"fmt"
	"localaiserver/internal/event"
	"localaiserver/internal/pool"
	"net/http"
	"sync"
	"time"
)

type telemetry struct {
	sync.Mutex
	Counts             map[string]uint64
	Durations          [8]uint64
	Prompt, Completion int64
}

var durationBounds = [7]int64{10, 50, 100, 500, 1000, 10000, 60000}

func (t *telemetry) record(e event.Event) {
	t.Lock()
	defer t.Unlock()
	if t.Counts == nil {
		t.Counts = map[string]uint64{}
	}
	t.Counts[fmt.Sprintf("%d:%s", e.Status, e.Outcome)]++
	bucket := 7
	for i, b := range durationBounds {
		if e.DurationMS <= b {
			bucket = i
			break
		}
	}
	t.Durations[bucket]++
	if e.PromptTokens != nil {
		t.Prompt += *e.PromptTokens
	}
	if e.CompletionTokens != nil {
		t.Completion += *e.CompletionTokens
	}
}
func (g *Gateway) metricHandler(w http.ResponseWriter, r *http.Request) {
	g.metrics.Lock()
	counts := map[string]uint64{}
	for k, v := range g.metrics.Counts {
		counts[k] = v
	}
	hist := g.metrics.Durations
	prompt, completion := g.metrics.Prompt, g.metrics.Completion
	g.metrics.Unlock()
	profiles := []pool.Capabilities{}
	for _, model := range g.pool.Models() {
		profiles = append(profiles, g.pool.Capabilities(model))
	}
	writeJSON(w, 200, map[string]any{"models": profiles, "requests": counts, "duration_bucket_upper_ms": durationBounds, "duration_bucket_counts": hist, "prompt_tokens": prompt, "completion_tokens": completion, "nodes": g.pool.Snapshot(), "queue_length": g.pool.QueueLength("")})
}
func (g *Gateway) health(w http.ResponseWriter, r *http.Request) {
	profiles := []any{}
	ready := false
	for _, m := range g.pool.Models() {
		c := g.pool.Capabilities(m)
		profiles = append(profiles, c)
		ready = ready || c.State == "ready"
	}
	writeJSON(w, 200, map[string]any{"alive": true, "ready": ready, "draining": g.pool.IsDraining(), "models": profiles, "updated_at": time.Now().UTC()})
}
func (g *Gateway) capabilities(w http.ResponseWriter, r *http.Request) {
	e, ok := g.identify(w, r)
	if !ok {
		return
	}
	model := r.PathValue("model")
	if !g.pool.Has(model) {
		g.reject(w, e, 404, "model_not_found", "Unknown model")
		return
	}
	if !g.allowed(e.KeyID, model) {
		g.reject(w, e, 403, "forbidden", "Model access denied")
		return
	}
	e.Type = "capabilities.viewed"
	e.Model = model
	e.Status = 200
	if g.append(e) != nil {
		apiError(w, 503, "model_unavailable", "Audit unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"capabilities": g.pool.Capabilities(model), "import_leases": true, "generation_timeout_seconds": g.cfg.TimeoutSeconds, "queue_wait_seconds": int(g.cfg.QueueWait() / time.Second), "queue_capacity": g.cfg.QueueSize(), "max_output_tokens": g.pool.Capabilities(model).ContextPerSlot - 1, "max_explicit_output_tokens": min(g.cfg.MaxOutputTokens, g.pool.Capabilities(model).ContextPerSlot-1), "implicit_output_limit": false})
}
func (g *Gateway) BeginDrain() { g.pool.Drain() }

func (g *Gateway) WaitIdle(ctx context.Context) error { return g.pool.WaitIdle(ctx) }
