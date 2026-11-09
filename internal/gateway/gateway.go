// Package gateway implements the deliberately small OpenAI-compatible API surface.
package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"localaiserver/internal/config"
	"localaiserver/internal/event"
	"localaiserver/internal/logmeta"
	"localaiserver/internal/pool"
	"localaiserver/internal/webui"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type Auditor interface {
	Append(context.Context, event.Event) error
}
type Gateway struct {
	keyMutation  sync.Mutex
	requestLog   *logmeta.Writer
	cfg          config.Config
	audit        Auditor
	pool         *pool.Pool
	auditFailed  atomic.Bool
	keys         keyStore
	ingressSlots chan struct{}
	metrics      telemetry
}

func New(c config.Config, a Auditor) (*Gateway, error) {
	if store, ok := a.(persistentKeys); ok {
		keys, err := store.LoadKeys(context.Background())
		if err != nil {
			return nil, err
		}
		if keys != nil {
			c.Keys = keys
		}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	p, err := pool.New(c.Nodes)
	if err != nil {
		return nil, err
	}
	var requestLog *logmeta.Writer
	if c.RequestLog != "" {
		requestLog, err = logmeta.Open(c.RequestLog, 100_000_000, 7)
		if err != nil {
			p.Close()
			return nil, err
		}
	}
	return &Gateway{requestLog: requestLog, cfg: c, audit: a, pool: p, keys: keyStore{keys: append([]config.Key(nil), c.Keys...)}, ingressSlots: make(chan struct{}, c.ConcurrentLimit())}, nil
}
func (g *Gateway) Pool() *pool.Pool { return g.pool }
func (g *Gateway) Close() {
	g.pool.Close()
	if g.requestLog != nil {
		_ = g.requestLog.Close()
	}
}
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { g.health(w, r) })
	mux.HandleFunc("GET /v1/models", g.models)
	mux.HandleFunc("GET /v1/status", g.status)
	mux.HandleFunc("POST /v1/import-leases", g.importLease)
	mux.HandleFunc("PUT /v1/import-leases/{id}", g.importLease)
	mux.HandleFunc("DELETE /v1/import-leases/{id}", g.importLease)
	mux.HandleFunc("GET /v1/models/{model}/capabilities", g.capabilities)
	mux.Handle("GET /metrics", g.Admin(http.HandlerFunc(g.metricHandler)))
	mux.Handle("/admin/keys", g.Admin(http.NotFoundHandler()))
	ui := webui.Handler()
	for _, path := range []string{"/{$}", "/app.js", "/style.css", "/model-profile"} {
		mux.Handle(path, ui)
	}
	mux.HandleFunc("POST /v1/chat/completions", g.chat)
	mux.HandleFunc("POST /v1/embeddings", g.embeddings)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { apiError(w, 404, "not_found", "Endpoint not supported") })
	return g.ingress(mux)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, code, message string) {
	code = canonicalCode(code)
	if retryable(code) && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": "gateway_error", "param": nil, "code": code}})
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (g *Gateway) append(e event.Event) error {
	e.Outcome = canonicalCode(e.Outcome)
	if e.Type == "inference.completed" || e.Type == "inference.failed" || e.Type == "request.rejected" {
		g.metrics.record(e)
		if g.requestLog != nil {
			raw, _ := json.Marshal(struct {
				Time  time.Time   `json:"time"`
				Event event.Event `json:"event"`
			}{time.Now().UTC(), e})
			if _, err := g.requestLog.Write(append(raw, 10)); err != nil {
				slog.Error("request metadata log unavailable")
			}
		}
	}
	if g.auditFailed.Load() {
		return errors.New("audit unavailable; restart after recovery required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := g.audit.Append(ctx, e)
	if err != nil {
		g.auditFailed.Store(true)
	}
	return err
}
func (g *Gateway) reject(w http.ResponseWriter, e event.Event, status int, code, msg string) {
	if retryable(code) {
		w.Header().Set("Retry-After", fmt.Sprint(g.pool.RetryAfter(e.Model)))
	}
	e.Type = "request.rejected"
	e.Status = status
	e.Outcome = code
	if err := g.append(e); err != nil {
		slog.Error("audit write failed", "request_id", e.RequestID)
		apiError(w, 503, "audit_unavailable", "Audit storage unavailable")
		return
	}
	apiError(w, status, code, msg)
}
func (g *Gateway) identify(w http.ResponseWriter, r *http.Request) (event.Event, bool) {
	e := event.Event{RequestID: newID()}
	if ids := r.Header.Values("X-Request-ID"); len(ids) == 1 && requestPattern.MatchString(ids[0]) {
		e.RequestID = ids[0]
	}
	w.Header().Set("X-Request-ID", e.RequestID)
	if g.auditFailed.Load() {
		apiError(w, 503, "audit_unavailable", "Audit storage unavailable; operator recovery required")
		return e, false
	}
	w.Header().Set("X-Request-ID", e.RequestID)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	auth := r.Header.Get("Authorization")
	candidate := ""
	if strings.HasPrefix(auth, "Bearer ") {
		candidate = strings.TrimPrefix(auth, "Bearer ")
	}
	h := sha256.Sum256([]byte(candidate))
	for _, k := range g.keySnapshot() {
		expected, _ := hex.DecodeString(k.SHA256)
		if subtle.ConstantTimeCompare(h[:], expected) == 1 && candidate != "" {
			e.KeyID = k.ID
		}
	}
	if e.KeyID == "" {
		g.reject(w, e, 401, "invalid_api_key", "Valid Bearer API key required")
		return e, false
	}
	users := r.Header.Values("X-User-ID")
	if len(users) > 1 || len(users) == 1 && !userPattern.MatchString(users[0]) {
		g.reject(w, e, 400, "invalid_user_id", "Invalid X-User-ID (1-64 ASCII letters, digits, dot, underscore, at or hyphen)")
		return e, false
	}
	if len(users) == 1 {
		e.UserID = users[0]
	}
	return e, true
}
func (g *Gateway) models(w http.ResponseWriter, r *http.Request) {
	e, ok := g.identify(w, r)
	if !ok {
		return
	}
	e.Type = "models.listed"
	e.Status = 200
	if err := g.append(e); err != nil {
		apiError(w, 503, "audit_unavailable", "Audit storage unavailable")
		return
	}
	data := []map[string]any{}
	for _, m := range g.pool.Models() {
		if !g.allowed(e.KeyID, m) {
			continue
		}
		data = append(data, map[string]any{"id": m, "object": "model", "created": 0, "owned_by": "local"})
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data})
}

type chatRequest struct {
	ResponseFormat      *responseFormat   `json:"response_format,omitempty"`
	CachePrompt         bool              `json:"cache_prompt"`
	Model               string            `json:"model"`
	Messages            []json.RawMessage `json:"messages"`
	Stream              bool              `json:"stream,omitempty"`
	MaxTokens           *int              `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int              `json:"max_completion_tokens,omitempty"`
	Temperature         *float64          `json:"temperature,omitempty"`
	TopP                *float64          `json:"top_p,omitempty"`
	Stop                json.RawMessage   `json:"stop,omitempty"`
	Seed                *int64            `json:"seed,omitempty"`
	FrequencyPenalty    *float64          `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64          `json:"presence_penalty,omitempty"`
	StreamOptions       *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	ChatTemplate *struct {
		EnableThinking *bool `json:"enable_thinking,omitempty"`
	} `json:"chat_template_kwargs,omitempty"`
	User string `json:"user,omitempty"`
	N    *int   `json:"n,omitempty"`
}

func decodeRequest(w http.ResponseWriter, r *http.Request, c config.Config) (chatRequest, error) {
	var v chatRequest
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, c.MaxBodyBytes))
	if err != nil {
		return v, err
	}
	if !utf8.Valid(raw) || !validUnicodeEscapes(raw) {
		return v, errors.New("invalid Unicode")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return v, err
	}
	if d.Decode(new(any)) != io.EOF {
		return v, errors.New("trailing JSON")
	}
	if !config.ValidID(v.Model) || len(v.Messages) == 0 || len(v.Messages) > 1024 {
		return v, errors.New("model and 1-1024 messages required")
	}
	if err := validateMessages(v.Messages); err != nil {
		return v, err
	}
	if err := v.ResponseFormat.prepare(); err != nil {
		return v, err
	}
	if v.Stream && v.ResponseFormat != nil && v.ResponseFormat.Type != "text" {
		return v, errors.New("structured responses require stream=false")
	}
	// Never reuse prompt caches, even if a caller explicitly requests it.
	v.CachePrompt = false

	if v.N != nil && *v.N != 1 {
		return v, errors.New("only n=1 supported")
	}
	if v.MaxTokens != nil && v.MaxCompletionTokens != nil {
		return v, errors.New("choose one output limit")
	}
	limit := v.MaxTokens
	if v.MaxCompletionTokens != nil {
		limit = v.MaxCompletionTokens
	}
	if limit != nil && (*limit < 1 || *limit > c.MaxOutputTokens) {
		return v, errors.New("output limit outside configured range")
	}
	// No implicit output cap: llama.cpp uses the available context when omitted.
	if v.Temperature != nil && (*v.Temperature < 0 || *v.Temperature > 2) {
		return v, errors.New("temperature out of range")
	}
	if v.TopP != nil && (*v.TopP < 0 || *v.TopP > 1) {
		return v, errors.New("top_p out of range")
	}
	for _, p := range []*float64{v.FrequencyPenalty, v.PresencePenalty} {
		if p != nil && (*p < -2 || *p > 2) {
			return v, errors.New("penalty out of range")
		}
	}
	if len(v.Stop) > 0 {
		var one string
		var many []string
		if json.Unmarshal(v.Stop, &one) != nil {
			if json.Unmarshal(v.Stop, &many) != nil || len(many) > 4 {
				return v, errors.New("invalid stop")
			}
		}
	}
	return v, nil
}
func (g *Gateway) chat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	e, ok := g.identify(w, r)
	if !ok {
		return
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		g.reject(w, e, 400, "invalid_request", "JSON content type required")
		return
	}
	v, err := decodeRequest(w, r, g.cfg)
	if err != nil {
		status := 400
		var big *http.MaxBytesError
		if errors.As(err, &big) || errors.Is(err, errSchemaTooLarge) {
			status = 413
		}
		g.reject(w, e, status, "invalid_request", "Invalid or oversized request")
		return
	}
	e.Model = v.Model
	if v.User != "" && e.UserID != "" && v.User != e.UserID {
		g.reject(w, e, 400, "invalid_request", "Conflicting user identity")
		return
	}
	v.User = ""
	if !g.pool.Has(v.Model) {
		g.reject(w, e, 404, "model_not_found", "Unknown model")
		return
	}
	if node := g.pool.ProbeNode(v.Model); node != nil && node.Config.Kind == "embedding" {
		g.reject(w, e, 400, "invalid_request", "Alias is an embedding model")
		return
	}
	if !g.allowed(e.KeyID, v.Model) {
		g.reject(w, e, 403, "forbidden", "Model access denied")
		return
	}
	if hasImage(v.Messages) && !g.pool.Capabilities(v.Model).Vision {
		g.reject(w, e, 422, "vision_not_supported", "Model does not support images")
		return
	}
	if err := g.checkContext(r.Context(), v); err != nil {
		if errors.Is(err, errContextExceeded) {
			g.reject(w, e, 400, "context_length_exceeded", "Input exceeds model context")
		} else if r.Context().Err() == nil {
			g.reject(w, e, 503, "model_unavailable", "Engine preprocessing unavailable")
		}
		return
	}
	n, release, err := g.pool.WaitAcquire(r.Context(), v.Model, r.Header.Get("X-Priority") == "interactive", g.cfg.QueueSize(), g.cfg.QueueWait())
	if err != nil {
		if r.Context().Err() != nil {
			e.Type = "inference.failed"
			e.Outcome = "client_disconnect"
			_ = g.append(e)
			return
		}
		code := err.Error()
		if code != "model_busy" && code != "model_loading" {
			code = "model_unavailable"
		}
		g.reject(w, e, 503, code, "Model temporarily unavailable")
		return
	}
	failed := false
	defer func() { release(failed) }()
	e.NodeID = n.Config.ID
	e.Revision = n.Config.Revision
	e.Type = "inference.accepted"
	if err = g.append(e); err != nil {
		apiError(w, 503, "model_unavailable", "Audit storage unavailable")
		return
	}
	e.Type = "inference.failed"
	e.Status = 502
	e.Outcome = "upstream_failure"
	defer func() {
		release(failed)
		e.DurationMS = time.Since(start).Milliseconds()
		if err := g.append(e); err != nil {
			slog.Error("completion audit write failed", "request_id", e.RequestID)
		}
	}()
	fail := func(status int, code, msg string) {
		e.Status = status
		e.Outcome = code
		apiError(w, status, code, msg)
	}
	ctx, cancel := context.WithTimeout(r.Context(), g.cfg.Timeout())
	defer cancel()
	v.Model = n.Config.UpstreamModel
	if v.Stream {
		v.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{true}
	}
	body, _ := json.Marshal(v)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.Endpoint(), bytes.NewReader(body))
	if err != nil {
		fail(502, "upstream_failure", "Cannot construct engine request")
		return
	}
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
			fail(504, "timeout", "Generation time limit exceeded")
			return
		}
		failed = true
		fail(503, "model_unavailable", "Engine unavailable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		status, code := classifyUpstream(resp)
		if retryable(code) {
			w.Header().Set("Retry-After", retryAfter(resp.Header.Get("Retry-After")))
		}
		failed = code == "upstream_failure"
		fail(status, code, "Engine request could not be completed")
		return
	}
	if v.Stream {
		g.stream(w, resp, e.Model, &e, &failed)
		if r.Context().Err() != nil {
			failed = false
			e.Status = 0
			e.Outcome = "client_disconnect"
		} else if ctx.Err() != nil {
			failed = false
			e.Outcome = "timeout"
		}
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20+1))
	resp.Body.Close()
	if err != nil || len(data) > 128<<20 {
		if r.Context().Err() != nil {
			e.Status = 0
			e.Outcome = "client_disconnect"
			return
		}
		if ctx.Err() != nil {
			fail(504, "timeout", "Generation time limit exceeded")
			return
		}
		failed = true
		fail(502, "upstream_failure", "Invalid engine transport response")
		return
	}
	data, err = prepareCompletion(data, v, e.Model, e.RequestID, &e)
	if err != nil {
		failed = true
		fail(502, "upstream_failure", "Invalid engine protocol response")
		return
	}
	release(false) // Engine finished: neither audit latency nor a slow reader owns a compute slot.
	e.Type = "inference.completed"
	e.Outcome = "completed"
	e.Status = 200
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	if _, err = w.Write(data); err != nil {
		e.Type = "inference.failed"
		e.Outcome = "client_disconnect"
		e.Status = 0
	}
}
func normalize(data []byte, model string, e *event.Event) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return nil, errors.New("invalid chunk")
	}
	if _, ok := obj["error"]; ok {
		return nil, errors.New("upstream error")
	}
	var choices []json.RawMessage
	if raw, ok := obj["choices"]; !ok || json.Unmarshal(raw, &choices) != nil {
		return nil, errors.New("choices missing")
	}
	if raw, ok := obj["usage"]; ok && string(raw) != "null" {
		var u struct {
			Prompt     *int64 `json:"prompt_tokens"`
			Completion *int64 `json:"completion_tokens"`
		}
		if json.Unmarshal(raw, &u) == nil {
			e.PromptTokens = u.Prompt
			e.CompletionTokens = u.Completion
		}
	}
	obj["model"], _ = json.Marshal(model)
	return json.Marshal(obj)
}
func (g *Gateway) stream(w http.ResponseWriter, resp *http.Response, model string, e *event.Event, failed *bool) {
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		*failed = true
		apiError(w, 502, "invalid_upstream", "Expected SSE response")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var event strings.Builder
	begun := false
	done := false
	reasoning := &reasoningStream{}
	send := func(data string) error {
		if data == "[DONE]" {
			done = true
		} else {
			normalized, err := normalize([]byte(data), model, e)
			if err != nil {
				return err
			}
			normalized, err = reasoning.filter(normalized)
			if err != nil {
				return err
			}
			data = string(normalized)
		}
		begun = true
		if err := rc.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		return rc.Flush()
	}
	var streamErr error
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if event.Len() > 0 {
				streamErr = send(strings.TrimSuffix(event.String(), "\n"))
				event.Reset()
				if streamErr != nil || done {
					break
				}
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if event.Len()+len(line) > 1<<20 {
				streamErr = errors.New("event too large")
				break
			}
			event.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			event.WriteByte('\n')
		}
	}
	if streamErr == nil {
		streamErr = scanner.Err()
	}
	if done && streamErr == nil {
		e.Type = "inference.completed"
		e.Status = 200
		e.Outcome = "completed"
		return
	}
	*failed = true
	e.Outcome = "stream_interrupted"
	if !begun {
		apiError(w, 502, "stream_interrupted", "Inference stream interrupted")
		return
	}
	e.Status = 200
	// A truncated stream never receives [DONE]. The terminal error is content-free.
	_ = rc.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(w, "data: {\"error\":{\"message\":\"Inference stream interrupted\",\"type\":\"gateway_error\",\"code\":\"upstream_failure\"}}\n\n")
	_ = rc.Flush()
}

func (g *Gateway) status(w http.ResponseWriter, r *http.Request) {
	e, ok := g.identify(w, r)
	if !ok {
		return
	}
	e.Type = "status.viewed"
	e.Status = 200
	if err := g.append(e); err != nil {
		apiError(w, 503, "audit_unavailable", "Audit storage unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"nodes": g.pool.Snapshot(), "models": g.pool.Models(), "queue_length": g.pool.QueueLength("")})
}

// Admin separates management privileges from ordinary inference API credentials.
func (g *Gateway) Admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e, ok := g.identify(w, r)
		if !ok {
			return
		}
		allowed := false
		for _, k := range g.keySnapshot() {
			if k.ID == e.KeyID && k.Admin {
				allowed = true
			}
		}
		if !allowed {
			g.reject(w, e, 403, "admin_required", "Administrator key required")
			return
		}
		// Bearer headers are never attached automatically by browsers. Reject cross-site mutations too.
		if r.Method != "GET" && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			g.reject(w, e, 403, "cross_site_request", "Same-origin request required")
			return
		}
		e.Actor = "admin:" + e.UserID
		if e.UserID == "" {
			e.Actor = "admin-key:" + e.KeyID
		}
		if r.URL.Path == "/admin/keys" {
			g.keyAdmin(w, r.WithContext(event.WithContext(r.Context(), e)))
			return
		}
		next.ServeHTTP(w, r.WithContext(event.WithContext(r.Context(), e)))
	})
}
