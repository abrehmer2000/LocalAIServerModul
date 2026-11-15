package control_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"localaiserver/internal/audit"
)

func TestFixtureShutdownDrainsActiveAuditBorrower(t *testing.T) {
	f := setup(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan error, 1)
	cancelled := make(chan struct{})
	f.mux.HandleFunc("GET /fixture-shutdown", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-release:
		}
		// A completed handler, including its final audit work, must precede the
		// store cleanup registered by setup. Context cancellation alone is not join.
		completed <- f.store.Append(context.Background(), audit.Event{Type: "fixture.shutdown_completed", Actor: "test"})
	})
	f.client.Timeout = 30 * time.Second
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		response, err := f.client.Get(f.server.URL + "/fixture-shutdown")
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("fixture request did not start")
	}
	stopped := make(chan struct{})
	go func() { f.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		close(release)
		<-stopped
		t.Fatal("fixture shutdown did not cancel and join its active request")
	}
	<-clientDone
	select {
	case <-cancelled:
	default:
		t.Fatal("request did not inherit the fixture lifetime")
	}
	if err := <-completed; err != nil {
		t.Fatalf("database closed before the admitted handler completed: %v", err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatalf("audit store close after fixture stopped: %v", err)
	}
	moved := f.c.Database + ".closed"
	// Windows rejects this operation if SQLite still owns an open file handle.
	if err := os.Rename(f.c.Database, moved); err != nil {
		t.Fatalf("fixture retained audit database handle: %v", err)
	}
	if err := os.Rename(moved, f.c.Database); err != nil {
		t.Fatal(err)
	}
}
