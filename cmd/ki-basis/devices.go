package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"localaiserver/internal/agent"
	"localaiserver/internal/audit"
	"localaiserver/internal/catalog"
	"localaiserver/internal/config"
	"localaiserver/internal/control"
	"localaiserver/internal/pki"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"os/user"
	"strings"
	"syscall"
	"time"
)

func actor() string {
	u, err := user.Current()
	if err != nil {
		return "local-os:unknown"
	}
	return "local-os:" + u.Username
}
func deviceCommand(command string, args []string) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	cfgPath := flags.String("config", "config.local.json", "gateway configuration")
	dir := flags.String("dir", "pki", "new PKI directory")
	host := flags.String("host", "127.0.0.1", "gateway certificate host/IP")
	id := flags.String("node-id", "", "unique device ID")
	address := flags.String("url", "", "HTTPS address of the agent, reachable from gateway")
	model := flags.String("model", catalog.DefaultAlias, "public model alias")
	upstream := flags.String("upstream-model", catalog.DefaultAlias, "engine model alias")
	revision := flags.String("revision", "", "model and engine revision")
	modelHash := flags.String("model-sha256", "", "model file SHA-256 for managed engine")
	capacity := flags.Int("capacity", 1, "maximum concurrent inference requests")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if command == "init-tls" {
		return pki.Init(*dir, *host)
	}
	c, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if c.ControlCACert == "" {
		return errors.New("control CA must be configured")
	}
	s, err := audit.Open(c.Database)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch command {
	case "join-token":
		n := config.Node{ID: *id, URL: *address, Model: *model, UpstreamModel: *upstream, Revision: *revision, Capacity: *capacity, ModelSHA256: *modelHash}
		if err = config.ValidateNode(n); err != nil {
			return err
		}
		u, _ := url.Parse(n.URL)
		if u.Scheme != "https" {
			return errors.New("agents require HTTPS even on loopback")
		}
		for _, other := range c.Nodes {
			if other.ID == n.ID || other.Model == n.Model && other.Revision != n.Revision {
				return errors.New("join grant conflicts with static configuration")
			}
		}
		var random [32]byte
		if _, err = rand.Read(random[:]); err != nil {
			return err
		}
		token := "jt_" + hex.EncodeToString(random[:])
		expiry := time.Now().Add(10 * time.Minute)
		if err = s.IssueJoin(ctx, control.TokenHash(token), n, expiry, actor()); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"token": token, "expires_at": expiry.UTC().Format(time.RFC3339), "node_id": n.ID})
	case "node-list":
		devices, err := s.Devices(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(devices)
	case "node-revoke":
		return s.Revoke(ctx, *id, actor())
	}
	return errors.New("unknown device command")
}
func agentCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ki-basis agent join|run")
	}
	flags := flag.NewFlagSet("agent "+args[0], flag.ContinueOnError)
	gateway := flags.String("gateway", "", "HTTPS gateway URL")
	ca := flags.String("ca", "", "installation CA public certificate")
	state := flags.String("state", "data/agent.json", "private persistent device identity")
	listen := flags.String("listen", "127.0.0.1:8091", "agent HTTPS listener")
	engine := flags.String("engine", "http://127.0.0.1:8081", "local engine origin")
	binary := flags.String("engine-binary", "", "optional llama-server executable to supervise")
	model := flags.String("model-file", "", "local GGUF model path")
	contextSize := flags.Int("context-size", 4096, "engine context size")
	tokenStdin := flags.Bool("token-stdin", false, "read one-time join token from stdin")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if args[0] == "join" {
		if !*tokenStdin {
			return errors.New("--token-stdin required; tokens must not be placed in command arguments")
		}
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 256))
		if err != nil {
			return err
		}
		return agent.Join(ctx, *gateway, strings.TrimSpace(string(raw)), *ca, *state)
	}
	if args[0] != "run" {
		return errors.New("unknown agent command")
	}
	if (*binary == "") != (*model == "") {
		return errors.New("engine-binary and model-file must both be set")
	}
	if _, _, err := net.SplitHostPort(*listen); err != nil {
		return err
	}
	runner, err := agent.New(*state, *engine)
	if err != nil {
		return err
	}
	defer runner.Close()
	tlsConfig, err := runner.TLSConfig()
	if err != nil {
		return err
	}
	server := &http.Server{Addr: *listen, Handler: runner.Handler(), TLSConfig: tlsConfig, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	engineDone := make(chan struct{})
	engineErrors := make(chan error, 1)
	if *binary != "" {
		go func() {
			defer close(engineDone)
			if err := runner.RunEngine(runCtx, *binary, *model, *contextSize); err != nil {
				slog.Error("managed engine stopped", "reason", err)
				engineErrors <- err
				cancel()
			}
		}()
	} else {
		close(engineDone)
	}
	go runner.RunHeartbeats(runCtx, func(err error) { slog.Warn("heartbeat failed", "reason", err) })
	results := make(chan error, 1)
	go func() { results <- server.ListenAndServeTLS("", "") }()
	slog.Info("agent listening", "address", *listen)
	select {
	case err = <-results:
		cancel()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-runCtx.Done():
		// Best effort withdrawal; heartbeat lease still bounds failure if the network is down.
		withdraw, done := context.WithTimeout(context.Background(), 3*time.Second)
		_ = runner.Heartbeat(withdraw, false)
		done()
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		err = server.Shutdown(shutdown)
		if err != nil {
			_ = server.Close()
		}
		cancel()
		select {
		case <-engineDone:
		case <-time.After(5 * time.Second):
			return errors.New("engine shutdown timeout")
		}
		select {
		case engineErr := <-engineErrors:
			return engineErr
		default:
		}
		return err
	}
}
