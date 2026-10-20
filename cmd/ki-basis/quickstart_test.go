package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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
	if file.Mode().Perm()&0077 != 0 {
		t.Fatal("access file is readable by others")
	}
}
