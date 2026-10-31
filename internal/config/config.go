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
	"regexp"
	"time"
)

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)

func ValidID(s string) bool { return idPattern.MatchString(s) }

type Key struct {
	Admin  bool   `json:"admin,omitempty"`
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}
type Node struct {
	Connection    string `json:"connection,omitempty"`
	ModelSHA256   string `json:"model_sha256,omitempty"`
	ID            string `json:"id"`
	URL           string `json:"url"`
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model"`
	Revision      string `json:"revision"`
	Capacity      int    `json:"capacity"`
	CAFile        string `json:"ca_file,omitempty"`
	KeyEnv        string `json:"key_env,omitempty"`
}
type Config struct {
	ControlCACert   string `json:"control_ca_cert,omitempty"`
	ControlCAKey    string `json:"control_ca_key,omitempty"`
	Listen          string `json:"listen"`
	Database        string `json:"database"`
	TLSCert         string `json:"tls_cert,omitempty"`
	TLSKey          string `json:"tls_key,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	MaxBodyBytes    int64  `json:"max_body_bytes"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Keys            []Key  `json:"keys"`
	Nodes           []Node `json:"nodes"`
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
		c.TimeoutSeconds = 180
	}
	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = 1 << 20
	}
	if c.MaxOutputTokens == 0 {
		c.MaxOutputTokens = 2048
	}
	return c, c.Validate()
}
func IsLoopback(host string) bool { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }
func (c Config) Validate() error {
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
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 3600 || c.MaxBodyBytes < 1 || c.MaxBodyBytes > 16<<20 || c.MaxOutputTokens < 1 || c.MaxOutputTokens > 131072 {
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

	seen := map[string]bool{}
	hashes := map[string]bool{}
	for _, k := range c.Keys {
		if !ValidID(k.ID) || seen[k.ID] || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(k.SHA256) || hashes[k.SHA256] {
			return errors.New("invalid or duplicate key ID/hash")
		}
		seen[k.ID] = true
		hashes[k.SHA256] = true
	}
	seen = map[string]bool{}
	revisions := map[string]string{}
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
		seen[n.ID] = true
		if r, ok := revisions[n.Model]; ok && r != n.Revision {
			return errors.New("nodes for one model alias must use the same revision")
		}
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
