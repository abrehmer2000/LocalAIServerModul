// Package pool selects a model replica without exceeding its configured capacity.
package pool

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"localaiserver/internal/config"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Node struct {
	Config      config.Node
	Client      *http.Client
	active      int
	failedUntil time.Time
	dynamic     bool
	ready       bool
	leaseUntil  time.Time
	authorized  func() bool
}
type Pool struct {
	mu     sync.Mutex
	nodes  []*Node
	cursor int
}

func New(configs []config.Node) (*Pool, error) {
	p := &Pool{}
	for _, c := range configs {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if c.CAFile != "" {
			pem, err := os.ReadFile(c.CAFile)
			if err != nil {
				return nil, err
			}
			roots, err := x509.SystemCertPool()
			if err != nil {
				roots = x509.NewCertPool()
			}
			if !roots.AppendCertsFromPEM(pem) {
				return nil, errors.New("invalid node CA file")
			}
			tlsConfig.RootCAs = roots
		}
		transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: tlsConfig, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConnsPerHost: c.Capacity, MaxConnsPerHost: c.Capacity, IdleConnTimeout: 60 * time.Second, DisableCompression: true}
		p.nodes = append(p.nodes, &Node{Config: c, Client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}})
	}
	return p, nil
}
func (p *Pool) Has(model string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.nodes {
		if n.Config.Model == model {
			return true
		}
	}
	return false
}
func (p *Pool) Models() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, n := range p.nodes {
		if !seen[n.Config.Model] {
			out = append(out, n.Config.Model)
			seen[n.Config.Model] = true
		}
	}
	return out
}
func (p *Pool) Acquire(model string) (*Node, func(bool)) {
	return p.acquire(model, "")
}

// AcquireID reserves a specific server for a setup test with the same capacity,
// revocation and health checks used for normal inference.
func (p *Pool) AcquireID(id string) (*Node, func(bool)) {
	return p.acquire("", id)
}
func (p *Pool) acquire(model, id string) (*Node, func(bool)) {
	p.mu.Lock()
	snapshot := append([]*Node(nil), p.nodes...)
	p.mu.Unlock()
	allowed := map[*Node]bool{}
	for _, n := range snapshot {
		if n.dynamic {
			allowed[n] = n.authorized()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	best := -1
	for i := 0; i < len(p.nodes); i++ {
		j := (p.cursor + i) % len(p.nodes)
		n := p.nodes[j]
		if (id == "" && n.Config.Model != model) || (id != "" && n.Config.ID != id) || (n.dynamic && (!n.ready || !time.Now().Before(n.leaseUntil) || !allowed[n])) || n.active >= n.Config.Capacity || time.Now().Before(n.failedUntil) {
			continue
		}
		if best < 0 || n.active*p.nodes[best].Config.Capacity < p.nodes[best].active*n.Config.Capacity {
			best = j
		}
	}
	if best < 0 {
		return nil, nil
	}
	n := p.nodes[best]
	n.active++
	p.cursor = (best + 1) % len(p.nodes)
	var once sync.Once
	return n, func(failed bool) {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			n.active--
			if failed {
				n.failedUntil = time.Now().Add(5 * time.Second)
			}
		})
	}
}
func (n *Node) Endpoint() string {
	return strings.TrimRight(n.Config.URL, "/") + "/v1/chat/completions"
}
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.nodes {
		n.Client.CloseIdleConnections()
	}
}

// AddDynamic registers an authenticated node as unavailable until its first heartbeat.
func (p *Pool) AddDynamic(c config.Node, clientTLS *tls.Config, authorized func() bool) error {
	temporary, err := New([]config.Node{c})
	if err != nil {
		return err
	}
	n := temporary.nodes[0]
	n.Client.Transport.(*http.Transport).TLSClientConfig = clientTLS.Clone()
	n.dynamic = true
	n.authorized = authorized
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, other := range p.nodes {
		if other.Config.ID == c.ID {
			n.Client.CloseIdleConnections()
			return errors.New("node ID already registered")
		}
		if other.Config.Model == c.Model && other.Config.Revision != c.Revision {
			n.Client.CloseIdleConnections()
			return errors.New("model revision conflict")
		}
	}
	p.nodes = append(p.nodes, n)
	return nil
}
func (p *Pool) CheckDescriptor(c config.Node) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.nodes {
		if n.Config.ID == c.ID {
			return errors.New("node ID already registered")
		}
		if n.Config.Model == c.Model && n.Config.Revision != c.Revision {
			return errors.New("model revision conflict")
		}
	}
	return nil
}
func (p *Pool) Heartbeat(id string, ready bool, ttl time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.nodes {
		if n.Config.ID == id && n.dynamic {
			n.ready = ready
			n.leaseUntil = time.Now().Add(ttl)
			return
		}
	}
}

type Status struct {
	ID       string `json:"id"`
	Model    string `json:"model"`
	Capacity int    `json:"capacity"`
	Active   int    `json:"active"`
	State    string `json:"state"`
}

// Snapshot intentionally omits private addresses, credentials and certificate material.
func (p *Pool) Snapshot() []Status {
	p.mu.Lock()
	out := make([]Status, 0, len(p.nodes))
	nodes := append([]*Node(nil), p.nodes...)
	for _, n := range nodes {
		state := "configured"
		if n.dynamic {
			state = "unavailable"
			if n.ready && time.Now().Before(n.leaseUntil) && !time.Now().Before(n.failedUntil) {
				state = "ready"
			}
		}
		out = append(out, Status{ID: n.Config.ID, Model: n.Config.Model, Capacity: n.Config.Capacity, Active: n.active, State: state})
	}
	p.mu.Unlock()
	for i, n := range nodes {
		if n.dynamic && !n.authorized() {
			out[i].State = "unavailable"
		}
	}
	return out
}

// SetTransport replaces direct dialing for an enrolled outbound agent.
func (p *Pool) SetTransport(id string, transport http.RoundTripper) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.nodes {
		if n.Config.ID == id {
			n.Client.CloseIdleConnections()
			n.Client.Transport = transport
			return
		}
	}
}
