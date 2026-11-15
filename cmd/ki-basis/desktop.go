package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"localaiserver/internal/agent"
	"localaiserver/internal/audit"
	"localaiserver/internal/catalog"
	"localaiserver/internal/config"
	"localaiserver/internal/control"
	"localaiserver/internal/gateway"
	"localaiserver/internal/management"
)

const desktopNodeID = "desktop-local"
const desktopSessionFile = ".desktop-session.json"

type desktopOptions struct {
	Dir     string
	Port    int
	Service bool
}
type desktopApp struct {
	mu                                   sync.Mutex
	dir, address, token, key, gatewayURL string
	ctx                                  context.Context
	cancel                               context.CancelFunc
	service                              bool
	agent                                *localAgent
	config                               config.Config
	client                               *http.Client
	store                                *audit.Store
	gateway                              *gateway.Gateway
	controller                           *control.Service
	server, backend                      *http.Server
	serverDone, backendDone              chan error
	workers                              sync.WaitGroup
	operationCancel                      context.CancelFunc
	busy                                 bool
	modelGeneration, modelActionName     string
	stage, errorCode, answer             string
	progress                             runtimeDownloadProgress
	closeOnce                            sync.Once
	cookieName                           string
	lifetimeMu                           sync.Mutex
	closing                              bool
	requests                             sync.WaitGroup
	background                           sync.WaitGroup
	servers                              sync.WaitGroup
	closeErr                             error
}

func desktopDirectory() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "KI-Basis", "desktop"), nil
}
func desktopCommand(args []string) (result error) {
	flags := flag.NewFlagSet("desktop", flag.ContinueOnError)
	dir := flags.String("dir", "", "private installation data directory")
	port := flags.Int("port", 3210, "loopback browser port; default falls back if occupied")
	launch := flags.Bool("open-browser", true, "open local browser")
	service := flags.Bool("service-mode", false, "run under the operating system service manager")
	_ = flags.Bool("quiet-setup", false, "do not print private setup links")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *service && !desktopServiceManager() {
		return errors.New("desktop_service_manager_required")
	}
	absolute, err := desktopResolveDirectory(*dir)
	if err != nil {
		return err
	}
	if !*service {
		if link, ok := reuseDesktopSession(absolute); ok {
			if *launch {
				openBrowser(link)
			}
			return nil
		}
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return errors.New("desktop_storage_unavailable")
	}
	unlock, err := lockProcessFile(absolute, ".desktop.lock")
	if err != nil {
		return errors.New("desktop_already_running_or_locked")
	}
	defer unlock()
	ctx, cancel := terminationContext()
	defer cancel()
	app, err := startDesktop(ctx, desktopOptions{Dir: absolute, Port: *port, Service: *service})
	if err != nil {
		return err
	}
	defer func() { app.close(); result = errors.Join(result, app.closeErr) }()
	if *launch && !*service {
		openBrowser(app.address + "/#" + app.token)
	}
	select {
	case <-app.ctx.Done():
		return nil
	case err = <-app.serverDone:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("desktop_listener_failed")
	case err = <-app.backendDone:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("desktop_gateway_failed")
	}
}
func desktopOpenCommand(args []string) error {
	flags := flag.NewFlagSet("desktop-open", flag.ContinueOnError)
	dir := flags.String("dir", "", "existing installation data directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	resolved, err := desktopResolveDirectory(*dir)
	if err != nil {
		return err
	}
	link, ok := reuseDesktopSession(resolved)
	if !ok {
		return errors.New("desktop_not_running_start_application_or_service")
	}
	openBrowser(link)
	return nil
}
func desktopResolveDirectory(value string) (string, error) {
	var err error
	if value == "" {
		value, err = desktopDirectory()
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(value) {
		return "", errors.New("desktop_directory_must_be_absolute")
	}
	return filepath.Abs(value)
}

func reuseDesktopSession(dir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, desktopSessionFile))
	if err != nil {
		return "", false
	}
	var s setupSession
	if json.Unmarshal(raw, &s) != nil || len(s.Token) != 64 {
		return "", false
	}
	if _, err = hex.DecodeString(s.Token); err != nil {
		return "", false
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.URL+"/desktop/status", nil)
	req.Header.Set("X-Desktop-Token", s.Token)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer response.Body.Close()
	var status struct {
		Directory string `json:"data_directory"`
	}
	if response.StatusCode != 200 || json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20)).Decode(&status) != nil || status.Directory != dir {
		return "", false
	}
	return s.URL + "/#" + s.Token, true
}

// The browser receives a setup capability, never the gateway administrator key.
// All privileged actions use the existing authenticated TLS API on the server.
func startDesktop(parent context.Context, options desktopOptions) (result *desktopApp, err error) {
	if !filepath.IsAbs(options.Dir) || options.Port < 0 || options.Port > 65535 {
		return nil, errors.New("desktop_invalid_options")
	}
	if err = os.MkdirAll(options.Dir, 0700); err != nil {
		return nil, errors.New("desktop_storage_unavailable")
	}
	if err = protectCentralDirectory(options.Dir); err != nil {
		return nil, errors.New("desktop_storage_unavailable")
	}
	ctx, cancel := context.WithCancel(parent)
	d := &desktopApp{dir: options.Dir, ctx: ctx, cancel: cancel, service: options.Service, stage: "runtime", serverDone: make(chan error, 1), backendDone: make(chan error, 1)}
	defer func() {
		if err != nil {
			d.close()
			err = errors.Join(err, d.closeErr)
		}
	}()
	central := filepath.Join(d.dir, "central")
	if err = beginDesktopInitialization(d.dir); err != nil {
		return nil, err
	}
	var listener net.Listener
	if _, statErr := os.Stat(filepath.Join(central, "config.json")); statErr == nil {
		c, loadErr := config.Load(filepath.Join(central, "config.json"))
		if loadErr != nil {
			return nil, errors.New("desktop_configuration_invalid")
		}
		host, _, splitErr := net.SplitHostPort(c.Listen)
		if splitErr != nil || host != "127.0.0.1" {
			return nil, errors.New("desktop_gateway_must_be_loopback")
		}
		listener, err = net.Listen("tcp", c.Listen)
	} else if os.IsNotExist(statErr) {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
	} else {
		return nil, errors.New("desktop_storage_unavailable")
	}
	if err != nil {
		return nil, errors.New("desktop_gateway_port_in_use")
	}
	defer func() {
		if err != nil {
			listener.Close()
		}
	}()
	d.config, d.key, err = localConfiguration(central, listener.Addr().(*net.TCPAddr).Port)
	if err != nil {
		return nil, errors.New("desktop_configuration_invalid")
	}
	d.gatewayURL = "https://" + d.config.Listen
	roots := x509.NewCertPool()
	ca, readErr := os.ReadFile(d.config.ControlCACert)
	if readErr != nil || !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("desktop_configuration_invalid")
	}
	d.client = &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	d.store, err = audit.Open(d.config.Database)
	if err != nil {
		return nil, errors.New("desktop_audit_unavailable")
	}
	activeKeys, loadErr := d.store.LoadKeys(ctx)
	if loadErr != nil {
		return nil, errors.New("desktop_audit_unavailable")
	}
	if err = d.provisionApplicationAccess(activeKeys); err != nil {
		return nil, err
	}
	if activeKeys == nil {
		activeKeys = d.config.Keys
	}
	d.key = desktopAdminCredential(central, activeKeys)
	if d.key == "" {
		return nil, errors.New("desktop_admin_credentials_unavailable")
	}
	d.gateway, err = gateway.New(d.config, d.store)
	if err != nil {
		return nil, errors.New("desktop_gateway_configuration_invalid")
	}
	d.controller, err = control.New(d.config, d.store, d.gateway.Pool())
	if err != nil {
		return nil, errors.New("desktop_gateway_configuration_invalid")
	}
	mux := http.NewServeMux()
	mux.Handle("/internal/", d.controller.Handler())
	mux.Handle("/admin/", d.gateway.Admin(d.controller.AdminHandler()))
	mux.Handle("/", d.gateway.Handler())
	d.backend = &http.Server{Handler: d.trackRequests(d.gateway.GuardPaths(mux)), BaseContext: func(net.Listener) context.Context { return d.ctx }, TLSConfig: d.controller.TLSConfig(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: config.UploadTimeout, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	d.background.Add(1)
	go func() { defer d.background.Done(); d.controller.Monitor(ctx) }()
	d.servers.Add(1)
	go func() {
		defer d.servers.Done()
		d.backendDone <- d.backend.ServeTLS(listener, d.config.TLSCert, d.config.TLSKey)
	}()
	if err = d.startExistingAgent(); err != nil {
		return nil, err
	}
	browserListener, listenErr := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(options.Port)))
	if listenErr != nil && options.Port == 3210 {
		browserListener, listenErr = net.Listen("tcp", "127.0.0.1:0")
	}
	if listenErr != nil {
		return nil, errors.New("desktop_port_in_use")
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		browserListener.Close()
		return nil, err
	}
	d.token = hex.EncodeToString(token[:])
	d.address = "http://" + browserListener.Addr().String()
	cookieHash := sha256.Sum256([]byte(d.dir + "|" + d.address))
	d.cookieName = "ki_desktop_" + hex.EncodeToString(cookieHash[:8])
	d.server = &http.Server{Handler: d.trackRequests(d.handler()), BaseContext: func(net.Listener) context.Context { return d.ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	d.servers.Add(1)
	go func() { defer d.servers.Done(); d.serverDone <- d.server.Serve(browserListener) }()
	session, _ := json.Marshal(setupSession{URL: d.address, Token: d.token})
	if err = writePrivate(filepath.Join(d.dir, desktopSessionFile), session); err != nil {
		return nil, errors.New("desktop_session_storage_failed")
	}
	return d, nil
}
func (d *desktopApp) startExistingAgent() error {
	dir := filepath.Join(d.dir, "agent")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("desktop_storage_unavailable")
	}
	a := &localAgent{dir: dir, ctx: d.ctx}
	d.agent = a
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err == nil {
		if json.Unmarshal(raw, &a.settings) != nil {
			return errors.New("desktop_agent_settings_invalid")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("desktop_agent_settings_invalid")
	}
	if _, err = os.Stat(filepath.Join(dir, "agent.json")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return errors.New("desktop_agent_identity_invalid")
	}
	state, err := agent.Load(filepath.Join(dir, "agent.json"))
	if err != nil || state.Gateway != d.gatewayURL || state.Identity.Node.ID != desktopNodeID {
		return errors.New("desktop_agent_identity_mismatch")
	}
	if err = a.settings.Runtime.Validate(); err != nil {
		return errors.New("desktop_agent_settings_invalid")
	}
	if info, statErr := os.Stat(a.settings.Binary); statErr != nil || !info.Mode().IsRegular() {
		return nil
	}
	if err = a.start(); err != nil {
		return errors.New("desktop_agent_start_failed")
	}
	d.stage = "model"
	return nil
}

// Admission and shutdown share a mutex: no request can become a new store
// borrower after shutdown starts waiting for the existing handlers.
func (d *desktopApp) trackRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.lifetimeMu.Lock()
		if d.closing {
			d.lifetimeMu.Unlock()
			desktopError(w, 503, "desktop_stopping")
			return
		}
		d.requests.Add(1)
		d.lifetimeMu.Unlock()
		defer d.requests.Done()
		next.ServeHTTP(w, r)
	})
}

func (d *desktopApp) close() {
	d.closeOnce.Do(func() {
		d.lifetimeMu.Lock()
		d.closing = true
		d.lifetimeMu.Unlock()
		d.mu.Lock()
		d.cancel()
		d.mu.Unlock()
		// Cancel broker jobs before waiting for long polls or streaming requests.
		// The server BaseContexts also cancel already-admitted request contexts.
		if d.controller != nil {
			d.controller.Close()
		}
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		for _, server := range []*http.Server{d.server, d.backend} {
			if server == nil {
				continue
			}
			_ = server.Shutdown(shutdown)
			// Close interrupts body reads that do not observe context cancellation.
			if err := server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				d.closeErr = errors.Join(d.closeErr, errors.New("desktop_listener_shutdown_failed"))
			}
		}
		cancel()
		d.workers.Wait()
		if d.agent != nil {
			d.agent.mu.Lock()
			if d.agent.cancel != nil {
				d.agent.cancel()
				<-d.agent.done
				d.agent.cancel = nil
				d.agent.runner = nil
			}
			d.agent.mu.Unlock()
		}
		d.background.Wait()
		d.servers.Wait()
		d.requests.Wait()
		if d.gateway != nil {
			d.gateway.Close()
		}
		if d.client != nil {
			d.client.CloseIdleConnections()
		}
		// Canceled SQL transactions can still be rolling back. Store.Close drains
		// its connection only after every request, monitor and owned agent has exited.
		if d.store != nil {
			if err := d.store.Close(); err != nil {
				d.closeErr = errors.Join(d.closeErr, errors.New("desktop_audit_shutdown_failed"))
			}
		}
		// Installers interpret disappearance as permission to replace executables.
		// A failed store drain must not be presented as a completed shutdown.
		if d.closeErr == nil {
			if err := os.Remove(filepath.Join(d.dir, desktopSessionFile)); err != nil && !os.IsNotExist(err) {
				d.closeErr = errors.New("desktop_session_cleanup_failed")
			}
		}
	})
}

func (d *desktopApp) status() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	report := management.Report{}
	connected := false
	installed := false
	if d.agent != nil {
		d.agent.mu.Lock()
		connected = d.agent.runner != nil
		if connected && d.agent.runner.Manager != nil {
			report = d.agent.runner.Manager.Report()
		}
		if d.agent.settings.Binary != "" {
			info, err := os.Stat(d.agent.settings.Binary)
			installed = err == nil && info.Mode().IsRegular()
		}
		d.agent.mu.Unlock()
	}
	if d.modelGeneration != "" && report.Generation != d.modelGeneration {
		if d.modelActionName == "stop" {
			report.State = "stopping"
		} else {
			report.State = "starting"
		}
	}
	ready := d.readyForReport(report)
	if report.State == "running" && !ready {
		report.State = "loading"
	}
	recommendation := runtimeRecommendation()
	stage := d.stage
	if !d.busy && connected {
		if report.State == "running" {
			stage = "ready"
		}
		if d.answer != "" && ready {
			stage = "complete"
		}
	}
	executable, _ := os.Executable()
	return map[string]any{"stage": stage, "error": d.errorCode, "busy": d.busy, "runtime": map[string]any{"supported": recommendation.Supported, "installed": installed, "version": recommendation.Version, "source": recommendation.URL, "sha256": recommendation.SHA256, "size": recommendation.Size, "platform": recommendation.Platform}, "progress": d.progress, "connected": connected, "ready": ready, "node_id": desktopNodeID, "report": report, "model": catalog.Default(), "answer": d.answer, "lifecycle": map[string]any{"mode": map[bool]string{true: "service", false: "desktop"}[d.service], "can_quit": !d.service}, "data_directory": d.dir, "process_id": os.Getpid(), "executable": executable}
}
func (d *desktopApp) begin(stage string, work func(context.Context) error) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.busy || d.ctx.Err() != nil {
		return false
	}
	ctx, cancel := context.WithCancel(d.ctx)
	d.operationCancel = cancel
	d.busy = true
	d.stage = stage
	d.errorCode = ""
	d.answer = ""
	d.workers.Add(1)
	go func() {
		defer d.workers.Done()
		defer cancel()
		err := work(ctx)
		d.mu.Lock()
		defer d.mu.Unlock()
		d.busy = false
		d.operationCancel = nil
		if err != nil {
			if ctx.Err() != nil {
				d.errorCode = "desktop_operation_cancelled"
			} else {
				d.errorCode = err.Error()
			}
		}
	}()
	return true
}

func desktopAdminCredential(dir string, keys []config.Key) string {
	for _, name := range []string{"admin-access.json", "access.json", "new-api-key.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var access struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(raw, &access) != nil {
			continue
		}
		hash := sha256.Sum256([]byte(access.Key))
		for _, key := range keys {
			if key.Admin && key.SHA256 == hex.EncodeToString(hash[:]) {
				return access.Key
			}
		}
	}
	return ""
}

// Pool readiness includes the latest authenticated heartbeat, model properties,
// broker connection and lease. A spawned model process alone is not ready.
func (d *desktopApp) readyForReport(report management.Report) bool {
	if report.State != "running" || d.modelGeneration != "" && report.Generation != d.modelGeneration {
		return false
	}
	for _, node := range d.gateway.Pool().Snapshot() {
		if node.ID == desktopNodeID && node.State == "ready" {
			return true
		}
	}
	return false
}
