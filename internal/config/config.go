// Package config validates the gateway's operator-controlled configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// InferenceTimeout is the default generation budget. Connection and health checks
// keep their shorter deadlines; inference transports use the request context.
const (
	DefaultTimeoutSeconds   = 900
	MaxRequestBytes         = 16 << 20
	AbsoluteMaxRequestBytes = 128 << 20
	DefaultOutputTokens     = 8192
	UploadTimeout           = 60 * time.Second
	ProxyTimeout            = 1080 * time.Second
)

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)

func ValidID(s string) bool { return idPattern.MatchString(s) }

type Key struct {
	Models []string `json:"models"`
	Admin  bool     `json:"admin,omitempty"`
	ID     string   `json:"id"`
	SHA256 string   `json:"sha256"`
}
type Node struct {
	Kind              string `json:"kind,omitempty"`
	EngineProtocol    string `json:"engine_protocol,omitempty"`
	MaxBodyBytes      int64  `json:"max_body_bytes,omitempty"`
	Vision            bool   `json:"vision,omitempty"`
	ContextPerRequest int    `json:"context_per_request,omitempty"`
	EngineSHA256      string `json:"engine_sha256,omitempty"`
	TemplateSHA256    string `json:"template_sha256,omitempty"`
	Connection        string `json:"connection,omitempty"`
	ModelSHA256       string `json:"model_sha256,omitempty"`
	ID                string `json:"id"`
	URL               string `json:"url"`
	Model             string `json:"model"`
	UpstreamModel     string `json:"upstream_model"`
	Revision          string `json:"revision"`
	Capacity          int    `json:"capacity"`
	CAFile            string `json:"ca_file,omitempty"`
	KeyEnv            string `json:"key_env,omitempty"`
}
type Config struct {
	AuditSigningKey       string `json:"audit_signing_key,omitempty"`
	AuditCheckpoint       string `json:"audit_checkpoint,omitempty"`
	QueueCapacity         int    `json:"queue_capacity,omitempty"`
	QueueWaitSeconds      int    `json:"queue_wait_seconds,omitempty"`
	ClientTimeoutSeconds  int    `json:"client_timeout_seconds,omitempty"`
	MaxConcurrentRequests int    `json:"max_concurrent_requests,omitempty"`
	RequestLog            string `json:"request_log,omitempty"`
	ControlCACert         string `json:"control_ca_cert,omitempty"`
	ControlCAKey          string `json:"control_ca_key,omitempty"`
	Listen                string `json:"listen"`
	Database              string `json:"database"`
	TLSCert               string `json:"tls_cert,omitempty"`
	TLSKey                string `json:"tls_key,omitempty"`
	TimeoutSeconds        int    `json:"timeout_seconds"`
	MaxBodyBytes          int64  `json:"max_body_bytes"`
	MaxOutputTokens       int    `json:"max_output_tokens"`
	Keys                  []Key  `json:"keys"`
	Nodes                 []Node `json:"nodes"`
}

func Load(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, fmt.Errorf("configuration: %w", err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, errors.New("configuration contains trailing data")
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.Database == "" {
		c.Database = "data/gateway.db"
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = MaxRequestBytes
	}
	if c.MaxOutputTokens == 0 {
		c.MaxOutputTokens = DefaultOutputTokens
	}
	if c.RequestLog == "" {
		c.RequestLog = filepath.Join(filepath.Dir(c.Database), "requests.log")
	}
	return c, c.Validate()
}
func IsLoopback(host string) bool { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }
func (c Config) Validate() error {
	if (c.AuditSigningKey == "") != (c.AuditCheckpoint == "") {
		return errors.New("audit signing key and checkpoint path must both be set")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("listen must be host:port")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("tls_cert and tls_key must both be set")
	}
	if !IsLoopback(host) && c.TLSCert == "" {
		return errors.New("non-loopback listeners require TLS")
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 3600 || c.MaxBodyBytes < 1 || c.MaxBodyBytes > 128<<20 || c.MaxOutputTokens < 1 || c.MaxOutputTokens > 131072 {
		return errors.New("invalid resource limits")
	}
	if len(c.Keys) == 0 || len(c.Nodes) == 0 && c.ControlCACert == "" {
		return errors.New("keys and either nodes or control CA required")
	}
	if (c.ControlCACert == "") != (c.ControlCAKey == "") {
		return errors.New("both control CA files required")
	}
	if c.ControlCACert != "" && c.TLSCert == "" {
		return errors.New("control plane requires gateway TLS")
	}

	if c.QueueCapacity < 0 || c.QueueCapacity > 1024 || c.QueueWaitSeconds < 0 || c.QueueWaitSeconds > 3600 || c.ClientTimeoutSeconds < 0 || c.ClientTimeoutSeconds > 3600 || c.MaxConcurrentRequests < 0 || c.MaxConcurrentRequests > 4096 {
		return errors.New("invalid admission limits")
	}
	seen := map[string]bool{}
	hashes := map[string]bool{}
	for _, k := range c.Keys {
		if !ValidID(k.ID) || seen[k.ID] || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(k.SHA256) || hashes[k.SHA256] {
			return errors.New("invalid or duplicate key ID/hash")
		}
		for _, model := range k.Models {
			if !ValidID(model) {
				return errors.New("invalid key model right")
			}
		}
		seen[k.ID] = true
		hashes[k.SHA256] = true
	}
	seen = map[string]bool{}
	revisions := map[string]string{}
	profiles := map[string]string{}
	for _, n := range c.Nodes {
		if n.Connection != "" && n.Connection != "direct" && n.Connection != "outbound" {
			return errors.New("invalid connection mode")
		}
		if n.ModelSHA256 != "" && !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(n.ModelSHA256) {
			return errors.New("invalid model SHA-256")
		}
		if !ValidID(n.ID) || seen[n.ID] || !ValidID(n.Model) || n.UpstreamModel == "" || len(n.UpstreamModel) > 256 || n.Revision == "" || len(n.Revision) > 256 || n.Capacity < 1 || n.Capacity > 128 {
			return errors.New("invalid or duplicate node; model, revision and capacity required")
		}
		if n.Kind != "" && n.Kind != "chat" && n.Kind != "embedding" {
			return errors.New("invalid model kind")
		}
		if n.MaxBodyBytes < 0 || n.MaxBodyBytes > AbsoluteMaxRequestBytes {
			return errors.New("invalid node body limit")
		}
		if n.EngineProtocol != "" && n.EngineProtocol != "llama.cpp" {
			return errors.New("unsupported engine protocol")
		}
		if n.ContextPerRequest != 0 && (n.ContextPerRequest < 32768 || n.ContextPerRequest > 131072) {
			return errors.New("context per slot must be at least 32768")
		}
		seen[n.ID] = true
		if r, ok := revisions[n.Model]; ok && r != n.Revision {
			return errors.New("nodes for one model alias must use the same revision")
		}
		kind := n.Kind
		if kind == "" {
			kind = "chat"
		}
		profile := fmt.Sprintf("%s|%t|%d|%s|%s", kind, n.Vision, n.ContextSize(), n.EngineSHA256, n.TemplateSHA256)
		if old, exists := profiles[n.Model]; exists && old != profile {
			return errors.New("replicas of one alias must have identical capabilities and artifact pins")
		}
		profiles[n.Model] = profile
		revisions[n.Model] = n.Revision
		u, e := url.Parse(n.URL)
		if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("invalid URL for node %s", n.ID)
		}
		if u.Scheme != "https" && (u.Scheme != "http" || !IsLoopback(u.Hostname())) {
			return errors.New("remote nodes require HTTPS; HTTP is allowed only for loopback IPs")
		}
		if n.KeyEnv != "" && os.Getenv(n.KeyEnv) == "" {
			return fmt.Errorf("missing credential environment variable for node %s", n.ID)
		}
	}
	return nil
}
func (c Config) Timeout() time.Duration { return time.Duration(c.TimeoutSeconds) * time.Second }

// ValidateNode applies the same transport and model checks to administrator-issued join grants.
func ValidateNode(n Node) error {
	c := Config{Listen: "127.0.0.1:1", TimeoutSeconds: 1, MaxBodyBytes: 1, MaxOutputTokens: 1, Keys: []Key{{ID: "validation", SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}}, Nodes: []Node{n}}
	return c.Validate()
}

// Zero values preserve old files while selecting the V2 admission defaults.
func (c Config) QueueSize() int {
	if c.QueueCapacity == 0 {
		return 8
	}
	return c.QueueCapacity
}
func (c Config) QueueWait() time.Duration {
	if c.QueueWaitSeconds == 0 {
		return 120 * time.Second
	}
	return time.Duration(c.QueueWaitSeconds) * time.Second
}
func (c Config) ConcurrentLimit() int {
	if c.MaxConcurrentRequests == 0 {
		return 64
	}
	return c.MaxConcurrentRequests
}
func (c Config) ClientTimeout() int {
	if c.ClientTimeoutSeconds == 0 {
		return 420
	}
	return c.ClientTimeoutSeconds
}
func (c Config) MinimumTimeout() int {
	v := c.ClientTimeout() + int(c.QueueWait()/time.Second)
	if v < 600 {
		return 600
	}
	return v
}
func (c Config) ProxyBudget() time.Duration { return c.Timeout() + c.QueueWait() + 60*time.Second }
func (n Node) ContextSize() int {
	if n.ContextPerRequest == 0 {
		return 32768
	}
	return n.ContextPerRequest
}
func (k Key) Allows(model string) bool {
	if k.Models == nil {
		return true
	}
	for _, m := range k.Models {
		if m == model {
			return true
		}
	}
	return false
}

func (n Node) BodyLimit() int64 {
	if n.MaxBodyBytes == 0 {
		return MaxRequestBytes
	}
	return n.MaxBodyBytes
}
func (c Config) ValidateDeployment() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.TimeoutSeconds < c.MinimumTimeout() || c.MaxBodyBytes < MaxRequestBytes || c.MaxOutputTokens < 8192 || c.QueueSize() < 8 {
		return errors.New("V2 requires generation budget >= max(600, client + queue), request >=16 MiB and explicit output allowance >=8192; migrate using prozesslog-profile")
	}
	return nil
}
