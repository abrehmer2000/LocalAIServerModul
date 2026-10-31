package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"localaiserver/internal/config"
	"localaiserver/internal/control"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestReadinessRequiresConfiguredModel(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"wrong-model"}]}`)
		}
	}))
	defer engine.Close()
	upstream, _ := url.Parse(engine.URL)
	r := &Runner{state: State{Identity: control.Identity{Node: config.Node{UpstreamModel: "expected-model"}}}, upstream: upstream, transport: &http.Transport{}}
	defer r.Close()
	if r.Ready(context.Background()) {
		t.Fatal("wrong model accepted")
	}
}
func TestManagedEngineRejectsChangedModelBeforeExecution(t *testing.T) {
	file := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("expected"))
	upstream, _ := url.Parse("http://127.0.0.1:8081")
	r := &Runner{state: State{Identity: control.Identity{Node: config.Node{ModelSHA256: hex.EncodeToString(h[:])}}}, upstream: upstream}
	if err := r.RunEngine(context.Background(), "must-never-execute", file, 256); err == nil || err.Error() != "model file SHA-256 differs from enrollment grant" {
		t.Fatalf("unexpected result: %v", err)
	}
}
