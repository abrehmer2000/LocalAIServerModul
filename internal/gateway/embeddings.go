package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"
	"unicode/utf8"
)

// Embedding aliases are explicit. A chat model is never silently repurposed.
func (g *Gateway) embeddings(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	e, ok := g.identify(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, g.cfg.MaxBodyBytes))
	if err != nil {
		g.reject(w, e, 413, "invalid_request", "Request exceeds limit")
		return
	}
	var in struct {
		Model    string          `json:"model"`
		Input    json.RawMessage `json:"input"`
		Encoding string          `json:"encoding_format,omitempty"`
		User     string          `json:"user,omitempty"`
	}
	if !utf8.Valid(raw) || !validUnicodeEscapes(raw) || strictJSON(raw, &in) != nil || in.Encoding != "" && in.Encoding != "float" {
		g.reject(w, e, 400, "invalid_request", "Invalid embedding request")
		return
	}
	var one string
	var many []string
	if json.Unmarshal(in.Input, &one) != nil {
		if json.Unmarshal(in.Input, &many) != nil || len(many) == 0 || len(many) > 128 {
			g.reject(w, e, 400, "invalid_request", "Input must be text or 1-128 texts")
			return
		}
	} else {
		many = []string{one}
	}
	for _, s := range many {
		if s == "" {
			g.reject(w, e, 400, "invalid_request", "Input must not be empty")
			return
		}
	}
	e.Model = in.Model
	if !g.pool.Has(in.Model) {
		g.reject(w, e, 404, "model_not_found", "Unknown model")
		return
	}
	if !g.allowed(e.KeyID, in.Model) {
		g.reject(w, e, 403, "forbidden", "Model access denied")
		return
	}
	probe := g.pool.ProbeNode(in.Model)
	if probe == nil || probe.Config.Kind != "embedding" {
		g.reject(w, e, 400, "invalid_request", "Alias is not an embedding model")
		return
	}
	n, release, err := g.pool.WaitAcquire(r.Context(), in.Model, false, g.cfg.QueueSize(), g.cfg.QueueWait())
	if err != nil {
		if r.Context().Err() == nil {
			g.reject(w, e, 503, err.Error(), "Embedding capacity unavailable")
		}
		return
	}
	failed := false
	defer func() { release(failed); e.DurationMS = time.Since(start).Milliseconds(); _ = g.append(e) }()
	e.NodeID = n.Config.ID
	e.Revision = n.Config.Revision
	e.Type = "inference.accepted"
	if g.append(e) != nil {
		apiError(w, 503, "model_unavailable", "Audit unavailable")
		return
	}
	e.Type = "inference.failed"
	e.Status = 502
	e.Outcome = "upstream_failure"
	in.Model = n.Config.UpstreamModel
	in.User = ""
	body, _ := json.Marshal(in)
	ctx, cancel := context.WithTimeout(r.Context(), g.cfg.Timeout())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", n.Origin()+"/v1/embeddings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if n.Config.KeyEnv != "" {
		req.Header.Set("Authorization", "Bearer "+os.Getenv(n.Config.KeyEnv))
	}
	resp, err := n.Client.Do(req)
	if err != nil {
		if r.Context().Err() != nil {
			e.Status = 0
			e.Outcome = "client_disconnect"
			return
		}
		if ctx.Err() != nil {
			e.Status = 504
			e.Outcome = "timeout"
		} else {
			e.Status = 503
			e.Outcome = "model_unavailable"
		}
		apiError(w, e.Status, e.Outcome, "Embedding engine unavailable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		e.Status, e.Outcome = classifyUpstream(resp)
		apiError(w, e.Status, e.Outcome, "Embedding request failed")
		return
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 128<<20+1))
	resp.Body.Close()
	var out map[string]json.RawMessage
	var data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	}
	if err != nil || len(raw) > 128<<20 || json.Unmarshal(raw, &out) != nil || json.Unmarshal(out["data"], &data) != nil || len(data) != len(many) {
		apiError(w, 502, "upstream_failure", "Invalid embedding protocol response")
		return
	}
	dimension := 0
	for i, item := range data {
		if len(item.Embedding) == 0 || item.Index != i || dimension != 0 && len(item.Embedding) != dimension {
			apiError(w, 502, "upstream_failure", "Invalid embedding dimensions")
			return
		}
		dimension = len(item.Embedding)
	}
	out["model"], _ = json.Marshal(e.Model)
	var usage struct {
		Prompt *int64 `json:"prompt_tokens"`
	}
	_ = json.Unmarshal(out["usage"], &usage)
	e.PromptTokens = usage.Prompt
	release(false)
	e.Type = "inference.completed"
	e.Status = 200
	e.Outcome = "completed"
	writeJSON(w, 200, out)
}
