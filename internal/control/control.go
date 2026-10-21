// Package control implements one-time enrollment and certificate-authenticated heartbeats.
package control

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"localaiserver/internal/audit"
	"localaiserver/internal/config"
	"localaiserver/internal/management"
	"localaiserver/internal/pki"
	"localaiserver/internal/pool"
	"localaiserver/internal/tunnel"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const Lease = 20 * time.Second

type JoinRequest struct {
	Token string `json:"token"`
	CSR   string `json:"csr"`
}
type Identity struct {
	Node        config.Node `json:"node"`
	Certificate string      `json:"certificate"`
	CA          string      `json:"ca"`
}
type HeartbeatResponse struct {
	Lease int              `json:"lease_seconds"`
	Plan  *management.Plan `json:"plan,omitempty"`
}
type Heartbeat struct {
	Report *management.Report `json:"report,omitempty"`
	Ready  bool               `json:"ready"`
}
type Service struct {
	gatewayCert *x509.Certificate
	reports     map[string]management.Report
	broker      *tunnel.Broker
	store       *audit.Store
	pool        *pool.Pool
	authority   *pki.Authority
	clientTLS   *tls.Config
	mu          sync.Mutex
	states      map[string]bool
	seen        map[string]time.Time
}

func Fingerprint(der []byte) string { h := sha256.Sum256(der); return hex.EncodeToString(h[:]) }
func TokenHash(token string) string { return Fingerprint([]byte(token)) }
func New(c config.Config, store *audit.Store, p *pool.Pool) (*Service, error) {
	a, err := pki.Load(c.ControlCACert, c.ControlCAKey)
	if err != nil {
		return nil, err
	}
	pair, err := tls.LoadX509KeyPair(c.TLSCert, c.TLSKey)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	roots, err := pki.Roots(a.PEM)
	if err != nil {
		return nil, err
	}
	if cert.Subject.CommonName != pki.GatewayIdentity {
		return nil, errors.New("gateway TLS certificate must use installation gateway identity")
	}
	if _, err = cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return nil, err
	}
	s := &Service{gatewayCert: cert, reports: map[string]management.Report{}, broker: tunnel.New(), store: store, pool: p, authority: a, states: map[string]bool{}, seen: map[string]time.Time{}, clientTLS: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ClientCAs: roots, Certificates: []tls.Certificate{pair}}}
	devices, err := store.Devices(context.Background())
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		if !d.Revoked {
			if err = s.add(d.Node); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// TLSConfig permits API clients without certificates but verifies any presented device certificate.
func (s *Service) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: s.clientTLS.ClientCAs}
}
func (s *Service) add(n config.Node) error {
	tlsConfig := s.clientTLS.Clone()
	tlsConfig.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 || cs.PeerCertificates[0].Subject.CommonName != "node:"+n.ID {
			return errors.New("unexpected device identity")
		}
		return nil
	}
	err := s.pool.AddDynamic(n, tlsConfig, func() bool {
		devices, err := s.store.Devices(context.Background())
		if err != nil {
			return false
		}
		for _, d := range devices {
			if d.Node.ID == n.ID {
				return !d.Revoked
			}
		}
		return false
	})
	if err == nil && n.Connection == "outbound" {
		s.pool.SetTransport(n.ID, s.broker.Transport(n.ID))
	}
	return err
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/join", s.join)
	mux.HandleFunc("POST /internal/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /internal/renew", s.renew)
	mux.HandleFunc("POST /internal/agent-events", s.agentEvent)
	mux.HandleFunc("GET /internal/tasks", s.pollTask)
	mux.HandleFunc("GET /internal/tasks/{id}/cancel", s.watchTask)
	mux.HandleFunc("POST /internal/tasks/{id}/result", s.taskResult)
	return mux
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func deny(w http.ResponseWriter, status int) {
	respond(w, status, map[string]string{"error": "control request rejected"})
}
func (s *Service) issue(csr string, n config.Node) (string, string, error) {
	u, err := url.Parse(n.URL)
	if err != nil {
		return "", "", err
	}
	cert, err := s.authority.Sign([]byte(csr), "node:"+n.ID, u.Hostname(), false)
	if err != nil {
		return "", "", err
	}
	b, _ := pem.Decode(cert)
	return string(cert), Fingerprint(b.Bytes), nil
}
func (s *Service) join(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var req JoinRequest
	if err := decode(w, r, &req); err != nil || len(req.Token) != 67 {
		deny(w, 400)
		return
	}
	n, cert, err := s.store.Join(r.Context(), TokenHash(req.Token), func(n config.Node) (string, string, error) {
		if err := s.pool.CheckDescriptor(n); err != nil {
			return "", "", err
		}
		return s.issue(req.CSR, n)
	})
	if err != nil {
		if !errors.Is(err, audit.ErrCredential) {
			deny(w, 503)
		} else {
			deny(w, 401)
		}
		return
	}
	if err = s.add(n); err != nil {
		deny(w, 503)
		return
	}
	respond(w, 201, Identity{Node: n, Certificate: cert, CA: string(s.authority.PEM)})
}
func (s *Service) authenticate(r *http.Request) (config.Node, string, error) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return config.Node{}, "", audit.ErrCredential
	}
	cert := r.TLS.PeerCertificates[0]
	id := cert.Subject.CommonName
	if len(id) < 6 || id[:5] != "node:" {
		return config.Node{}, "", audit.ErrCredential
	}
	fingerprint := Fingerprint(cert.Raw)
	n, err := s.store.Device(r.Context(), id[5:], fingerprint)
	return n, fingerprint, err
}
func (s *Service) heartbeat(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, _, err := s.authenticate(r)
	if err != nil {
		deny(w, 401)
		return
	}
	var h Heartbeat
	if err = decode(w, r, &h); err != nil {
		deny(w, 400)
		return
	}
	if h.Report != nil {
		if !validReport(*h.Report) {
			deny(w, 400)
			return
		}
		old := s.reports[n.ID]
		if old.State != h.Report.State || old.Generation != h.Report.Generation || old.Error != h.Report.Error {
			if s.store.Append(r.Context(), audit.Event{Type: "model.state_changed", NodeID: n.ID, Actor: "device:" + n.ID, Outcome: h.Report.State, Revision: h.Report.Generation}) != nil {
				deny(w, 503)
				return
			}
		}
		s.reports[n.ID] = *h.Report
	}
	if n.Connection == "outbound" {
		h.Ready = h.Ready && s.broker.Online(n.ID)
	}
	previous, known := s.states[n.ID]
	if !known || previous != h.Ready {
		outcome := "unready"
		if h.Ready {
			outcome = "ready"
		}
		if err = s.store.Append(r.Context(), audit.Event{Type: "node.readiness_changed", NodeID: n.ID, Actor: "device:" + n.ID, Outcome: outcome}); err != nil {
			deny(w, 503)
			return
		}
		s.states[n.ID] = h.Ready
	}
	s.seen[n.ID] = time.Now()
	s.pool.Heartbeat(n.ID, h.Ready, Lease)
	plan, _ := s.store.Plan(r.Context(), n.ID)
	respond(w, 200, HeartbeatResponse{Lease: int(Lease / time.Second), Plan: plan})
}
func (s *Service) renew(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, old, err := s.authenticate(r)
	if err != nil {
		deny(w, 401)
		return
	}
	var req struct {
		CSR string `json:"csr"`
	}
	if err = decode(w, r, &req); err != nil {
		deny(w, 400)
		return
	}
	cert, fingerprint, err := s.issue(req.CSR, n)
	if err != nil {
		deny(w, 400)
		return
	}
	if err = s.store.Renew(r.Context(), n.ID, old, fingerprint); err != nil {
		deny(w, 503)
		return
	}
	respond(w, 200, Identity{Node: n, Certificate: cert, CA: string(s.authority.PEM)})
}

// Monitor records lease loss; routing independently enforces the deadline on every selection.
func (s *Service) Monitor(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			s.mu.Lock()
			for id, last := range s.seen {
				if now.Sub(last) > Lease && s.states[id] {
					auditCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
					err := s.store.Append(auditCtx, audit.Event{Type: "node.lease_expired", NodeID: id, Actor: "gateway", Outcome: "unavailable"})
					cancel()
					if err == nil {
						s.states[id] = false
					}
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Service) Close() { s.broker.Close() }
func (s *Service) pollTask(w http.ResponseWriter, r *http.Request) {
	n, _, err := s.authenticate(r)
	if err != nil || n.Connection != "outbound" {
		deny(w, 401)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), tunnel.PollTimeout)
	defer cancel()
	task, err := s.broker.Poll(ctx, n.ID)
	if err != nil {
		w.WriteHeader(204)
		return
	}
	respond(w, 200, task)
}
func (s *Service) watchTask(w http.ResponseWriter, r *http.Request) {
	n, _, err := s.authenticate(r)
	if err != nil {
		deny(w, 401)
		return
	}
	ctx, ok := s.broker.Context(n.ID, r.PathValue("id"))
	if !ok {
		w.WriteHeader(410)
		return
	}
	select {
	case <-ctx.Done():
		w.WriteHeader(410)
	case <-r.Context().Done():
	case <-time.After(tunnel.PollTimeout):
		w.WriteHeader(204)
	}
}
func (s *Service) taskResult(w http.ResponseWriter, r *http.Request) {
	n, _, err := s.authenticate(r)
	if err != nil {
		deny(w, 401)
		return
	}
	jobContext, ok := s.broker.Context(n.ID, r.PathValue("id"))
	if !ok {
		w.WriteHeader(410)
		return
	}
	// Bound slow uploads by the inference deadline and interrupt reads on client cancellation.
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(time.Hour)
	if d, ok := jobContext.Deadline(); ok {
		deadline = d
	}
	_ = rc.SetReadDeadline(deadline)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-jobContext.Done():
			_ = rc.SetReadDeadline(time.Now())
		case <-finished:
		}
	}()
	status, err := strconv.Atoi(r.Header.Get("X-Inference-Status"))
	if err != nil || status < 200 || status > 599 {
		deny(w, 400)
		return
	}
	if err = s.broker.Deliver(r.Context(), n.ID, r.PathValue("id"), status, r.Header.Get("Content-Type"), r.Body); err != nil {
		w.WriteHeader(410)
		return
	}
	w.WriteHeader(204)
}

func (s *Service) agentEvent(w http.ResponseWriter, r *http.Request) {
	n, _, err := s.authenticate(r)
	if err != nil {
		deny(w, 401)
		return
	}
	var in struct {
		Action string `json:"action"`
	}
	allowed := map[string]bool{"settings_change_requested": true, "settings_changed": true, "autostart_enable_requested": true, "autostart_disable_requested": true, "autostart_enabled": true, "autostart_disabled": true}
	if decode(w, r, &in) != nil || !allowed[in.Action] {
		deny(w, 400)
		return
	}
	if s.store.Append(r.Context(), audit.Event{Type: "agent." + in.Action, NodeID: n.ID, Actor: "device:" + n.ID}) != nil {
		deny(w, 503)
		return
	}
	respond(w, 200, map[string]bool{"recorded": true})
}
