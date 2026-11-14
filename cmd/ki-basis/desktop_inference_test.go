package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localaiserver/internal/catalog"
	"localaiserver/internal/management"
)

// The child speaks the real engine HTTP contract while keeping model weights
// and inference deterministic. This proves orchestration, not model quality.
func init() {
	if os.Getenv("KI_BASIS_DESKTOP_TEST_ENGINE") != "1" || len(os.Args) < 2 || os.Args[1] != "--model" {
		return
	}
	arguments := map[string]string{}
	for i := 1; i+1 < len(os.Args); i++ {
		if strings.HasPrefix(os.Args[i], "--") && !strings.HasPrefix(os.Args[i+1], "--") {
			arguments[os.Args[i]] = os.Args[i+1]
		}
	}
	started := time.Now()
	server := &http.Server{Addr: net.JoinHostPort(arguments["--host"], arguments["--port"]), ReadHeaderTimeout: time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var response any
		switch r.URL.Path {
		case "/health":
			if time.Since(started) < 800*time.Millisecond {
				w.WriteHeader(503)
			}
			response = map[string]string{"status": "ok"}
		case "/v1/models":
			response = map[string]any{"data": []any{map[string]string{"id": catalog.DefaultAlias}}}
		case "/props":
			response = map[string]any{"total_slots": 1, "chat_template": "fixture-template", "modalities": map[string]bool{"vision": false}, "default_generation_settings": map[string]int{"n_ctx": 32768}}
		case "/apply-template":
			response = map[string]string{"prompt": "fixture-prompt"}
		case "/tokenize":
			response = map[string]any{"tokens": []int{1, 2, 3}}
		case "/slots":
			response = []any{map[string]any{"id": 0, "is_processing": false}}
		case "/v1/chat/completions":
			response = map[string]any{"id": "fixture-answer", "object": "chat.completion", "model": catalog.DefaultAlias, "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": "Hallo aus dem lokalen Testmodell."}}}, "usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 7, "total_tokens": 10}}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	if server.ListenAndServe() != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestDesktopInferenceAcrossProcessLifecycle(t *testing.T) {
	t.Setenv("KI_BASIS_DESKTOP_TEST_ENGINE", "1")
	d := desktopFixture(t, false)
	dir := d.dir
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	modelBytes := []byte("synthetic model fixture, not production model weights")
	modelPath := filepath.Join(dir, "test-model.gguf")
	if err = os.WriteFile(modelPath, modelBytes, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(modelBytes)
	model := management.Model{ID: catalog.DefaultAlias, Repository: catalog.Default().Repository, Filename: catalog.ModelFile, Commit: strings.Repeat("a", 40), SHA256: hex.EncodeToString(digest[:]), Size: int64(len(modelBytes))}
	if _, err = d.store.PinModel(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	d.agent.mu.Lock()
	d.agent.settings = agentSettings{Binary: executable, ExistingModel: modelPath, Models: filepath.Join(dir, "agent", "models")}
	d.agent.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	if err = d.prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err = d.modelAction(ctx, "start"); err != nil {
		t.Fatal(err)
	}
	waitForDesktopAnswer := func(app *desktopApp) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			status := app.status()
			if status["ready"] == true {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("model did not become ready: %#v", status)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err := app.testAnswer(ctx); err != nil {
			t.Fatal(err)
		}
		if app.status()["answer"] != "Hallo aus dem lokalen Testmodell." {
			t.Fatal("actual gateway response was not displayed")
		}
	}
	waitForDesktopAnswer(d)
	identity, err := os.ReadFile(filepath.Join(dir, "agent", "agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	d.close()
	next, err := startDesktop(context.Background(), desktopOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer next.close()
	waitForDesktopAnswer(next)
	restoredIdentity, err := os.ReadFile(filepath.Join(dir, "agent", "agent.json"))
	if err != nil || string(restoredIdentity) != string(identity) {
		t.Fatal("restart replaced local agent identity")
	}
}
