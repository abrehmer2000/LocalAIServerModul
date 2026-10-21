package audit

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSnapshotIncludesLiveWALAndVerifiedChain(t *testing.T) {
	ctx := context.Background()
	s, e := Open(filepath.Join(t.TempDir(), "source.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Append(ctx, Event{Type: "backup.test", Actor: "operator"}); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if e = s.Snapshot(ctx, path); e != nil {
		t.Fatal(e)
	}
	snapshot, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer snapshot.Close()
	head, e := snapshot.Verify(ctx, nil)
	if e != nil || head.Sequence != 1 {
		t.Fatalf("inconsistent snapshot: %+v %v", head, e)
	}
	if e = s.Snapshot(ctx, path); e == nil {
		t.Fatal("snapshot overwrote existing file")
	}
}
