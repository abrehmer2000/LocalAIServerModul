package logmeta

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBoundedRotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "requests.log")
	w, err := Open(p, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err = w.Write([]byte("12345678\n")); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Fatal("unbounded log files", len(entries))
	}
	for _, e := range entries {
		info, _ := e.Info()
		if info.Size() > 10 {
			t.Fatal("log exceeds bound")
		}
	}
}
