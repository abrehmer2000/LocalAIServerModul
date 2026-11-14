package main

import (
	"context"
	"encoding/json"
	"io"
	"localaiserver/internal/management"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func desktopFixture(t *testing.T, service bool) *desktopApp {
	t.Helper()
	app, err := startDesktop(context.Background(), desktopOptions{Dir: t.TempDir(), Service: service})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.close)
	return app
}
func desktopRequest(t *testing.T, d *desktopApp, method, path, token, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, d.address+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Desktop-Token", token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
func TestDesktopLocalAuthenticationAndCapabilityBoundary(t *testing.T) {
	d := desktopFixture(t, false)
	for _, tc := range []struct {
		method, path, token, origin string
		want                        int
	}{{"GET", "/desktop/status", "", "", 401}, {"GET", "/desktop/status", d.token, "https://evil.example", 403}, {"POST", "/desktop/prepare", d.token, "null", 403}, {"GET", "/desktop/status", d.token, "", 200}, {"GET", "/", "", "", 200}} {
		res := desktopRequest(t, d, tc.method, tc.path, tc.token, tc.origin)
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatalf("%s %s status %d want %d: %s", tc.method, tc.path, res.StatusCode, tc.want, body)
		}
		if strings.Contains(string(body), d.key) {
			t.Fatal("gateway admin key crossed browser boundary")
		}
	}
	request := httptest.NewRequest("GET", d.address+"/desktop/status", nil)
	request.Host = "attacker.example"
	request.Header.Set("X-Desktop-Token", d.token)
	recorder := httptest.NewRecorder()
	d.handler().ServeHTTP(recorder, request)
	if recorder.Code != 403 {
		t.Fatal("untrusted Host accepted")
	}
	res := desktopRequest(t, d, "POST", "/desktop/session", d.token, d.address)
	cookies := res.Cookies()
	res.Body.Close()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/desktop/" {
		t.Fatal("invalid session cookie")
	}
	req, _ := http.NewRequest("GET", d.address+"/desktop/status", nil)
	req.AddCookie(cookies[0])
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("cookie did not authenticate reload")
	}
	req, _ = http.NewRequest("POST", d.address+"/desktop/session", nil)
	req.AddCookie(cookies[0])
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("cookie could bootstrap capability")
	}
}
func TestDesktopSessionReuseIdentityAndShutdown(t *testing.T) {
	d := desktopFixture(t, false)
	dir := d.dir
	key := d.key
	if link, ok := reuseDesktopSession(dir); !ok || !strings.HasSuffix(link, d.token) {
		t.Fatal("could not reopen live installation")
	}
	// A preinstalled executable avoids network access; preparation must still use
	// real TLS enrollment and persist a certificate-bearing local agent identity.
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings := agentSettings{Binary: binary, Models: filepath.Join(dir, "agent", "models"), Engine: "http://127.0.0.1:59999"}
	raw, _ := json.Marshal(settings)
	if err = os.WriteFile(filepath.Join(dir, "agent", "settings.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	d.agent.mu.Lock()
	d.agent.settings = settings
	d.agent.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = d.prepare(ctx); err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(dir, "agent", "agent.json")
	identity, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	devices, err := d.store.Devices(ctx)
	if err != nil || len(devices) != 1 || devices[0].Node.ID != desktopNodeID {
		t.Fatalf("devices %v %v", devices, err)
	}
	if err = d.prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(identityPath); string(after) != string(identity) {
		t.Fatal("repeated prepare replaced identity")
	}
	d.close()
	if _, err = os.Stat(filepath.Join(dir, desktopSessionFile)); !os.IsNotExist(err) {
		t.Fatal("session retained after shutdown")
	}
	if _, ok := reuseDesktopSession(dir); ok {
		t.Fatal("closed installation reusable")
	}
	next, err := startDesktop(context.Background(), desktopOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer next.close()
	if next.key != key {
		t.Fatal("restart replaced existing credential")
	}
	if after, _ := os.ReadFile(identityPath); string(after) != string(identity) {
		t.Fatal("restart replaced identity")
	}
	if !next.status()["connected"].(bool) {
		t.Fatal("agent did not reconnect")
	}
}
func TestDesktopServiceCannotQuitAndExplicitPortConflict(t *testing.T) {
	d := desktopFixture(t, true)
	res := desktopRequest(t, d, "POST", "/desktop/quit", d.token, d.address)
	res.Body.Close()
	if res.StatusCode != 409 {
		t.Fatal("browser can stop system service")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, err = startDesktop(context.Background(), desktopOptions{Dir: t.TempDir(), Port: listener.Addr().(*net.TCPAddr).Port})
	if err == nil || err.Error() != "desktop_port_in_use" {
		t.Fatalf("port conflict: %v", err)
	}
}
func TestDesktopCancelAndStopWaitForWorkers(t *testing.T) {
	d := desktopFixture(t, false)
	entered := make(chan struct{})
	finished := make(chan struct{})
	if !d.begin("runtime", func(ctx context.Context) error { close(entered); <-ctx.Done(); close(finished); return ctx.Err() }) {
		t.Fatal("operation not started")
	}
	<-entered
	if d.begin("runtime", func(context.Context) error { return nil }) {
		t.Fatal("overlapping operation accepted")
	}
	d.close()
	select {
	case <-finished:
	default:
		t.Fatal("close returned before operation stopped")
	}
	if d.begin("runtime", func(context.Context) error { return nil }) {
		t.Fatal("operation accepted during shutdown")
	}
}

func TestDesktopCookiesRemainIndependentAcrossInstallations(t *testing.T) {
	first := desktopFixture(t, false)
	second := desktopFixture(t, false)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	for _, app := range []*desktopApp{first, second} {
		req, _ := http.NewRequest("POST", app.address+"/desktop/session", strings.NewReader("{}"))
		req.Header.Set("X-Desktop-Token", app.token)
		req.Header.Set("Origin", app.address)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("session rejected")
		}
	}
	for _, app := range []*desktopApp{first, second} {
		response, err := client.Get(app.address + "/desktop/status")
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			Directory string `json:"data_directory"`
		}
		err = json.NewDecoder(response.Body).Decode(&status)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || status.Directory != app.dir {
			t.Fatalf("installation cookie overwritten: %d %v", response.StatusCode, err)
		}
	}
}
func TestDesktopExplicitDirectoryDoesNotRequireUserProfile(t *testing.T) {
	d := desktopFixture(t, false)
	t.Setenv("APPDATA", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if err := desktopCommand([]string{"--dir", d.dir, "--open-browser=false"}); err != nil {
		t.Fatalf("explicit directory depended on user profile: %v", err)
	}
	if resolved, err := desktopResolveDirectory(d.dir); err != nil || resolved != d.dir {
		t.Fatalf("explicit directory: %s %v", resolved, err)
	}
	if _, err := desktopResolveDirectory("relative"); err == nil {
		t.Fatal("relative directory accepted")
	}
}
func TestDesktopProcessReportRequiresAuthenticatedReadiness(t *testing.T) {
	d := desktopFixture(t, false)
	if d.readyForReport(management.Report{State: "running"}) {
		t.Fatal("process report treated as engine readiness")
	}
	if d.status()["ready"].(bool) {
		t.Fatal("fresh installation ready without engine")
	}
	if err := d.testAnswer(context.Background()); err == nil || err.Error() != "desktop_model_not_ready" {
		t.Fatalf("test accepted unready model: %v", err)
	}
}
