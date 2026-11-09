package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"localaiserver/internal/config"
	"localaiserver/internal/gateway"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type centralCredential struct {
	Key     string `json:"key"`
	BaseURL string `json:"base_url"`
}

func TestCentralInitializationRolesAndCertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Zentrale mit Leerzeichen")
	c, err := centralConfiguration(dir, "central.example.test", "0.0.0.0:8443")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Nodes) != 0 || loaded.TimeoutSeconds != 900 || loaded.MaxBodyBytes != 16<<20 {
		t.Fatal("wrong central profile")
	}
	pair, err := tls.LoadX509KeyPair(c.TLSCert, c.TLSKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if cert.VerifyHostname("central.example.test") != nil || cert.VerifyHostname("127.0.0.1") != nil {
		t.Fatal("certificate host mismatch")
	}
	var app, admin centralCredential
	for name, target := range map[string]*centralCredential{"prozesslog-access.json": &app, "admin-access.json": &admin} {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if json.Unmarshal(raw, target) != nil || target.BaseURL != "https://central.example.test:8443/v1" {
			t.Fatal("wrong access file")
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			t.Fatal("credentials not private")
		}
	}
	if app.Key == admin.Key || len(app.Key) != 67 {
		t.Fatal("roles share credentials")
	}
	for i, key := range []string{admin.Key, app.Key} {
		sum := sha256.Sum256([]byte(key))
		if loaded.Keys[i].SHA256 != hex.EncodeToString(sum[:]) || loaded.Keys[i].Admin != (i == 0) {
			t.Fatal("incorrect key role")
		}
	}
	g, err := gateway.New(loaded, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	handler := g.Admin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	// Admin authorization succeeds without an inference engine; rejected application
	// access needs an auditor, so verify its role in the persisted config above.
	request := httptest.NewRequest("GET", "/admin/overview", nil)
	request.Header.Set("Authorization", "Bearer "+admin.Key)
	request.Header.Set("X-User-ID", "admin-test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatal("admin access not usable")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if _, err := centralConfiguration(dir, "different.example.test", "0.0.0.0:9443"); err == nil {
		t.Fatal("existing installation replaced")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if string(before) != string(after) {
		t.Fatal("configuration mutated")
	}
}

func TestCentralRejectsUnreachableOrMalformedHostsBeforeWriting(t *testing.T) {
	for _, host := range []string{"", "localhost", "127.0.0.1", "::1", "0.0.0.0", "https://central.example", "central.example:8443", "*.example", "bad/path", "name.localhost", "-invalid.example"} {
		dir := filepath.Join(t.TempDir(), "central")
		if _, err := centralConfiguration(dir, host, "0.0.0.0:8443"); err == nil {
			t.Fatalf("accepted %q", host)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("invalid input created files")
		}
	}
	for _, listen := range []string{"127.0.0.1:8443", "0.0.0.0:0", "0.0.0.0:65536", "name:8443"} {
		if _, err := centralConfiguration(filepath.Join(t.TempDir(), "central"), "central.example", listen); err == nil {
			t.Fatalf("accepted %q", listen)
		}
	}
}
