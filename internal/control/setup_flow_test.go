package control_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"localaiserver/internal/agent"
)

func TestSetupTestTargetsExactNodeAndRejectsEmptyAnswer(t *testing.T) {
	f := adminFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var hits [2]atomic.Int32
	var empty atomic.Bool
	for i := 0; i < 2; i++ {
		index := i
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/health":
				fmt.Fprint(w, `{"status":"ok"}`)
			case "/v1/models":
				fmt.Fprint(w, `{"data":[{"id":"chat"}]}`)
			case "/v1/chat/completions":
				hits[index].Add(1)
				if r.Header.Get("Authorization") != "" || r.Header.Get("X-User-ID") != "" {
					t.Error("admin credentials reached engine")
				}
				content := "Hallo"
				if empty.Load() {
					content = ""
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content, "reasoning_content": "private hidden reasoning"}}}})
			}
		}))
		defer backend.Close()
		_, state := f.enrollOutbound(fmt.Sprintf("setup-%d", i), "chat")
		runner, e := agent.New(state, backend.URL)
		if e != nil {
			t.Fatal(e)
		}
		defer runner.Close()
		done := make(chan struct{})
		go func() { defer close(done); runner.RunOutbound(ctx, func(error) {}) }()
		t.Cleanup(func() { cancel(); <-done })
		time.Sleep(100 * time.Millisecond)
		if e = runner.Heartbeat(ctx, true); e != nil {
			t.Fatal(e)
		}
	}
	if status, data := f.admin("POST", "/admin/nodes/setup-1/test", `{}`); status != 200 {
		t.Fatalf("test %d %s", status, data)
	}
	if hits[0].Load() != 0 || hits[1].Load() != 1 {
		t.Fatalf("wrong node received test: %d %d", hits[0].Load(), hits[1].Load())
	}
	empty.Store(true)
	if status, _ := f.admin("POST", "/admin/nodes/setup-1/test", `{}`); status != 502 {
		t.Fatalf("empty answer accepted: %d", status)
	}
	records, _ := f.store.Recent(ctx, 100)
	raw, _ := json.Marshal(records)
	if bytes.Contains(raw, []byte("private hidden")) || bytes.Contains(raw, []byte("Hallo")) {
		t.Fatal("test content persisted in audit")
	}
	if status, _ := f.admin("POST", "/admin/nodes/setup-0/revoke", `{}`); status != 200 {
		t.Fatal(status)
	}
	if status, _ := f.admin("POST", "/admin/nodes/setup-0/test", `{}`); status != 409 {
		t.Fatal("revoked node tested")
	}
}
func TestCentralBackupRequiresAdminAndContainsPortableIdentity(t *testing.T) {
	ordinary := setup(t)
	if status, _ := ordinary.admin("GET", "/admin/backup", ""); status != 403 {
		t.Fatal("ordinary application key exported installation")
	}
	f := adminFixture(t)
	status, data := f.admin("GET", "/admin/backup", "")
	if status != 200 {
		t.Fatalf("backup %d %s", status, data)
	}
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		t.Fatal(e)
	}
	files := map[string][]byte{}
	for _, entry := range z.File {
		r, e := entry.Open()
		if e != nil {
			t.Fatal(e)
		}
		files[entry.Name], e = io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"manifest.json", "gateway.db", "config.json", "pki/ca.key", "pki/gateway.key"} {
		if len(files[name]) == 0 {
			t.Fatalf("missing %s", name)
		}
	}
	var cfg map[string]any
	if json.Unmarshal(files["config.json"], &cfg) != nil || cfg["database"] != "gateway.db" {
		t.Fatal("backup config not portable")
	}
}
