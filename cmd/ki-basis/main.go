package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"localaiserver/internal/audit"
	"localaiserver/internal/config"
	"localaiserver/internal/control"
	"localaiserver/internal/gateway"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, flag.ErrHelp) {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	if os.Getenv("KI_BASIS_SSH_ASKPASS") == "1" {
		return sshAskpass()
	}
	if len(os.Args) < 2 {
		return agentUICommand(nil)
	}
	command := os.Args[1]
	switch command {
	case "restore":
		return restoreCommand(os.Args[2:])
	case "agent-bootstrap":
		return agentBootstrapCommand(os.Args[2:])
	case "admin-key":
		return adminKeyCommand(os.Args[2:])
	case "agent-ui":
		return agentUICommand(os.Args[2:])
	case "--help", "-h", "help":
		fmt.Fprintln(os.Stdout, "KI-Basis: serve | quickstart | agent-ui | agent-bootstrap | restore | agent join|run | keygen | audit-verify | audit-export | init-tls | join-token | node-list | node-revoke | admin-key")
		return nil
	case "quickstart":
		return quickstartCommand(os.Args[2:])
	case "web-preview":
		return previewCommand(os.Args[2:])
	case "agent":
		return agentCommand(os.Args[2:])
	case "init-tls", "join-token", "node-list", "node-revoke":
		return deviceCommand(command, os.Args[2:])
	}
	if command == "keygen" {
		if len(os.Args) > 2 {
			if os.Args[2] == "--help" || os.Args[2] == "-h" {
				fmt.Fprintln(os.Stdout, "keygen: creates a new API key and SHA-256 as JSON; redirect stdout to a private file")
				return nil
			}
			return errors.New("keygen accepts no arguments")
		}
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		key := "kb_" + hex.EncodeToString(b[:])
		h := sha256.Sum256([]byte(key))
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"key": key, "sha256": hex.EncodeToString(h[:])})
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	path := flags.String("config", "config.local.json", "configuration file")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if command != "serve" && command != "audit-verify" && command != "audit-export" {
		return errors.New("unknown command")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	store, err := audit.Open(cfg.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	if command != "serve" {
		var emit func(audit.Record) error
		if command == "audit-export" {
			enc := json.NewEncoder(os.Stdout)
			emit = func(r audit.Record) error { return enc.Encode(r) }
		}
		head, err := store.Verify(context.Background(), emit)
		if err != nil {
			return err
		}
		if command == "audit-verify" {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"valid": true, "sequence": head.Sequence, "head_hash": head.Hash})
		}
		return nil
	}
	g, err := gateway.New(cfg, store)
	if err != nil {
		return err
	}
	defer g.Close()
	if err = store.Append(context.Background(), audit.Event{Type: "gateway.started", Outcome: "configuration_loaded"}); err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.Listen, Handler: g.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	var controller *control.Service
	if cfg.ControlCACert != "" {
		controller, err = control.New(cfg, store, g.Pool())
		if err != nil {
			return err
		}
		mux := http.NewServeMux()
		mux.Handle("/internal/", controller.Handler())
		mux.Handle("/admin/", g.Admin(controller.AdminHandler()))
		mux.Handle("/", g.Handler())
		server.Handler = mux
		server.TLSConfig = controller.TLSConfig()
		defer controller.Close()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if controller != nil {
		go controller.Monitor(ctx)
	}
	exit := make(chan error, 1)
	go func() {
		if cfg.TLSCert != "" {
			exit <- server.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			exit <- server.ListenAndServe()
		}
	}()
	slog.Info("gateway listening", "address", cfg.Listen)
	select {
	case err = <-exit:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err = server.Shutdown(shutdown)
		if err != nil {
			_ = server.Close()
		}
		return err
	}
}
