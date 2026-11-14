package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"localaiserver/internal/audit"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localaiserver/internal/config"
	"localaiserver/internal/event"
)

func TestDesktopApplicationAccessUsesScopedKeyAndPreservesRevocation(t *testing.T) {
	d := desktopFixture(t, false)
	access, err := d.applicationAccess(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if access.Key == d.key || access.Model == "" || access.CAPEM == "" || access.BaseURL != d.gatewayURL+"/v1" {
		t.Fatal("application access contains wrong role or connection")
	}
	for _, tc := range []struct {
		path string
		want int
	}{{"/v1/models", 200}, {"/admin/overview", 403}} {
		req, _ := http.NewRequest("GET", d.gatewayURL+tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+access.Key)
		req.Header.Set("X-User-ID", access.User)
		res, err := d.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatalf("application key %s: %d", tc.path, res.StatusCode)
		}
	}
	res := desktopRequest(t, d, "GET", "/desktop/status", d.token, d.address)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(raw), access.Key) {
		t.Fatal("application key leaked into polling status")
	}
	keys, err := d.store.LoadKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var keep []config.Key
	for _, k := range keys {
		if k.ID != desktopApplicationKeyID {
			keep = append(keep, k)
		}
	}
	if err = d.gateway.ReplaceKeys(keep, event.Event{Actor: "test-operator"}); err != nil {
		t.Fatal(err)
	}
	if _, err = d.applicationAccess(context.Background()); err == nil {
		t.Fatal("revoked key exported")
	}
	dir := d.dir
	d.close()
	next, err := startDesktop(context.Background(), desktopOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer next.close()
	if _, err = next.applicationAccess(context.Background()); err == nil {
		t.Fatal("restart resurrected revoked key")
	}
	// The stale configuration still contains the old hash, but the durable set is
	// authoritative. Export must not rewrite it or restore a missing key file.
	if err = os.Remove(filepath.Join(dir, "central", desktopApplicationFile)); err != nil {
		t.Fatal(err)
	}
	if _, err = next.applicationAccess(context.Background()); err == nil {
		t.Fatal("missing credential regenerated on export")
	}
	if _, err = os.Stat(filepath.Join(dir, "central", desktopApplicationFile)); !os.IsNotExist(err) {
		t.Fatal("missing credential file recreated")
	}
}
func TestDesktopUsesDurableAdministratorRotation(t *testing.T) {
	d := desktopFixture(t, false)
	dir := d.dir
	replacement := "kb_" + strings.Repeat("b", 64)
	hash := sha256.Sum256([]byte(replacement))
	keys, err := d.store.LoadKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range keys {
		if keys[i].Admin {
			keys[i].SHA256 = hex.EncodeToString(hash[:])
		}
	}
	raw, _ := json.Marshal(map[string]string{"key": replacement})
	if err = os.WriteFile(filepath.Join(dir, "central", "new-api-key.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = d.gateway.ReplaceKeys(keys, event.Event{Actor: "test-operator"}); err != nil {
		t.Fatal(err)
	}
	d.close()
	next, err := startDesktop(context.Background(), desktopOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if next.key != replacement {
		t.Fatal("stale config administrator key selected")
	}
	next.close()
	if err = os.Remove(filepath.Join(dir, "central", "new-api-key.json")); err != nil {
		t.Fatal(err)
	}
	if app, err := startDesktop(context.Background(), desktopOptions{Dir: dir}); err == nil {
		app.close()
		t.Fatal("revoked administrator credential accepted from stale config")
	}
}

func TestDesktopInterruptedApplicationProvisioningResumes(t *testing.T) {
	dir := t.TempDir()
	if err := beginDesktopInitialization(dir); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(dir, desktopInitializationFile))
	if err != nil {
		t.Fatal(err)
	}
	var pending desktopInitialization
	if err = json.Unmarshal(marker, &pending); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	configuration, _, err := localConfiguration(filepath.Join(dir, "central"), port)
	if err != nil {
		t.Fatal(err)
	}
	store, err := audit.Open(configuration.Database)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	partial := &desktopApp{dir: dir, ctx: context.Background(), config: configuration, store: store}
	if err = partial.provisionApplicationAccess(nil); err == nil {
		t.Fatal("closed audit unexpectedly accepted provisioning")
	}
	if _, err = os.Stat(filepath.Join(dir, desktopInitializationFile)); err != nil {
		t.Fatal("failed initialization lost intent")
	}
	app, err := startDesktop(context.Background(), desktopOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer app.close()
	access, err := app.applicationAccess(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if access.Key != pending.Key {
		t.Fatal("retry replaced the pending application identity")
	}
	if _, err = os.Stat(filepath.Join(dir, desktopInitializationFile)); !os.IsNotExist(err) {
		t.Fatal("completed initialization retained pending marker")
	}
}
func TestDesktopStaleInitializationMarkerNeverRestoresRevokedKey(t *testing.T) {
	d := desktopFixture(t, false)
	dir := d.dir
	access, err := d.applicationAccess(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	marker, _ := json.Marshal(desktopInitialization{Version: 1, Key: access.Key})
	keys, err := d.store.LoadKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var keep []config.Key
	for _, key := range keys {
		if key.ID != desktopApplicationKeyID {
			keep = append(keep, key)
		}
	}
	if err = d.gateway.ReplaceKeys(keep, event.Event{Actor: "test-operator"}); err != nil {
		t.Fatal(err)
	}
	d.close()
	if err = os.WriteFile(filepath.Join(dir, desktopInitializationFile), marker, 0600); err != nil {
		t.Fatal(err)
	}
	next, err := startDesktop(context.Background(), desktopOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer next.close()
	if _, err = next.applicationAccess(context.Background()); err == nil {
		t.Fatal("stale initialization marker restored revoked key")
	}
}
