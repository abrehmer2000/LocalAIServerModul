package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeContextAndProjectorIntegrity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vision.gguf")
	data := []byte("projector fixture")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	o := EngineOptions{ContextPerRequest: 32768, Projector: path, ProjectorSHA256: hex.EncodeToString(sum[:])}
	args, err := o.arguments(context.Background(), 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	for _, want := range []string{"--ctx-size 131072", "--parallel 4", "--mmproj " + path, "--reasoning-format deepseek", "--cache-ram 0", "--no-context-shift"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := o.arguments(context.Background(), 4, 0); err == nil {
		t.Fatal("tampered projector accepted")
	}
	for _, bad := range []EngineOptions{{ContextPerRequest: -1}, {ContextPerRequest: 131073}, {Projector: path}, {ProjectorSHA256: o.ProjectorSHA256}} {
		if bad.Validate() == nil {
			t.Fatal("invalid runtime accepted")
		}
	}
	args, err = (EngineOptions{}).arguments(context.Background(), 1, 4096)
	if err == nil {
		t.Fatal("undersized legacy context accepted")
	}
}
