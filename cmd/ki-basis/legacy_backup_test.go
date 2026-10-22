package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func legacyCentralFixture(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "server-01.tar.gz")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	archive := tar.NewWriter(gz)
	names := []string{"config.json", "gateway.db", "pki/ca.crt", "pki/ca.key", "pki/gateway.crt", "pki/gateway.key"}
	for _, name := range append(names, "../../escape") {
		data := []byte("test-payload")
		if err = archive.WriteHeader(&tar.Header{Name: "workspace/ki-daten/" + name, Mode: 0600, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		archive.Write(data)
	}
	archive.WriteHeader(&tar.Header{Name: "root/.ssh/gateway_tunnel", Mode: 0600, Size: 6})
	archive.Write([]byte("SECRET"))
	archive.Close()
	gz.Close()
	f.Close()
	return file
}
func TestPilotMigrationOnlyImportsPortableCentralData(t *testing.T) {
	source := legacyCentralFixture(t)
	dest := t.TempDir()
	if kind, err := unpackBackup(source, dest); err != nil || kind != "ki-basis-central-backup" {
		t.Fatalf("migration failed: %s %v", kind, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "root/.ssh/gateway_tunnel")); !os.IsNotExist(err) {
		t.Fatal("host private key imported")
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 4 {
		t.Fatalf("unexpected extracted entries: %v", entries)
	}
}
func TestPilotMigrationRejectsCorruptGzip(t *testing.T) {
	source := legacyCentralFixture(t)
	raw, _ := os.ReadFile(source)
	raw[len(raw)-8] ^= 0xff
	os.WriteFile(source, raw, 0600)
	if _, err := unpackBackup(source, t.TempDir()); err == nil {
		t.Fatal("corrupt gzip accepted")
	}
}
