// Package agent enrolls a device and exposes a local engine only to the authenticated gateway.
package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"localaiserver/internal/config"
	"localaiserver/internal/control"
	"localaiserver/internal/pki"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type State struct {
	Gateway    string           `json:"gateway"`
	Identity   control.Identity `json:"identity"`
	PrivateKey string           `json:"private_key"`
}

func client(ca []byte, pair *tls.Certificate) (*http.Client, error) {
	roots, err := pki.Roots(ca)
	if err != nil {
		return nil, err
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	if pair != nil {
		tc.Certificates = []tls.Certificate{*pair}
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: tc, Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func post(ctx context.Context, c *http.Client, endpoint string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return errors.New("gateway control connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gateway rejected control request (HTTP %d)", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(out)
}
func gatewayURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("gateway must be HTTPS origin without path or credentials")
	}
	return nil
}
func Join(ctx context.Context, gateway, token, caFile, path string) error {
	if err := gatewayURL(gateway); err != nil {
		return err
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return errors.New("state already exists or cannot be inspected")
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return err
	}
	c, err := client(ca, nil)
	if err != nil {
		return err
	}
	defer c.CloseIdleConnections()
	key, csr, err := pki.CSR()
	if err != nil {
		return err
	}
	var identity control.Identity
	if err = post(ctx, c, gateway+"/internal/join", control.JoinRequest{Token: token, CSR: string(csr)}, &identity); err != nil {
		return err
	}
	state := State{Gateway: gateway, Identity: identity, PrivateKey: string(key)}
	if _, err = validate(state); err != nil {
		return err
	}
	// Reserve the final path exclusively so concurrent joins never overwrite an identity.
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func Load(path string) (State, error) {
	var s State
	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 64<<10))
	d.DisallowUnknownFields()
	if err = d.Decode(&s); err != nil {
		return s, err
	}
	_, err = validate(s)
	return s, err
}
func validate(s State) (tls.Certificate, error) {
	if err := gatewayURL(s.Gateway); err != nil {
		return tls.Certificate{}, err
	}
	if err := config.ValidateNode(s.Identity.Node); err != nil {
		return tls.Certificate{}, err
	}
	pair, err := tls.X509KeyPair([]byte(s.Identity.Certificate), []byte(s.PrivateKey))
	if err != nil {
		return pair, errors.New("invalid device credentials")
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return pair, err
	}
	roots, err := pki.Roots([]byte(s.Identity.CA))
	if err != nil {
		return pair, err
	}
	u, _ := url.Parse(s.Identity.Node.URL)
	if cert.Subject.CommonName != "node:"+s.Identity.Node.ID {
		return pair, errors.New("invalid device certificate identity")
	}
	_, err = cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: u.Hostname(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return pair, err
}

type Runner struct {
	Manager   *ModelManager
	mu        sync.RWMutex
	state     State
	pair      tls.Certificate
	path      string
	upstream  *url.URL
	transport *http.Transport
}

func New(path, engineURL string) (*Runner, error) {
	s, err := Load(path)
	if err != nil {
		return nil, err
	}
	pair, err := validate(s)
	if err != nil {
		return nil, err
	}
	if err := ValidateEngineURL(engineURL); err != nil {
		return nil, err
	}
	u, _ := url.Parse(engineURL)
	return &Runner{state: s, pair: pair, path: path, upstream: u, transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 30 * time.Second, MaxConnsPerHost: s.Identity.Node.Capacity + 2, IdleConnTimeout: 30 * time.Second}}, nil
}
func (r *Runner) Close() {
	if r.Manager != nil {
		r.Manager.Close()
	}
	r.transport.CloseIdleConnections()
}
func (r *Runner) TLSConfig() (*tls.Config, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	roots, err := pki.Roots([]byte(r.state.Identity.CA))
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		r.mu.RLock()
		defer r.mu.RUnlock()
		pair := r.pair
		return &pair, nil
	}, VerifyConnection: func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 || cs.PeerCertificates[0].Subject.CommonName != pki.GatewayIdentity {
			return errors.New("gateway identity required")
		}
		return nil
	}}, nil
}
func (r *Runner) Handler() http.Handler {
	proxy := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(r.upstream)
		pr.Out.Host = r.upstream.Host
		pr.Out.Header.Del("Authorization")
		pr.Out.Header.Del("X-User-ID")
		pr.Out.Header.Del("Forwarded")
		pr.Out.Header.Del("X-Forwarded-For")
		pr.Out.Header.Del("X-Forwarded-Host")
		pr.Out.Header.Del("X-Forwarded-Proto")
	}, Transport: r.transport, FlushInterval: -1, ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
		http.Error(w, "local engine unavailable", 502)
	}}
	mux := http.NewServeMux()
	mux.Handle("POST /v1/chat/completions", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, 16<<20)
		proxy.ServeHTTP(w, req)
	}))
	return mux
}

// Ready checks both engine health and the configured model alias; process liveness alone is insufficient.
func (r *Runner) Ready(ctx context.Context) bool {
	c := &http.Client{Transport: r.transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", r.upstream.String()+"/health", nil)
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	req, _ = http.NewRequestWithContext(ctx, "GET", r.upstream.String()+"/v1/models", nil)
	resp, err = c.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&models) != nil {
		return false
	}
	r.mu.RLock()
	expected := r.state.Identity.Node.UpstreamModel
	r.mu.RUnlock()
	for _, m := range models.Data {
		if m.ID == expected {
			return true
		}
	}
	return false
}
func (r *Runner) Heartbeat(ctx context.Context, ready bool) error {
	r.mu.RLock()
	state, pair := r.state, r.pair
	r.mu.RUnlock()
	c, err := client([]byte(state.Identity.CA), &pair)
	if err != nil {
		return err
	}
	defer c.CloseIdleConnections()
	var response control.HeartbeatResponse
	heartbeat := control.Heartbeat{Ready: ready}
	if r.Manager != nil {
		report := r.Manager.Report()
		heartbeat.Report = &report
		if report.State == "stopped" || report.State == "error" {
			heartbeat.Ready = false
		}
	}
	if err = post(ctx, c, state.Gateway+"/internal/heartbeat", heartbeat, &response); err != nil {
		return err
	}
	if r.Manager != nil {
		r.Manager.Apply(ctx, response.Plan)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if time.Until(cert.NotAfter) > 30*24*time.Hour {
		return nil
	}
	key, csr, err := pki.CSR()
	if err != nil {
		return err
	}
	var identity control.Identity
	if err = post(ctx, c, state.Gateway+"/internal/renew", map[string]string{"csr": string(csr)}, &identity); err != nil {
		return err
	}
	renewed := State{Gateway: state.Gateway, Identity: identity, PrivateKey: string(key)}
	newPair, err := validate(renewed)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(renewed, "", "  ")
	temp, err := os.CreateTemp(filepath.Dir(r.path), ".identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(data); err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(temp.Name(), r.path); err != nil {
		return err
	}
	r.mu.Lock()
	r.state = renewed
	r.pair = newPair
	r.mu.Unlock()
	return nil
}
func (r *Runner) RunHeartbeats(ctx context.Context, onError func(error)) {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		if err := r.Heartbeat(ctx, r.Ready(ctx)); err != nil && ctx.Err() == nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func ValidateEngineURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || !config.IsLoopback(u.Hostname()) || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("engine must be an HTTP loopback IP origin with port")
	}
	return nil
}
