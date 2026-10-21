package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"localaiserver/internal/agent"
	"localaiserver/internal/control"
	"localaiserver/internal/webui"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

type agentSettings struct {
	ExistingModel string `json:"existing_model,omitempty"`
	Engine        string `json:"engine"`
	Binary        string `json:"binary"`
	Models        string `json:"models"`
}
type localAgent struct {
	tunnel   managedTunnel
	remoteMu sync.Mutex
	mu       sync.Mutex
	dir      string
	runner   *agent.Runner
	settings agentSettings
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

func (a *localAgent) start() error {
	state := filepath.Join(a.dir, "agent.json")
	if _, err := os.Stat(state); os.IsNotExist(err) {
		return nil
	}
	r, err := agent.New(state, a.settings.Engine)
	if err != nil {
		return err
	}
	s, err := agent.Load(state)
	if err != nil {
		return err
	}
	if s.Identity.Node.Connection != "outbound" {
		r.Close()
		return errors.New("outbound_identity_required")
	}
	r.Manager = agent.NewModelManager(a.settings.Models, a.settings.Binary, a.settings.Engine, s.Identity.Node.UpstreamModel, s.Identity.Node.Capacity)
	r.Manager.ExistingModel = a.settings.ExistingModel
	ctx, cancel := context.WithCancel(a.ctx)
	a.cancel = cancel
	a.runner = r
	a.done = make(chan struct{})
	go func() {
		done := a.done
		defer close(done)
		heartbeatDone := make(chan struct{})
		go func() { defer close(heartbeatDone); r.RunHeartbeats(ctx, func(error) {}) }()
		r.RunOutbound(ctx, func(error) {})
		cancel()
		<-heartbeatDone
		r.Close()
	}()
	return nil
}
func openBrowser(address string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", address).Start()
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", address).Start()
	default:
		_ = exec.Command("xdg-open", address).Start()
	}
}
func writeLocalJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func agentUICommand(args []string) error {
	flags := flag.NewFlagSet("agent-ui", flag.ContinueOnError)
	userDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dir := flags.String("dir", filepath.Join(userDir, "KI-Basis", "agent"), "private agent data directory")
	port := flags.Int("port", 3100, "loopback setup port")
	launch := flags.Bool("open-browser", true, "open local setup browser")
	if err = flags.Parse(args); err != nil {
		return err
	}
	absolute, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return err
	}
	if link, ok := reuseSetupSession(absolute); ok {
		fmt.Fprintln(os.Stdout, "Agent-Einrichtung (bereits geöffnet):", link)
		if *launch {
			openBrowser(link)
		}
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := &localAgent{dir: absolute, ctx: ctx, settings: agentSettings{Engine: "http://127.0.0.1:8082", Models: filepath.Join(absolute, "models")}}
	exe, _ := os.Executable()
	for _, candidate := range []string{filepath.Join(filepath.Dir(exe), "llama-server"), filepath.Join(filepath.Dir(exe), "llama-server.exe"), filepath.Join(filepath.Dir(exe), "runtime", "llama-server"), filepath.Join(filepath.Dir(exe), "runtime", "llama-server.exe"), "/workspace/llama-runtime/llama-server", "/workspace/llama.cpp/build/bin/llama-server"} {
		if _, e := os.Stat(candidate); e == nil {
			a.settings.Binary = candidate
			break
		}
	}
	if a.settings.Binary == "" {
		a.settings.Binary, _ = exec.LookPath("llama-server")
	}
	settingsPath := filepath.Join(absolute, "settings.json")
	if data, e := os.ReadFile(settingsPath); e == nil {
		if err = json.Unmarshal(data, &a.settings); err != nil {
			return err
		}
	}
	if _, e := os.Stat(settingsPath); os.IsNotExist(e) {
		for port := 8082; port < 8182; port++ {
			listener, e := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
			if e == nil {
				listener.Close()
				a.settings.Engine = fmt.Sprintf("http://127.0.0.1:%d", port)
				break
			}
		}
	}
	if err = a.start(); err != nil {
		return err
	}
	defer func() {
		if a.cancel != nil {
			a.cancel()
			<-a.done
		}
	}()
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(random[:])
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(*port)))
	if err != nil && *port == 3100 {
		// Keep explicit custom ports strict; the default may choose another free setup port.
		listener, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return err
	}
	defer listener.Close()
	address := "http://" + listener.Addr().String()
	api := http.NewServeMux()
	api.HandleFunc("POST /setup/quit", func(w http.ResponseWriter, r *http.Request) {
		writeLocalJSON(w, 200, map[string]bool{"stopping": true})
		go func() { time.Sleep(100 * time.Millisecond); stop() }()
	})
	api.HandleFunc("POST /setup/tunnel", a.connectTunnel)
	api.HandleFunc("POST /setup/tunnel/disconnect", a.disconnectTunnel)
	api.HandleFunc("POST /setup/tunnel/reconnect", func(w http.ResponseWriter, r *http.Request) {
		raw, e := os.ReadFile(filepath.Join(a.dir, "central-tunnel.json"))
		var p tunnelProfile
		if e != nil || json.Unmarshal(raw, &p) != nil {
			writeLocalJSON(w, 409, map[string]string{"error": "tunnel_profile_missing"})
			return
		}
		if e = a.startTunnel(r.Context(), p); e != nil {
			writeLocalJSON(w, 409, map[string]string{"error": e.Error()})
			return
		}
		writeLocalJSON(w, 200, a.tunnelStatus())
	})
	api.HandleFunc("POST /setup/ssh/unlock", unlockSSHKey)
	api.HandleFunc("POST /setup/pick-file", pickSetupFile)
	api.HandleFunc("POST /setup/ssh/check", a.remoteCheck)
	api.HandleFunc("POST /setup/ssh/install", a.remoteInstall)
	api.HandleFunc("POST /setup/runtime", a.importRuntime)
	api.HandleFunc("GET /setup/backup", a.backup)
	api.HandleFunc("POST /setup/restore", a.restore)
	sessionCookie := "ki_setup_" + fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	api.HandleFunc("POST /setup/session", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/setup/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		writeLocalJSON(w, 200, map[string]bool{"authenticated": true})
	})
	api.HandleFunc("GET /setup/status", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		result := map[string]any{"connected": a.runner != nil, "settings": a.settings, "tunnel": a.tunnelStatus(), "data_directory": a.dir}
		if a.runner != nil {
			s, e := agent.Load(filepath.Join(a.dir, "agent.json"))
			if e != nil {
				writeLocalJSON(w, 500, map[string]string{"error": "identity_unavailable"})
				return
			}
			result["node_id"] = s.Identity.Node.ID
			result["report"] = a.runner.Manager.Report()
		}
		writeLocalJSON(w, 200, result)
	})
	api.HandleFunc("POST /setup/check", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defaults, registered := a.settings, a.runner != nil
		a.mu.Unlock()
		var in struct {
			Settings agentSettings   `json:"settings"`
			Bundle   *control.Bundle `json:"bundle,omitempty"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
			writeLocalJSON(w, 400, map[string]string{"error": "invalid_request"})
			return
		}
		writeLocalJSON(w, 200, inspectSetup(r.Context(), resolveSettings(in.Settings, defaults), in.Bundle, registered))
	})
	api.HandleFunc("POST /setup/connect", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.runner != nil {
			writeLocalJSON(w, 409, map[string]string{"error": "already_connected"})
			return
		}
		var in struct {
			Bundle   control.Bundle `json:"bundle"`
			Settings agentSettings  `json:"settings"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF || in.Bundle.Version != 1 {
			writeLocalJSON(w, 400, map[string]string{"error": "invalid_connection_file"})
			return
		}
		expiry, e := time.Parse(time.RFC3339, in.Bundle.Expires)
		if e != nil || !time.Now().Before(expiry) {
			writeLocalJSON(w, 400, map[string]string{"error": "connection_file_expired"})
			return
		}
		if in.Settings.Engine == "" {
			in.Settings.Engine = a.settings.Engine
		}
		if in.Settings.Models == "" {
			in.Settings.Models = a.settings.Models
		}
		if in.Settings.Binary == "" {
			in.Settings.Binary = a.settings.Binary
		}
		if e = agent.ValidateEngineURL(in.Settings.Engine); e != nil {
			writeLocalJSON(w, 400, map[string]string{"error": "invalid_engine_address"})
			return
		}
		if in.Settings.Binary != "" {
			if !filepath.IsAbs(in.Settings.Binary) {
				writeLocalJSON(w, 400, map[string]string{"error": "absolute_binary_path_required"})
				return
			}
			if _, e = os.Stat(in.Settings.Binary); e != nil {
				writeLocalJSON(w, 400, map[string]string{"error": "engine_binary_not_found"})
				return
			}
		}
		if e = validateExistingModel(in.Settings.ExistingModel); e != nil {
			writeLocalJSON(w, 400, map[string]string{"error": "existing_model_not_found"})
			return
		}
		in.Settings.Models, e = filepath.Abs(in.Settings.Models)
		if e != nil {
			writeLocalJSON(w, 400, map[string]string{"error": "invalid_model_directory"})
			return
		}
		ca := filepath.Join(a.dir, "join-ca.crt")
		if e = os.WriteFile(ca, []byte(in.Bundle.CA), 0600); e != nil {
			writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
			return
		}
		defer os.Remove(ca)
		joinCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if e = agent.Join(joinCtx, in.Bundle.Gateway, in.Bundle.Token, ca, filepath.Join(a.dir, "agent.json")); e != nil {
			writeLocalJSON(w, 502, map[string]string{"error": "enrollment_failed_check_address_certificate_and_code"})
			return
		}
		a.settings = in.Settings
		data, _ := json.Marshal(a.settings)
		if e = writePrivate(settingsPath, data); e != nil {
			writeLocalJSON(w, 500, map[string]string{"error": "settings_storage_failed"})
			return
		}
		if e = a.start(); e != nil {
			writeLocalJSON(w, 500, map[string]string{"error": "agent_start_failed"})
			return
		}
		writeLocalJSON(w, 201, map[string]bool{"connected": true})
	})
	api.HandleFunc("POST /setup/settings", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		var next agentSettings
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&next) != nil || decoder.Decode(new(any)) != io.EOF || agent.ValidateEngineURL(next.Engine) != nil || !filepath.IsAbs(next.Models) {
			writeLocalJSON(w, 400, map[string]string{"error": "invalid_settings"})
			return
		}
		if next.Binary != "" {
			info, e := os.Stat(next.Binary)
			if !filepath.IsAbs(next.Binary) || e != nil || info.IsDir() {
				writeLocalJSON(w, 400, map[string]string{"error": "engine_binary_not_found"})
				return
			}
		}
		if err := validateExistingModel(next.ExistingModel); err != nil {
			writeLocalJSON(w, 400, map[string]string{"error": "existing_model_not_found"})
			return
		}
		if a.runner != nil {
			auditContext, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			e := a.runner.AuditLocal(auditContext, "settings_change_requested")
			cancel()
			if e != nil {
				writeLocalJSON(w, 503, map[string]string{"error": "management_audit_unavailable"})
				return
			}
		}
		if a.cancel != nil {
			a.cancel()
			<-a.done
			a.runner = nil
			a.cancel = nil
		}
		a.settings = next
		data, _ := json.Marshal(next)
		if e := writePrivate(settingsPath, data); e != nil {
			writeLocalJSON(w, 500, map[string]string{"error": "settings_storage_failed"})
			return
		}
		if e := a.start(); e != nil {
			writeLocalJSON(w, 500, map[string]string{"error": "agent_start_failed"})
			return
		}
		writeLocalJSON(w, 200, map[string]bool{"saved": true})
	})

	api.HandleFunc("POST /setup/autostart", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.runner == nil {
			writeLocalJSON(w, 409, map[string]string{"error": "connect_before_autostart"})
			return
		}
		var in struct {
			Enabled bool `json:"enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
			writeLocalJSON(w, 400, map[string]string{"error": "invalid_request"})
			return
		}
		action := "autostart_disable_requested"
		if in.Enabled {
			action = "autostart_enable_requested"
		}
		auditContext, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		e := a.runner.AuditLocal(auditContext, action)
		cancel()
		if e != nil {
			writeLocalJSON(w, 503, map[string]string{"error": "management_audit_unavailable"})
			return
		}
		if err := configureAutostart(exe, a.dir, in.Enabled); err != nil {
			writeLocalJSON(w, 409, map[string]string{"error": "autostart_unavailable_use_service_installation"})
			return
		}
		writeLocalJSON(w, 200, map[string]bool{"enabled": in.Enabled})
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Host != listener.Addr().String() {
			http.Error(w, "invalid host", 403)
			return
		}
		if r.URL.Path == "/setup/quit" || r.URL.Path == "/setup/tunnel" || r.URL.Path == "/setup/tunnel/reconnect" || r.URL.Path == "/setup/tunnel/disconnect" || r.URL.Path == "/setup/ssh/unlock" || r.URL.Path == "/setup/pick-file" || r.URL.Path == "/setup/restore" || r.URL.Path == "/setup/ssh/check" || r.URL.Path == "/setup/ssh/install" || r.URL.Path == "/setup/session" || r.URL.Path == "/setup/check" || r.URL.Path == "/setup/runtime" || r.URL.Path == "/setup/backup" || r.URL.Path == "/setup/status" || r.URL.Path == "/setup/connect" || r.URL.Path == "/setup/settings" || r.URL.Path == "/setup/autostart" {
			supplied := r.Header.Get("X-Setup-Token")
			if supplied == "" {
				if cookie, e := r.Cookie(sessionCookie); e == nil {
					supplied = cookie.Value
				}
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(supplied)) != 1 {
				http.Error(w, "setup token required", 401)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != address {
				http.Error(w, "invalid origin", 403)
				return
			}
			requestCtx, cancel := context.WithCancel(r.Context())
			stopLink := context.AfterFunc(ctx, cancel)
			defer func() { stopLink(); cancel() }()
			api.ServeHTTP(w, r.WithContext(requestCtx))
			return
		}
		webui.AgentHandler().ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 180 * time.Second, WriteTimeout: 180 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	exit := make(chan error, 1)
	go func() { exit <- server.Serve(listener) }()
	setupURL := address + "/#" + token
	sessionPath := filepath.Join(a.dir, ".ui-session.json")
	sessionData, _ := json.Marshal(setupSession{address, token})
	if e := writePrivate(sessionPath, sessionData); e != nil {
		return e
	}
	defer os.Remove(sessionPath)
	if raw, e := os.ReadFile(filepath.Join(a.dir, "central-tunnel.json")); e == nil {
		var profile tunnelProfile
		if json.Unmarshal(raw, &profile) == nil && profile.Target.valid() {
			a.tunnel.mu.Lock()
			a.tunnel.State = "reconnecting"
			a.tunnel.mu.Unlock()
			go func() {
				if a.startTunnel(ctx, profile) != nil {
					a.tunnel.mu.Lock()
					if a.tunnel.cancel == nil {
						a.tunnel.State = "disconnected"
					}
					a.tunnel.mu.Unlock()
				}
			}()
		}
	}
	fmt.Fprintln(os.Stdout, "Agent-Einrichtung (nur lokal):", setupURL)
	if *launch {
		openBrowser(setupURL)
	}
	select {
	case err = <-exit:
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func writePrivate(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func validateExistingModel(path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(path) || !info.Mode().IsRegular() {
		return errors.New("invalid existing model path")
	}
	return nil
}
