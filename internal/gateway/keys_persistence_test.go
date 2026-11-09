package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"localaiserver/internal/audit"
	"localaiserver/internal/config"
	"localaiserver/internal/event"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestRevokedKeyDoesNotReappearAfterRestart(t *testing.T) {
	store, err := audit.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := cfg(node("n", "http://127.0.0.1:1"))
	g, err := New(c, store)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("synthetic-new-key"))
	next := []config.Key{{ID: "rotated", SHA256: hex.EncodeToString(sum[:]), Models: []string{}}}
	if err = g.ReplaceKeys(next, event.Event{KeyID: c.Keys[0].ID}); err != nil {
		t.Fatal(err)
	}
	g.Close()
	// Deliberately restart with the OLD file: the durable key set is authoritative.
	g, err = New(c, store)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for _, tt := range []struct {
		key  string
		want int
	}{{testKey, 401}, {"synthetic-new-key", 200}} {
		r := httptest.NewRequest("GET", "/v1/models", nil)
		r.Header.Set("Authorization", "Bearer "+tt.key)
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatalf("key state lost on restart: %d", w.Code)
		}
	}
	if _, err = store.Verify(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if g.allowed("rotated", "chat") {
		t.Fatal("empty model list became unrestricted")
	}
}
