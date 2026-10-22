package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"localaiserver/internal/event"
)

// testNode requires an admin, reserves this exact node and accepts no operator
// prompt. A positive result requires real visible text, not merely HTTP success.
func (s *Service) testNode(w http.ResponseWriter, r *http.Request) {
	if body, e := io.ReadAll(io.LimitReader(r.Body, 1025)); e != nil || len(body) > 1024 || strings.TrimSpace(string(body)) != "{}" {
		deny(w, 400)
		return
	}
	node, release := s.pool.AcquireID(r.PathValue("id"))
	if node == nil {
		respond(w, 409, map[string]string{"error": "node_not_ready_or_busy"})
		return
	}
	failed := true
	defer func() { release(failed) }()
	e := event.FromContext(r.Context())
	e.NodeID = node.Config.ID
	e.Type = "admin.node_test_requested"
	e.Status = 202
	if s.store.Append(r.Context(), e) != nil {
		deny(w, 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"model": node.Config.UpstreamModel, "messages": []map[string]string{{"role": "user", "content": "Antworte ausschließlich mit dem Wort Hallo."}}, "max_tokens": min(1024, s.config.MaxOutputTokens), "stream": false, "chat_template_kwargs": map[string]bool{"enable_thinking": false}})
	req, _ := http.NewRequestWithContext(ctx, "POST", node.Endpoint(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if node.Config.KeyEnv != "" {
		req.Header.Set("Authorization", "Bearer "+os.Getenv(node.Config.KeyEnv))
	}
	response, err := node.Client.Do(req)
	outcome := "test_failed"
	if err == nil {
		defer response.Body.Close()
		var result struct {
			Choices []struct {
				Finish  string `json:"finish_reason"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result) == nil && len(result.Choices) > 0 && strings.TrimSpace(result.Choices[0].Message.Content) != "" && result.Choices[0].Finish == "stop" {
			failed = false
			outcome = "visible_answer_received"
		}
	}
	e.Type = "admin.node_test_completed"
	e.Outcome = outcome
	e.Status = 200
	if failed {
		e.Status = 502
	}
	if s.store.Append(r.Context(), e) != nil {
		deny(w, 503)
		return
	}
	if failed {
		respond(w, 502, map[string]string{"error": "node_test_no_complete_answer"})
		return
	}
	respond(w, 200, map[string]any{"node_id": node.Config.ID, "verified": true, "message": "Testantwort empfangen. Dieser Server kann Anfragen bearbeiten."})
}
