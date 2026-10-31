package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"localaiserver/internal/config"
	"localaiserver/internal/event"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "test-only-key-never-used-in-production"

func cfg(nodes ...config.Node) config.Config {
	h := sha256.Sum256([]byte(testKey))
	return config.Config{Listen: "127.0.0.1:8080", Database: "unused.db", TimeoutSeconds: 5, MaxBodyBytes: 4096, MaxOutputTokens: 128, Keys: []config.Key{{ID: "app-01", SHA256: hex.EncodeToString(h[:])}}, Nodes: nodes}
}
func node(id, url string) config.Node {
	return config.Node{ID: id, URL: url, Model: "chat", UpstreamModel: "engine-model", Revision: "test-revision", Capacity: 1}
}
func request(url, key, user, body string) (*http.Response, error) {
	req, _ := http.NewRequest("POST", url+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if user != "" {
		req.Header.Set("X-User-ID", user)
	}
	return http.DefaultClient.Do(req)
}

const chat = `{"model":"chat","messages":[{"role":"user","content":"PROMPT_SECRET"}],"stream":true}`

func TestLiveStreamingAuditAndSecondBackend(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	seen := make(chan string, 8)
	backend := func(id string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["model"] != "engine-model" || r.Header.Get("Authorization") != "" || r.Header.Get("X-User-ID") != "" {
				t.Error("invalid upstream request / identity leak")
			}
			seen <- id
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"model\":\"engine-model\",\"choices\":[{\"delta\":{\"content\":\"ANSWER_SECRET\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
		}))
	}
	a, b := backend("a"), backend("b")
	defer a.Close()
	defer b.Close()
	store := &memoryAudit{}
	g, err := New(cfg(node("a", a.URL), node("b", b.URL)), store)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	server := httptest.NewServer(g.Handler())
	defer server.Close()
	first, err := request(server.URL, testKey, "user-01", chat)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	// The engine is blocked: observing this line proves live forwarding, not buffering.
	lines := bufio.NewReader(first.Body)
	line, err := lines.ReadString('\n')
	if err != nil || !strings.Contains(line, "ANSWER_SECRET") || !strings.Contains(line, `"model":"chat"`) {
		t.Fatalf("first chunk=%s error=%v", line, err)
	}
	second, err := request(server.URL, testKey, "user-02", chat)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	one, two := <-seen, <-seen
	if one == two {
		t.Fatal("second replica not used")
	}
	full, err := request(server.URL, testKey, "user-03", chat)
	if err != nil {
		t.Fatal(err)
	}
	full.Body.Close()
	if full.StatusCode != 503 {
		t.Fatalf("capacity exceeded: %d", full.StatusCode)
	}
	// Release generators without closing the channel twice.
	release <- struct{}{}
	release <- struct{}{}
	rest, _ := io.ReadAll(lines)
	other, _ := io.ReadAll(second.Body)
	if !bytes.Contains(rest, []byte("[DONE]")) || !bytes.Contains(other, []byte("[DONE]")) {
		t.Fatal("missing completion")
	}
	var records []event.Event
	deadline := time.Now().Add(2 * time.Second)
	for {
		store.mu.Lock()
		records = append([]event.Event(nil), store.events...)
		store.mu.Unlock()
		completed := 0
		for _, r := range records {
			if r.Type == "inference.completed" {
				completed++
			}
		}
		if completed == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion audit missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	encoded, _ := json.Marshal(records)
	for _, secret := range []string{"PROMPT_SECRET", "ANSWER_SECRET", testKey} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("audit leaked %s", secret)
		}
	}
	for _, r := range records {
		if r.Type == "inference.completed" && (r.UserID == "" || r.NodeID == "" || r.RequestID == "" || r.CompletionTokens == nil || *r.CompletionTokens != 2) {
			t.Fatalf("incomplete event: %+v", r)
		}
	}
}

type memoryAudit struct {
	mu     sync.Mutex
	events []event.Event
	fail   bool
}

func (a *memoryAudit) Append(_ context.Context, e event.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail {
		return errors.New("disk full")
	}
	a.events = append(a.events, e)
	return nil
}
func TestValidationAndAuditFailClosed(t *testing.T) {
	for _, tt := range []struct {
		name, key, user, body string
		fail                  bool
		status                int
	}{
		{"missing key", "", "u", chat, false, 401},
		{"missing user", testKey, "", chat, false, 400},
		{"invalid user", testKey, "bad user", chat, false, 400},
		{"unknown parameter", testKey, "u", `{"model":"chat","messages":[],"tools":[]}`, false, 400},
		{"oversized", testKey, "u", strings.Repeat("x", 5000), false, 400},
		{"unknown model", testKey, "u", strings.Replace(chat, `"chat"`, `"other"`, 1), false, 404},
		{"audit unavailable", testKey, "u", chat, true, 503},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &memoryAudit{fail: tt.fail}
			g, err := New(cfg(node("n", "http://127.0.0.1:1")), a)
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.key != "" {
				req.Header.Set("Authorization", "Bearer "+tt.key)
			}
			if tt.user != "" {
				req.Header.Set("X-User-ID", tt.user)
			}
			w := httptest.NewRecorder()
			g.Handler().ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestBrokenStreamNeverCompletes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer upstream.Close()
	a := &memoryAudit{}
	g, err := New(cfg(node("n", upstream.URL)), a)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	s := httptest.NewServer(g.Handler())
	defer s.Close()
	resp, err := request(s.URL, testKey, "u", chat)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if bytes.Contains(data, []byte("[DONE]")) || !bytes.Contains(data, []byte("stream_interrupted")) {
		t.Fatalf("unexpected stream %s", data)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.events) != 2 || a.events[1].Outcome != "stream_interrupted" {
		t.Fatalf("missing failure: %+v", a.events)
	}
}

func TestClientCancellationStopsInference(t *testing.T) {
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer upstream.Close()
	a := &memoryAudit{}
	g, err := New(cfg(node("n", upstream.URL)), a)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	s := httptest.NewServer(g.Handler())
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", s.URL+"/v1/chat/completions", strings.NewReader(chat))
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("X-User-ID", "user-01")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(resp.Body)
	if _, err = reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("engine continues after client cancellation")
	}
}

func TestTimeoutBeforeFirstToken(t *testing.T) {
	stopped := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		close(stopped)
	}))
	defer upstream.Close()
	c := cfg(node("n", upstream.URL))
	c.TimeoutSeconds = 1
	a := &memoryAudit{}
	g, err := New(c, a)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	s := httptest.NewServer(g.Handler())
	defer s.Close()
	resp, err := request(s.URL, testKey, "u", chat)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 504 {
		t.Fatalf("timeout status %d", resp.StatusCode)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("engine request not stopped")
	}
}

func TestAuditFailureLatchesUntilRestart(t *testing.T) {
	a := &memoryAudit{fail: true}
	g, err := New(cfg(node("n", "http://127.0.0.1:1")), a)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chat))
		req.Header.Set("Authorization", "Bearer "+testKey)
		req.Header.Set("X-User-ID", "u")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, req)
		if w.Code != 503 {
			t.Fatalf("unexpected status %d", w.Code)
		}
		a.mu.Lock()
		a.fail = false
		a.mu.Unlock()
	}
}

func TestStatusIsAuthenticatedAuditedAndRedacted(t *testing.T) {
	a := &memoryAudit{}
	g, err := New(cfg(node("local", "http://127.0.0.1:6543")), a)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	request := httptest.NewRequest("GET", "/v1/status", nil)
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, request)
	if w.Code != 401 {
		t.Fatal("status publicly accessible")
	}
	request = httptest.NewRequest("GET", "/v1/status", nil)
	request.Header.Set("Authorization", "Bearer "+testKey)
	request.Header.Set("X-User-ID", "user-01")
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, request)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"configured"`) {
		t.Fatalf("invalid status %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "127.0.0.1") || strings.Contains(w.Body.String(), testKey) {
		t.Fatal("status leaked infrastructure or credentials")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.events[len(a.events)-1].Type != "status.viewed" {
		t.Fatal("status read not audited")
	}
}
