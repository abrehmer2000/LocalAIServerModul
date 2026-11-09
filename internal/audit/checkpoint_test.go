package audit

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
)

func TestSignedCheckpointDetectsRollbackAndTampering(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	if err = s.Append(ctx, Event{Type: "first"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Checkpoint(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.VerifyCheckpoint(ctx, c, pub); err != nil {
		t.Fatal(err)
	}
	c.Sequence++
	if err = s.VerifyCheckpoint(ctx, c, pub); err == nil {
		t.Fatal("modified signature accepted")
	}
	c, _ = s.Checkpoint(ctx, key)
	empty, err := Open(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if err = empty.VerifyCheckpoint(ctx, c, pub); err == nil {
		t.Fatal("rolled back store accepted")
	}
}
