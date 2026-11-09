package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"localaiserver/internal/config"
	"localaiserver/internal/pki"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func validCentralHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsMulticast()
	}
	if len(host) > 253 || host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// A separate, fresh network installation. Existing quickstart identities are
// deliberately not rewritten, so rotated keys and certificates stay authoritative.
func centralConfiguration(dir, host, listen string) (config.Config, error) {
	var c config.Config
	if !validCentralHost(host) {
		return c, errors.New("--host must be the non-loopback DNS name or IP reachable from the agent")
	}
	bind, port, err := net.SplitHostPort(listen)
	number, e := strconv.Atoi(port)
	if err != nil || e != nil || number < 1 || number > 65535 || net.ParseIP(bind) == nil || config.IsLoopback(bind) {
		return c, errors.New("--listen requires a non-loopback bind IP and valid port")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return c, err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return c, err
	}
	if err = os.Mkdir(absolute, 0700); err != nil {
		return c, errors.New("central directory must not exist; existing installations are never overwritten")
	}
	if err = protectCentralDirectory(absolute); err != nil {
		return c, err
	}
	pkiDir := filepath.Join(absolute, "pki")
	if err = pki.Init(pkiDir, host, "127.0.0.1"); err != nil {
		return c, err
	}
	c = config.Config{Listen: listen, Database: filepath.Join(absolute, "gateway.db"), TLSCert: filepath.Join(pkiDir, "gateway.crt"), TLSKey: filepath.Join(pkiDir, "gateway.key"), ControlCACert: filepath.Join(pkiDir, "ca.crt"), ControlCAKey: filepath.Join(pkiDir, "ca.key"), TimeoutSeconds: config.DefaultTimeoutSeconds, MaxBodyBytes: config.MaxRequestBytes, MaxOutputTokens: config.DefaultOutputTokens, Nodes: []config.Node{}}
	origin := "https://" + net.JoinHostPort(host, port)
	for _, role := range []struct {
		id, user, file string
		admin          bool
	}{{"central-admin", "admin-01", "admin-access.json", true}, {"prozesslog", "prozesslog-test", "prozesslog-access.json", false}} {
		var random [32]byte
		if _, err = rand.Read(random[:]); err != nil {
			return c, err
		}
		key := "kb_" + hex.EncodeToString(random[:])
		hash := sha256.Sum256([]byte(key))
		c.Keys = append(c.Keys, config.Key{ID: role.id, SHA256: hex.EncodeToString(hash[:]), Admin: role.admin})
		data, _ := json.MarshalIndent(map[string]string{"key": key, "id": role.id, "user": role.user, "base_url": origin + "/v1", "ca_bundle": c.ControlCACert}, "", "  ")
		if err = os.WriteFile(filepath.Join(absolute, role.file), append(data, '\n'), 0600); err != nil {
			return c, err
		}
	}
	if err = c.Validate(); err != nil {
		return c, err
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	// The runnable configuration is written last; partial initialization never
	// silently starts a service or replaces any existing installation.
	if err = os.WriteFile(filepath.Join(absolute, "config.json"), append(data, '\n'), 0600); err != nil {
		return c, err
	}
	return c, nil
}

func centralInitCommand(args []string) error {
	flags := flag.NewFlagSet("init-central", flag.ContinueOnError)
	dir := flags.String("dir", "data/central", "new private central directory (must not exist)")
	host := flags.String("host", "", "DNS name or IP reachable from the model server")
	listen := flags.String("listen", "0.0.0.0:8443", "HTTPS bind IP and port")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := centralConfiguration(*dir, *host, *listen); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Zentrale vorbereitet. config.json prüfen und mit serve starten. Separate Zugänge liegen ausschließlich in admin-access.json und prozesslog-access.json. Nur pki/ca.crt ist zur Weitergabe bestimmt. Es wurde kein Dienst gestartet und keine Firewall geändert.")
	return nil
}
