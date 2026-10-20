package main

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPreviewOnlyForwardsAuthenticatedReadRoutes(t *testing.T) {
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-User-ID") != "test-user" {
			t.Error("credentials not forwarded")
		}
		io.WriteString(w, `{"nodes":[],"models":[]}`)
	}))
	defer backend.Close()
	handler, err := previewHandler(backend.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/v1/status", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	request.Header.Set("X-User-ID", "test-user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || calls.Load() != 1 {
		t.Fatal("status read failed")
	}
	for _, path := range []string{"/internal/join", "/internal/renew", "/v1/chat/completions"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{}`)))
		if w.Code < 400 {
			t.Fatalf("write route exposed: %s", path)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("write route reached gateway")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Gemma 4") {
		t.Fatal("UI missing")
	}
	if w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing browser protections")
	}
}
func TestPreviewVerifiesBackendTLS(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	defer backend.Close()
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: backend.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		ca     string
		status int
	}{{"", 503}, {ca, 200}} {
		handler, err := previewHandler(backend.URL, test.ca)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
		if w.Code != test.status {
			t.Fatalf("TLS verification status %d expected %d", w.Code, test.status)
		}
	}
	if _, err := previewHandler("https://remote.example", ""); err == nil {
		t.Fatal("remote preview target accepted")
	}
}
