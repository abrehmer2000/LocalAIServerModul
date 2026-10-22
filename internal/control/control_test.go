package control_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"localaiserver/internal/agent"
	"localaiserver/internal/audit"
	"localaiserver/internal/config"
	"localaiserver/internal/control"
	"localaiserver/internal/gateway"
	"localaiserver/internal/pki"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const key = "integration-key-only"

type fixture struct {
	t       *testing.T
	dir     string
	c       config.Config
	store   *audit.Store
	g       *gateway.Gateway
	control *control.Service
	server  *httptest.Server
	client  *http.Client
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	pkiDir := filepath.Join(dir, "pki")
	if err := pki.Init(pkiDir, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(key))
	c := config.Config{Listen: "127.0.0.1:1", Database: filepath.Join(dir, "audit.db"), TLSCert: filepath.Join(pkiDir, "gateway.crt"), TLSKey: filepath.Join(pkiDir, "gateway.key"), ControlCACert: filepath.Join(pkiDir, "ca.crt"), ControlCAKey: filepath.Join(pkiDir, "ca.key"), Keys: []config.Key{{ID: "app", SHA256: hex.EncodeToString(h[:])}}, TimeoutSeconds: 10, MaxBodyBytes: 4096, MaxOutputTokens: 128}
	store, err := audit.Open(c.Database)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, dir: dir, c: c, store: store}
	f.start()
	t.Cleanup(func() { f.server.Close(); f.g.Close(); f.control.Close(); store.Close() })
	return f
}
func (f *fixture) start() {
	var err error
	f.g, err = gateway.New(f.c, f.store)
	if err != nil {
		f.t.Fatal(err)
	}
	f.control, err = control.New(f.c, f.store, f.g.Pool())
	if err != nil {
		f.t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/internal/", f.control.Handler())
	mux.Handle("/admin/", f.g.Admin(f.control.AdminHandler()))
	mux.Handle("/", f.g.Handler())
	f.server = httptest.NewUnstartedServer(mux)
	tc := f.control.TLSConfig()
	pair, err := tls.LoadX509KeyPair(f.c.TLSCert, f.c.TLSKey)
	if err != nil {
		f.t.Fatal(err)
	}
	tc.Certificates = []tls.Certificate{pair}
	f.server.TLS = tc
	f.server.StartTLS()
	data, _ := os.ReadFile(f.c.ControlCACert)
	roots, _ := pki.Roots(data)
	f.client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}, Timeout: 5 * time.Second}
}
func (f *fixture) grant(n config.Node, token string, expires time.Time) {
	f.t.Helper()
	if err := f.store.IssueJoin(context.Background(), control.TokenHash(token), n, expires, "local-os:test"); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) chat(status int) {
	f.t.Helper()
	body := `{"model":"chat","messages":[{"role":"user","content":"private prompt"}],"stream":true}`
	req, _ := http.NewRequest("POST", f.server.URL+"/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-User-ID", "person-01")
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		f.t.Fatalf("status %d, expected %d: %s", resp.StatusCode, status, data)
	}
	if status == 200 && !bytes.Contains(data, []byte("[DONE]")) {
		f.t.Fatalf("incomplete stream: %s", data)
	}
}
func TestEnrollmentTLSReadinessRevocationAndRestart(t *testing.T) {
	f := setup(t)
	// The agent knows its externally reachable address before enrollment.
	listener := httptest.NewUnstartedServer(nil)
	n := config.Node{ID: "node-01", URL: "https://" + listener.Listener.Addr().String(), Model: "chat", UpstreamModel: "local-model", Revision: "r1", Capacity: 1}
	token := "jt_" + string(bytes.Repeat([]byte("a"), 64))
	f.grant(n, token, time.Now().Add(time.Minute))
	statePath := filepath.Join(f.dir, "identity.json")
	if err := agent.Join(context.Background(), f.server.URL, token, f.c.ControlCACert, statePath); err != nil {
		t.Fatal(err)
	}
	if err := agent.Join(context.Background(), f.server.URL, token, f.c.ControlCACert, filepath.Join(f.dir, "replay.json")); err == nil {
		t.Fatal("token replay accepted")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "replay.json")); !os.IsNotExist(err) {
		t.Fatal("failed join wrote state")
	}
	f.chat(503)
	ready := true
	var mu sync.Mutex
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			mu.Lock()
			available := ready
			mu.Unlock()
			if !available {
				w.WriteHeader(503)
			}
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"local-model"}]}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer engine.Close()
	runner, err := agent.New(statePath, engine.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	tc, err := runner.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	listener.Config.Handler = runner.Handler()
	state, err := agent.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	devicePair, err := tls.X509KeyPair([]byte(state.Identity.Certificate), []byte(state.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	tc.Certificates = []tls.Certificate{devicePair}
	listener.TLS = tc
	listener.StartTLS()
	defer listener.Close()
	if !runner.Ready(context.Background()) {
		t.Fatal("engine not ready")
	}
	if err = runner.Heartbeat(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	f.chat(200)
	// Clients without device credentials must never be accepted by the agent.
	untrusted := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.client.Transport.(*http.Transport).TLSClientConfig.RootCAs}}, Timeout: time.Second}
	if resp, err := untrusted.Post(n.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{}`)); err == nil {
		resp.Body.Close()
		t.Fatal("client without mTLS accepted")
	}
	// A node certificate from the same CA is still not the gateway identity.
	authority, err := pki.Load(f.c.ControlCACert, f.c.ControlCAKey)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, otherCSR, err := pki.CSR()
	if err != nil {
		t.Fatal(err)
	}
	otherCert, err := authority.Sign(otherCSR, "node:other", "127.0.0.1", false)
	if err != nil {
		t.Fatal(err)
	}
	otherPair, err := tls.X509KeyPair(otherCert, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	otherClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.client.Transport.(*http.Transport).TLSClientConfig.RootCAs, Certificates: []tls.Certificate{otherPair}}}, Timeout: time.Second}
	if response, err := otherClient.Post(n.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{}`)); err == nil {
		response.Body.Close()
		t.Fatal("node impersonated gateway")
	}
	mu.Lock()
	ready = false
	mu.Unlock()
	if runner.Ready(context.Background()) {
		t.Fatal("unhealthy engine reported ready")
	}
	if err = runner.Heartbeat(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	f.chat(503)
	mu.Lock()
	ready = true
	mu.Unlock()
	if err = runner.Heartbeat(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	// Persisted devices survive gateway object restart and start unavailable until heartbeat.
	f.g.Close()
	f.g, err = gateway.New(f.c, f.store)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := control.New(f.c, f.store, f.g.Pool())
	if err != nil {
		t.Fatal(err)
	}
	if chosen, _ := f.g.Pool().Acquire("chat"); chosen != nil {
		t.Fatal("persisted node ready without heartbeat")
	}
	_ = fresh
	if err = f.store.Revoke(context.Background(), n.ID, "local-os:test"); err != nil {
		t.Fatal(err)
	}
	f.chat(503)
	if err = runner.Heartbeat(context.Background(), true); err == nil {
		t.Fatal("revoked device accepted")
	}
	// The old service's pool also consults revocation before selecting a node.
	if err = f.store.Revoke(context.Background(), n.ID, "local-os:test"); err == nil {
		t.Fatal("duplicate revoke unexpectedly succeeded")
	}
	var events []audit.Event
	head, err := f.store.Verify(context.Background(), func(r audit.Record) error { events = append(events, r.Event); return nil })
	if err != nil || head.Sequence == 0 {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(events)
	for _, secret := range []string{token, key, "private prompt"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("audit leaked %q", secret)
		}
	}
}
func TestExpiredGrantAndConcurrentSingleUse(t *testing.T) {
	f := setup(t)
	n := config.Node{ID: "expired", URL: "https://127.0.0.1:9999", Model: "chat", UpstreamModel: "local", Revision: "r1", Capacity: 1}
	token := "jt_" + string(bytes.Repeat([]byte("b"), 64))
	f.grant(n, token, time.Now().Add(-time.Second))
	if err := agent.Join(context.Background(), f.server.URL, token, f.c.ControlCACert, filepath.Join(f.dir, "expired.json")); err == nil {
		t.Fatal("expired token accepted")
	}
	n.ID = "race"
	token = "jt_" + string(bytes.Repeat([]byte("c"), 64))
	f.grant(n, token, time.Now().Add(time.Minute))
	var wg sync.WaitGroup
	success := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			success <- agent.Join(context.Background(), f.server.URL, token, f.c.ControlCACert, filepath.Join(f.dir, fmt.Sprintf("race-%d.json", i))) == nil
		}(i)
	}
	wg.Wait()
	close(success)
	count := 0
	for ok := range success {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("single-use race: %d succeeded", count)
	}
}

func TestAutomaticCertificateRenewal(t *testing.T) {
	f := setup(t)
	n := config.Node{ID: "renew", URL: "https://127.0.0.1:9998", Model: "chat", UpstreamModel: "local", Revision: "r1", Capacity: 1}
	token := "jt_" + string(bytes.Repeat([]byte("d"), 64))
	f.grant(n, token, time.Now().Add(time.Minute))
	path := filepath.Join(f.dir, "renew.json")
	if err := agent.Join(context.Background(), f.server.URL, token, f.c.ControlCACert, path); err != nil {
		t.Fatal(err)
	}
	state, err := agent.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair([]byte(state.Identity.Certificate), []byte(state.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	oldFingerprint := control.Fingerprint(cert.Raw)
	ca, err := pki.Load(f.c.ControlCACert, f.c.ControlCAKey)
	if err != nil {
		t.Fatal(err)
	}
	cert.NotAfter = time.Now().Add(20 * 24 * time.Hour)
	der, err := x509.CreateCertificate(rand.Reader, cert, ca.Cert, cert.PublicKey, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.Renew(context.Background(), n.ID, oldFingerprint, control.Fingerprint(der)); err != nil {
		t.Fatal(err)
	}
	state.Identity.Certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	data, _ := json.Marshal(state)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := agent.New(path, "http://127.0.0.1:8081")
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	if err = runner.Heartbeat(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	renewed, err := agent.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(renewed.Identity.Certificate))
	newCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(newCert.NotAfter) < 80*24*time.Hour || bytes.Equal(der, newCert.Raw) {
		t.Fatal("certificate not renewed")
	}
	if err = runner.Heartbeat(context.Background(), false); err != nil {
		t.Fatalf("renewed identity unusable: %v", err)
	}
}
