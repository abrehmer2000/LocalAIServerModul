package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"localaiserver/internal/pool"
	"net/http"
	"os"
	"strings"
	"time"
)

// engineJSON is also transported through the authenticated outbound agent; it
// never sends prompts to third-party tokenizers or persists tokenization input.
func engineJSON(ctx context.Context, n *pool.Node, path string, in, out any) error {
	raw, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(n.Config.URL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if n.Config.KeyEnv != "" {
		req.Header.Set("Authorization", "Bearer "+os.Getenv(n.Config.KeyEnv))
	}
	resp, err := n.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		status, code := classifyUpstream(resp)
		return &preprocessError{status: status, code: code}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

type preprocessError struct {
	status int
	code   string
}

func (e *preprocessError) Error() string { return e.code }
func (g *Gateway) checkContext(ctx context.Context, v chatRequest, n *pool.Node) error {
	if n.Config.EngineProtocol != "llama.cpp" || hasImage(v.Messages) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var template struct {
		Prompt string `json:"prompt"`
	}
	if err := engineJSON(ctx, n, "/apply-template", map[string]any{"messages": v.Messages, "chat_template_kwargs": v.ChatTemplate}, &template); err != nil {
		return err
	}
	var tokenized struct {
		Tokens []json.RawMessage `json:"tokens"`
	}
	if err := engineJSON(ctx, n, "/tokenize", map[string]any{"content": template.Prompt, "add_special": true}, &tokenized); err != nil {
		return err
	}
	if len(tokenized.Tokens) == 0 {
		return errors.New("engine tokenizer returned no tokens")
	}
	if len(tokenized.Tokens) >= n.Config.ContextSize() {
		return errContextExceeded
	}
	return nil
}

var errContextExceeded = errors.New("context_length_exceeded")
