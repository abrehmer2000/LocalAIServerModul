package pool

import (
	"fmt"
	"io"
	"localaiserver/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func awaitState(t *testing.T, p *Pool, state string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for p.Capabilities("m").State != state {
		if time.Now().After(deadline) {
			t.Fatalf("state=%s want %s", p.Capabilities("m").State, state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStaticHealthDoesNotBorrowInferenceConnection(t *testing.T) {
	finish := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/props":
			fmt.Fprint(w, `{"total_slots":1,"default_generation_settings":{"n_ctx":32768},"modalities":{"vision":false}}`)
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"engine"}]}`)
		default:
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-finish
		}
	}))
	defer up.Close()
	p, err := New([]config.Node{{ID: "a", Model: "m", URL: up.URL, UpstreamModel: "engine", Capacity: 1, EngineProtocol: "llama.cpp"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.StartHealth()
	awaitState(t, p, "ready")
	n, release := p.Acquire("m")
	if n == nil {
		t.Fatal("ready node unavailable")
	}
	req, _ := http.NewRequest("POST", n.Endpoint(), strings.NewReader(`{}`))
	resp, err := n.Client.Do(req)
	if err != nil {
		close(finish)
		t.Fatal(err)
	}
	// Two health cycles run while the sole inference connection is occupied.
	time.Sleep(1100 * time.Millisecond)
	state := p.Capabilities("m").State
	close(finish)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	release(false)
	if state != "ready" {
		t.Fatalf("healthy busy engine became %s", state)
	}
	if next, done := p.Acquire("m"); next == nil {
		t.Fatal("free slot blocked by health polling")
	} else {
		done(false)
	}
}

func TestStaticLoadingAndActualContextAreChecked(t *testing.T) {
	var loaded atomic.Bool
	var contextSize atomic.Int64
	contextSize.Store(4096)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			if !loaded.Load() {
				w.WriteHeader(503)
				fmt.Fprint(w, `{"status":"loading model"}`)
			} else {
				fmt.Fprint(w, `{"status":"ok"}`)
			}
		case "/props":
			fmt.Fprintf(w, `{"total_slots":1,"default_generation_settings":{"n_ctx":%d},"modalities":{"vision":false}}`, contextSize.Load())
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"engine"}]}`)
		}
	}))
	defer up.Close()
	p, err := New([]config.Node{{ID: "a", Model: "m", URL: up.URL, UpstreamModel: "engine", Capacity: 1, EngineProtocol: "llama.cpp"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.StartHealth()
	awaitState(t, p, "loading")
	loaded.Store(true)
	awaitState(t, p, "unavailable")
	if n, _ := p.Acquire("m"); n != nil {
		t.Fatal("undersized actual engine context admitted")
	}
	contextSize.Store(32768)
	awaitState(t, p, "ready")
}
