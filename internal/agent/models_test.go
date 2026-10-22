package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"localaiserver/internal/catalog"
	"localaiserver/internal/management"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestResumedDownloadVerifiedAndCorruptionRejected(t *testing.T) {
	body := []byte("test model fixture, not model weights")
	hash := sha256.Sum256(body)
	spec := management.Model{ID: catalog.DefaultAlias, Repository: catalog.Default().Repository, Filename: catalog.ModelFile, Commit: strings.Repeat("a", 40), SHA256: hex.EncodeToString(hash[:]), Size: int64(len(body))}
	dir := t.TempDir()
	path := filepath.Join(dir, spec.SHA256+".gguf")
	if e := os.WriteFile(path+".part", body[:5], 0600); e != nil {
		t.Fatal(e)
	}
	m := NewModelManager(dir, "", "http://127.0.0.1:8082", spec.ID, 1)
	m.report.Generation = "one"
	m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Range") != "bytes=5-" {
			t.Error("missing resume")
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": []string{fmt.Sprintf("bytes 5-%d/%d", len(body)-1, len(body))}}, Body: io.NopCloser(bytes.NewReader(body[5:]))}, nil
	})}
	p := management.Plan{Generation: "one", Action: "download", Model: spec}
	if err := m.download(context.Background(), p, path); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, body) {
		t.Fatal("download mismatch")
	}
	if _, e := os.Stat(path + ".part"); !os.IsNotExist(e) {
		t.Fatal("partial file not atomically activated")
	}
	corrupt := bytes.Repeat([]byte("x"), len(body))
	os.WriteFile(path, corrupt, 0600)
	if err := m.download(context.Background(), p, path); err == nil || err.Error() != "hash_mismatch" {
		t.Fatal("corrupt installed model accepted")
	}
	os.Remove(path)
	os.WriteFile(path+".part", corrupt, 0600)
	if err := m.download(context.Background(), p, path); err == nil || err.Error() != "hash_mismatch" {
		t.Fatal("corrupt partial model accepted")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("corrupt model activated")
	}
	if _, e := os.Stat(path + ".part"); !os.IsNotExist(e) {
		t.Fatal("corrupt partial retained")
	}
}
func TestRejectArbitraryModelSourcesAndRedirects(t *testing.T) {
	m := NewModelManager(t.TempDir(), "", "http://127.0.0.1:8082", catalog.DefaultAlias, 1)
	req, _ := http.NewRequest("GET", "http://127.0.0.1/metadata", nil)
	if m.client.CheckRedirect(req, nil) == nil {
		t.Fatal("unsafe redirect allowed")
	}
	spec := management.Model{ID: catalog.DefaultAlias, Repository: "attacker/model", Filename: "../../escape"}
	m.report.Generation = "bad"
	m.execute(context.Background(), management.Plan{Generation: "bad", Action: "download", Model: spec})
	if m.Report().Error != "invalid_model" {
		t.Fatal("unapproved model accepted")
	}
}

func TestExistingModelIsVerifiedAndReusedWithoutCopy(t *testing.T) {
	body := []byte("already downloaded model fixture")
	hash := sha256.Sum256(body)
	root := t.TempDir()
	existing := filepath.Join(root, "operator-model.gguf")
	if err := os.WriteFile(existing, body, 0600); err != nil {
		t.Fatal(err)
	}
	spec := management.Model{ID: catalog.DefaultAlias, Repository: catalog.Default().Repository, Filename: catalog.ModelFile, Commit: strings.Repeat("a", 40), SHA256: hex.EncodeToString(hash[:]), Size: int64(len(body))}
	dir := filepath.Join(root, "managed-models")
	m := NewModelManager(dir, "", "http://127.0.0.1:8082", spec.ID, 1)
	m.ExistingModel = existing
	m.report.Generation = "reuse"
	m.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("existing model downloaded again")
		return nil, fmt.Errorf("unexpected download")
	})}
	p := management.Plan{Generation: "reuse", Action: "download", Model: spec}
	m.execute(context.Background(), p)
	if m.Report().State != "downloaded" {
		t.Fatal(m.Report())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("existing model was duplicated")
	}
	bad := bytes.Repeat([]byte("x"), len(body))
	os.WriteFile(existing, bad, 0600)
	m.execute(context.Background(), p)
	if m.Report().Error != "hash_mismatch" {
		t.Fatal("corrupt imported model accepted")
	}
	actual, err := os.ReadFile(existing)
	if err != nil || !bytes.Equal(actual, bad) {
		t.Fatal("operator-owned file changed")
	}
}
