package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"localaiserver/internal/control"
)

func runtimeZip(t *testing.T, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.zip")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	h := &zip.FileHeader{Name: name}
	h.SetMode(mode)
	w, e := z.CreateHeader(h)
	if e != nil {
		t.Fatal(e)
	}
	w.Write([]byte("runtime"))
	z.Close()
	f.Close()
	return path
}
func TestRuntimeArchiveRejectsTraversalAndLinks(t *testing.T) {
	for _, name := range []string{"../llama-server", "/llama-server", "C:/llama-server", "..\\llama-server"} {
		if _, e := extractRuntime(runtimeZip(t, name, 0700), t.TempDir()); e == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, e := extractRuntime(runtimeZip(t, "llama-server", os.ModeSymlink|0700), t.TempDir()); e == nil {
		t.Fatal("ZIP symlink accepted")
	}
	dir := t.TempDir()
	path, e := extractRuntime(runtimeZip(t, "release/bin/llama-server", 0700), dir)
	if e != nil || path != filepath.Join(dir, "release", "bin", "llama-server") {
		t.Fatalf("valid runtime: %s %v", path, e)
	}
}
func TestRuntimeTarSafelyCopiesRelativeLibraryLinks(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range []*tar.Header{{Name: "bin/lib.so", Linkname: "lib.so.1", Typeflag: tar.TypeSymlink}, {Name: "bin/lib.so.1", Size: 3, Mode: 0700}, {Name: "bin/llama-server", Size: 3, Mode: 0700}} {
		tw.WriteHeader(h)
		if h.Size > 0 {
			tw.Write([]byte("abc"))
		}
	}
	tw.Close()
	gz.Close()
	path := filepath.Join(t.TempDir(), "runtime.tar.gz")
	os.WriteFile(path, buf.Bytes(), 0600)
	dir := t.TempDir()
	if _, e := extractRuntime(path, dir); e != nil {
		t.Fatal(e)
	}
	info, e := os.Lstat(filepath.Join(dir, "bin", "lib.so"))
	if e != nil || !info.Mode().IsRegular() {
		t.Fatalf("link wasn't materialized as a regular file: %v", e)
	}
}
func TestSSHTargetCannotInjectOptionsOrShell(t *testing.T) {
	good := sshTarget{Host: "server.example", Port: 22, User: "operator"}
	if !good.valid() {
		t.Fatal("valid target refused")
	}
	for _, host := range []string{"-oProxyCommand=bad", "server;touch /tmp/no", "x\nHost evil", "root@host", "$(whoami)"} {
		bad := good
		bad.Host = host
		if bad.valid() {
			t.Fatalf("accepted %q", host)
		}
	}
	for _, user := range []string{"root;id", "-o", "root@host", "root\n"} {
		bad := good
		bad.User = user
		if bad.valid() {
			t.Fatalf("accepted %q", user)
		}
	}
}
func TestPreflightRejectsExpiredGrantAndOccupiedPort(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	s := agentSettings{Models: t.TempDir(), Engine: "http://" + listener.Addr().String()}
	b := control.Bundle{Version: 1, Expires: time.Now().Add(-time.Minute).Format(time.RFC3339)}
	report := inspectSetup(context.Background(), s, &b, false)
	if report.Ready {
		t.Fatal("invalid setup accepted")
	}
	codes := map[string]bool{}
	for _, c := range report.Checks {
		if c.State == "error" {
			codes[c.ID] = true
		}
	}
	if !codes["runtime"] || !codes["port"] || !codes["connection"] {
		t.Fatalf("missing failures: %+v", report)
	}
}
func TestBackupUnpackRequiresManifestAndRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../agent.json", "agent.json"} {
		if _, e := unpackBackup(runtimeZip(t, name, 0600), t.TempDir()); e == nil {
			t.Fatal("invalid backup accepted")
		}
	}
	// An invalid or incomplete backup must not overwrite an existing installation.
	dest := t.TempDir()
	sentinel := filepath.Join(dest, "keep")
	os.WriteFile(sentinel, []byte("keep"), 0600)
	e := restoreCommand([]string{"--archive", runtimeZip(t, "manifest.json", 0600), "--dir", dest})
	if e == nil {
		t.Fatal("nonempty destination overwritten")
	}
	raw, _ := os.ReadFile(sentinel)
	if string(raw) != "keep" {
		t.Fatal("existing data changed")
	}
}
func TestResolveSettingsPreservesDetectedDefaults(t *testing.T) {
	defaults := agentSettings{Engine: "http://127.0.0.1:8082", Models: "/models", Binary: "/bin/llama-server"}
	raw, _ := json.Marshal(resolveSettings(agentSettings{}, defaults))
	if !bytes.Contains(raw, []byte("/bin/llama-server")) {
		t.Fatal("defaults lost")
	}
}
