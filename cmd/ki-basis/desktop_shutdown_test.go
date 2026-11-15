package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"localaiserver/internal/event"
)

func TestDesktopShutdownWaitsForCanceledRequestBorrowers(t *testing.T) {
	app := desktopFixture(t, false)
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	handler := app.trackRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(cancelled)
		<-release
		// Request cleanup may need one final audit transaction after cancellation.
		// The database must remain open until the admitted handler has returned.
		finished <- app.store.Append(context.Background(), event.Event{Type: "shutdown.test_request_completed", Actor: "test"})
	}))
	request := httptest.NewRequest("GET", app.address+"/shutdown-test", nil)
	request = request.WithContext(app.backend.BaseContext(nil))
	go handler.ServeHTTP(httptest.NewRecorder(), request)
	<-entered
	stopped := make(chan struct{})
	go func() { app.close(); close(stopped) }()
	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("shutdown did not cancel the server request base context")
	}
	select {
	case <-stopped:
		close(release)
		t.Fatal("shutdown returned while an admitted handler still owned the store")
	default:
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not join its canceled request")
	}
	if err := <-finished; err != nil {
		t.Fatalf("store closed before handler cleanup: %v", err)
	}
	if app.closeErr != nil {
		t.Fatal(app.closeErr)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", app.address+"/shutdown-test", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatal("new borrower admitted after shutdown")
	}
}

func TestDesktopShutdownReleasesDatabaseBeforeRestart(t *testing.T) {
	app := desktopFixture(t, false)
	dir := app.dir
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	app.agent.mu.Lock()
	app.agent.settings = agentSettings{Binary: binary, Models: filepath.Join(dir, "agent", "models")}
	app.agent.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = app.prepare(ctx); err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(dir, "agent", "agent.json")
	identity, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 3; iteration++ {
		response := desktopRequest(t, app, "POST", "/desktop/access", app.token, app.address)
		var access desktopApplicationAccess
		if err = json.NewDecoder(response.Body).Decode(&access); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		app.close()
		if app.closeErr != nil {
			t.Fatal(app.closeErr)
		}
		database := filepath.Join(dir, "central", "gateway.db")
		moved := database + ".closed"
		// Windows refuses the rename while any SQLite borrower retains its handle.
		// Restoring the name then reopening also checks the actual persisted state.
		if err = os.Rename(database, moved); err != nil {
			t.Fatalf("closed database still in use: %v", err)
		}
		if err = os.Rename(moved, database); err != nil {
			t.Fatal(err)
		}
		next, startErr := startDesktop(context.Background(), desktopOptions{Dir: dir})
		if startErr != nil {
			t.Fatal(startErr)
		}
		app = next
		t.Cleanup(app.close)
		saved, readErr := os.ReadFile(identityPath)
		if readErr != nil || string(saved) != string(identity) {
			t.Fatal("shutdown/restart replaced the agent identity")
		}
	}
	app.close()
	if app.closeErr != nil {
		t.Fatal(app.closeErr)
	}
	if err = os.RemoveAll(dir); err != nil {
		t.Fatalf("shutdown left installation files in use: %v", err)
	}
}
