package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"localaiserver/internal/config"
	"net/http"
	"time"
)

// Properties contain configuration/template metadata, never generated content.
// V2 llama.cpp identities are not declared ready when their actual properties
// contradict the enrolled model profile.
func (r *Runner) verifyProperties(ctx context.Context, n config.Node) bool {
	work, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(work, "GET", r.upstream.String()+"/props", nil)
	client := &http.Client{Transport: r.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return false
	}
	var props struct {
		Slots      int    `json:"total_slots"`
		Template   string `json:"chat_template"`
		Modalities struct {
			Vision bool `json:"vision"`
		} `json:"modalities"`
		Generation struct {
			Context int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&props) != nil || props.Slots != n.Capacity || props.Generation.Context != n.ContextSize() || props.Modalities.Vision != n.Vision {
		return false
	}
	hash := sha256.Sum256([]byte(props.Template))
	templateHash := hex.EncodeToString(hash[:])
	if n.TemplateSHA256 != "" && n.TemplateSHA256 != templateHash {
		return false
	}
	r.mu.Lock()
	r.observedTemplate = templateHash
	r.observedContext = props.Generation.Context
	r.observedVision = props.Modalities.Vision
	r.mu.Unlock()
	return true
}
