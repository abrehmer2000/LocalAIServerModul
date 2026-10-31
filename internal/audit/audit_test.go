package audit

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentChainAndPrivacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Append(context.Background(), Event{Type: "inference.completed", UserID: "user-01", KeyID: "app", Model: "chat"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	head, err := s.Verify(context.Background(), nil)
	if err != nil || head.Sequence != 30 {
		t.Fatalf("head=%+v err=%v", head, err)
	}
	if _, err := s.db.Exec(`UPDATE audit SET hash='changed' WHERE sequence=1`); err == nil {
		t.Fatal("update allowed")
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`DROP TRIGGER audit_no_update; UPDATE audit SET hash='changed' WHERE sequence=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(path); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("tampering undetected: %v", err)
	}
}

func TestIndependentWritersSerializeBeforeReadingHead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared audit.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var wg sync.WaitGroup
	for _, store := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if err := s.Append(context.Background(), Event{Type: "test.concurrent"}); err != nil {
					t.Error(err)
					return
				}
			}
		}(store)
	}
	wg.Wait()
	head, err := a.Verify(context.Background(), nil)
	if err != nil || head.Sequence != 40 {
		t.Fatalf("concurrent stores: sequence=%d error=%v", head.Sequence, err)
	}
}
