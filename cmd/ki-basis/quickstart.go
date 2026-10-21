package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"localaiserver/internal/audit"
	"localaiserver/internal/config"
	"localaiserver/internal/control"
	"localaiserver/internal/gateway"
	"localaiserver/internal/pki"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func localConfiguration(dir string, port int) (config.Config, string, error) {
	var c config.Config
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return c, "", err
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return c, "", err
	}
	// Existing configuration is authoritative: never resurrect a rotated or revoked key.
	configPath := filepath.Join(absolute, "config.json")
	if _, e := os.Stat(configPath); e == nil {
		existing, e := config.Load(configPath)
		if e != nil {
			return c, "", e
		}
		var access struct {
			Key string `json:"key"`
		}
		if raw, e := os.ReadFile(filepath.Join(absolute, "access.json")); e == nil && json.Unmarshal(raw, &access) == nil {
			hash := sha256.Sum256([]byte(access.Key))
			for _, k := range existing.Keys {
				if k.SHA256 == hex.EncodeToString(hash[:]) {
					return existing, access.Key, nil
				}
			}
		}
		return existing, "", nil
	} else if !os.IsNotExist(e) {
		return c, "", e
	}
	pkiDir := filepath.Join(absolute, "pki")
	if _, err = os.Stat(pkiDir); os.IsNotExist(err) {
		if err = pki.Init(pkiDir, "127.0.0.1"); err != nil {
			return c, "", err
		}
	} else if err != nil {
		return c, "", err
	}
	accessFile := filepath.Join(absolute, "access.json")
	var access struct {
		Key string `json:"key"`
	}
	data, err := os.ReadFile(accessFile)
	if os.IsNotExist(err) {
		var b [32]byte
		if _, err = rand.Read(b[:]); err != nil {
			return c, "", err
		}
		access.Key = "kb_" + hex.EncodeToString(b[:])
		data, _ = json.Marshal(access)
		f, err := os.OpenFile(accessFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return c, "", err
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			return c, "", writeErr
		}
		if closeErr != nil {
			return c, "", closeErr
		}
	} else if err != nil {
		return c, "", err
	} else {
		if err = json.Unmarshal(data, &access); err != nil || len(access.Key) != 67 {
			return c, "", errors.New("invalid local access file")
		}
	}
	h := sha256.Sum256([]byte(access.Key))
	c = config.Config{Listen: net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), Database: filepath.Join(absolute, "gateway.db"), TLSCert: filepath.Join(pkiDir, "gateway.crt"), TLSKey: filepath.Join(pkiDir, "gateway.key"), ControlCACert: filepath.Join(pkiDir, "ca.crt"), ControlCAKey: filepath.Join(pkiDir, "ca.key"), Keys: []config.Key{{ID: "local-preview", Admin: true, SHA256: hex.EncodeToString(h[:])}}, TimeoutSeconds: 180, MaxBodyBytes: 1 << 20, MaxOutputTokens: 2048, Nodes: []config.Node{}}
	if err = c.Validate(); err != nil {
		return c, "", err
	}
	configData, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return c, "", err
	}
	if err = os.WriteFile(filepath.Join(absolute, "config.json"), configData, 0600); err != nil {
		return c, "", err
	}
	return c, access.Key, nil
}
func quickstartCommand(args []string) error {
	flags := flag.NewFlagSet("quickstart", flag.ContinueOnError)
	dir := flags.String("dir", "data/local-preview", "private local state")
	port := flags.Int("port", 3000, "browser port")
	backendPort := flags.Int("gateway-port", 8443, "local gateway TLS port")
	launch := flags.Bool("open-browser", false, "open local browser")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *port < 1 || *port > 65535 || *backendPort < 1 || *backendPort > 65535 || *port == *backendPort {
		return errors.New("two distinct valid ports required")
	}
	c, key, err := localConfiguration(*dir, *backendPort)
	if err != nil {
		return err
	}
	store, err := audit.Open(c.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	g, err := gateway.New(c, store)
	if err != nil {
		return err
	}
	defer g.Close()
	controller, err := control.New(c, store, g.Pool())
	if err != nil {
		return err
	}
	defer controller.Close()
	mux := http.NewServeMux()
	mux.Handle("/internal/", controller.Handler())
	mux.Handle("/admin/", g.Admin(controller.AdminHandler()))
	mux.Handle("/", g.Handler())
	backend := &http.Server{Handler: mux, TLSConfig: controller.TLSConfig(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	preview, err := browserHandler("https://"+c.Listen, c.ControlCACert, true)
	if err != nil {
		return err
	}
	frontend := &http.Server{Handler: preview, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second}
	backendListener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	defer backendListener.Close()
	browserAddress := net.JoinHostPort("127.0.0.1", fmt.Sprint(*port))
	frontListener, err := net.Listen("tcp", browserAddress)
	if err != nil {
		return err
	}
	defer frontListener.Close()
	if err = store.Append(context.Background(), audit.Event{Type: "gateway.started", Outcome: "local_preview"}); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go controller.Monitor(ctx)
	results := make(chan error, 2)
	go func() { results <- backend.ServeTLS(backendListener, c.TLSCert, c.TLSKey) }()
	go func() { results <- frontend.Serve(frontListener) }()
	address := "http://" + browserAddress
	// This is an explicit local setup screen, equivalent to keygen, not a service log.
	if key == "" {
		key = "Bestehende Zugangsdaten verwenden (kein Schlüssel ausgegeben)."
	}
	fmt.Fprintf(os.Stdout, "\nKI-Basis lokale Vorschau\n\nBrowser: %s\nAPI-Key: %s\nBenutzer-ID: local-preview\n\nKeine Modellgewichte erforderlich. Beenden mit Ctrl+C.\n\n", address, key)
	if *launch {
		openBrowser(address)
	}
	var result error
	select {
	case result = <-results:
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = frontend.Shutdown(shutdown)
	_ = backend.Shutdown(shutdown)
	if errors.Is(result, http.ErrServerClosed) {
		return nil
	}
	return result
}
