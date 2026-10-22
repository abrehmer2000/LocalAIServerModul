package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLocalPreviewPersistsItsIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Mac Preview")
	first, key, err := localConfiguration(dir, 8443)
	if err != nil {
		t.Fatal(err)
	}
	second, reused, err := localConfiguration(dir, 8443)
	if err != nil {
		t.Fatal(err)
	}
	if key != reused || first.Database != second.Database {
		t.Fatal("local identity was replaced")
	}
	h := sha256.Sum256([]byte(key))
	if first.Keys[0].SHA256 != hex.EncodeToString(h[:]) {
		t.Fatal("configured key hash mismatch")
	}
	if len(first.Nodes) != 0 {
		t.Fatal("preview fabricated installed models")
	}
	file, err := os.Stat(filepath.Join(dir, "access.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && file.Mode().Perm()&0077 != 0 {
		t.Fatal("access file is readable by others")
	}
}

func TestQuickstartNeverResurrectsRotatedKey(t *testing.T) {
	dir := t.TempDir()
	cfg, original, err := localConfiguration(dir, 8443)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Keys[0].SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cfg.Keys[0].Admin = false
	data, _ := json.Marshal(cfg)
	if err = os.WriteFile(filepath.Join(dir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	reused, key, err := localConfiguration(dir, 8443)
	if err != nil {
		t.Fatal(err)
	}
	if key == original || key != "" || reused.Keys[0].SHA256 != cfg.Keys[0].SHA256 || reused.Keys[0].Admin {
		t.Fatal("rotated key restored or elevated")
	}
}
