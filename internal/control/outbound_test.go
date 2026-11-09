package control_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"localaiserver/internal/agent"
	"localaiserver/internal/audit"
	"localaiserver/internal/catalog"
	"localaiserver/internal/control"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func (f *fixture) admin(method, path, body string) (int, []byte) {
	f.t.Helper()
	req, _ := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-User-ID", "operator-01")
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}
func adminFixture(t *testing.T) *fixture {
	f := setup(t)
	f.server.Close()
	f.g.Close()
	f.control.Close()
	f.c.Keys[0].Admin = true
	f.start()
	return f
}
func (f *fixture) enrollOutbound(id, model string) (*agent.Runner, string) {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"id": id, "gateway": f.server.URL, "model": model, "revision": "r1", "capacity": 1, "vision": true})
	status, data := f.admin("POST", "/admin/enroll", string(body))
	if status != 201 {
		f.t.Fatalf("enroll %d %s", status, data)
	}
	var bundle control.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		f.t.Fatal(err)
	}
	state := filepath.Join(f.dir, id+".json")
	if err := agent.Join(context.Background(), bundle.Gateway, bundle.Token, f.c.ControlCACert, state); err != nil {
		f.t.Fatal(err)
	}
	return nil, state
}
func TestOutboundStreamingSecondNodeCancellationAndRevocation(t *testing.T) {
	f := adminFixture(t)
	var hits [2]atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 2; i++ {
		index := i
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/health":
				fmt.Fprint(w, `{"status":"ok"}`)
			case "/v1/models":
				fmt.Fprint(w, `{"data":[{"id":"chat"}]}`)
			case "/v1/chat/completions":
				if r.Header.Get("Authorization") != "" || r.Header.Get("X-User-ID") != "" {
					t.Error("credentials reached model")
				}
				hits[index].Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"node-%d\"}}]}\n\n", index)
				w.(http.Flusher).Flush()
				time.Sleep(50 * time.Millisecond)
				fmt.Fprint(w, "data: [DONE]\n\n")
			}
		}))
		defer backend.Close()
		_, path := f.enrollOutbound(fmt.Sprintf("outbound-%d", i), "chat")
		runner, err := agent.New(path, backend.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer runner.Close()
		if !runner.Ready(ctx) {
			t.Fatal("fake engine not ready")
		}
		done := make(chan struct{})
		go func() { defer close(done); runner.RunOutbound(ctx, func(error) {}) }()
		t.Cleanup(func() { cancel(); <-done })
		time.Sleep(100 * time.Millisecond)
		if err = runner.Heartbeat(ctx, true); err != nil {
			t.Fatal(err)
		}
		// Adding the second node requires no gateway restart and both receive requests.
		for request := 0; request < 3; request++ {
			f.chat(200)
		}
	}
	if hits[0].Load() == 0 || hits[1].Load() == 0 {
		t.Fatal("both replicas did not receive requests")
	}
	if status, data := f.admin("POST", "/admin/nodes/outbound-0/revoke", `{}`); status != 200 {
		t.Fatalf("revoke %d %s", status, data)
	}
	before := hits[0].Load()
	f.chat(200)
	if hits[0].Load() != before {
		t.Fatal("revoked node used")
	}
	records, _ := f.store.Recent(ctx, 100)
	for _, r := range records {
		data, _ := json.Marshal(r)
		if bytes.Contains(data, []byte("private prompt")) || bytes.Contains(data, []byte("node-0\"")) {
			t.Fatal("content persisted")
		}
	}
	if _, err := f.store.Verify(ctx, nil); err != nil {
		t.Fatal(err)
	}
}
func TestAdminRequiresPrivilegeAndBundleDoesNotLeakSecrets(t *testing.T) {
	f := setup(t)
	// Even a valid application key cannot issue device identities.
	if status, _ := f.admin("POST", "/admin/enroll", `{}`); status != 403 {
		t.Fatalf("ordinary key status %d", status)
	}
	f.server.Close()
	f.g.Close()
	f.control.Close()
	f.c.Keys[0].Admin = true
	f.start()
	body, _ := json.Marshal(map[string]any{"id": "test-node", "gateway": f.server.URL, "model": catalog.DefaultAlias})
	status, data := f.admin("POST", "/admin/enroll", string(body))
	if status != 201 {
		t.Fatalf("%d %s", status, data)
	}
	if bytes.Contains(data, []byte(key)) || bytes.Contains(data, []byte("PRIVATE KEY")) {
		t.Fatal("bundle includes private key")
	}
	var bundle control.Bundle
	json.Unmarshal(data, &bundle)
	path := filepath.Join(f.dir, "one.json")
	if err := agent.Join(context.Background(), bundle.Gateway, bundle.Token, f.c.ControlCACert, path); err != nil {
		t.Fatal(err)
	}
	if err := agent.Join(context.Background(), bundle.Gateway, bundle.Token, f.c.ControlCACert, filepath.Join(f.dir, "replay.json")); err == nil {
		t.Fatal("bundle replay accepted")
	}
	status, records := f.admin("GET", "/admin/audit", "")
	if status != 200 || bytes.Contains(records, []byte(bundle.Token)) {
		t.Fatal("token leaked to audit")
	}
	if _, err := f.store.Verify(context.Background(), func(r audit.Record) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestOutboundLongImageRequestAndRetryAfter(t *testing.T) {
	if testing.Short() {
		t.Skip("31-second outbound inference regression")
	}
	f := adminFixture(t)
	f.server.Close()
	f.g.Close()
	f.control.Close()
	f.c.TimeoutSeconds = 60
	f.c.MaxBodyBytes = 16 << 20
	f.start()
	f.client.Timeout = 55 * time.Second
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			fmt.Fprint(w, `{"status":"ok"}`)
			return
		}
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"chat"}]}`)
			return
		}
		var body struct {
			Messages    []json.RawMessage `json:"messages"`
			CachePrompt *bool             `json:"cache_prompt"`
			Thinking    struct {
				EnableThinking bool `json:"enable_thinking"`
			} `json:"chat_template_kwargs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if body.CachePrompt == nil || *body.CachePrompt || !body.Thinking.EnableThinking {
			t.Error("cache/thinking changed in tunnel")
		}
		if len(body.Messages) != 1 || len(body.Messages[0]) < 8<<20 {
			t.Error("large image request truncated")
		}
		if calls.Add(1) > 1 {
			w.Header().Set("Retry-After", "17")
			w.WriteHeader(429)
			return
		}
		select {
		case <-time.After(31 * time.Second):
			fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	_, path := f.enrollOutbound("long-image", "chat")
	runner, err := agent.New(path, backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); runner.RunOutbound(ctx, func(error) {}) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err = runner.Heartbeat(ctx, true); err != nil {
			t.Fatal(err)
		}
		if f.g.Pool().Snapshot()[0].State == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("outbound worker not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	image := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, (8<<20)-8)...)
	body := `{"model":"chat","messages":[{"role":"user","content":[{"type":"text","text":"` + strings.Repeat("n", 70000) + `"},{"type":"image_url","image_url":{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(image) + `"}}]}],"chat_template_kwargs":{"enable_thinking":true}}`
	// Heartbeats must continue while generation exceeds the ready lease.
	hbDone := make(chan struct{})
	go func() { defer close(hbDone); runner.RunHeartbeats(ctx, func(error) {}) }()
	defer func() { cancel(); <-hbDone }()
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("POST", f.server.URL+"/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("X-User-ID", "synthetic-user")
		req.Header.Set("Content-Type", "application/json")
		resp, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if i == 0 && (resp.StatusCode != 200 || !bytes.Contains(data, []byte(`"content":"ok"`))) {
			t.Fatalf("delayed response %d: %s", resp.StatusCode, data)
		}
		if i == 1 && (resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "17") {
			t.Fatalf("lost overload status/header: %d %s", resp.StatusCode, resp.Header.Get("Retry-After"))
		}
	}
}
