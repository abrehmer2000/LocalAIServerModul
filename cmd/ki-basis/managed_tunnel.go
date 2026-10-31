package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type tunnelProfile struct {
	Target      sshTarget `json:"target"`
	Fingerprint string    `json:"fingerprint"`
	WebPort     int       `json:"web_port"`
	GatewayPort int       `json:"gateway_port"`
}
type managedTunnel struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	State   string `json:"state"`
	Browser string `json:"browser"`
	Gateway string `json:"gateway"`
}

func localTunnelPort(preferred int) (int, error) {
	l, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred))
	if e != nil {
		l, e = net.Listen("tcp", "127.0.0.1:0")
	}
	if e != nil {
		return 0, e
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
func (a *localAgent) tunnelStatus() map[string]string {
	a.tunnel.mu.Lock()
	defer a.tunnel.mu.Unlock()
	return map[string]string{"state": a.tunnel.State, "browser": a.tunnel.Browser, "gateway": a.tunnel.Gateway}
}
func (a *localAgent) startTunnel(ctx context.Context, p tunnelProfile) error {
	fingerprint, line, e := scanHost(ctx, p.Target)
	if e != nil {
		return e
	}
	if p.Fingerprint == "" || fingerprint != p.Fingerprint {
		return fmt.Errorf("ssh_host_key_changed")
	}
	if p.WebPort < 1 || p.WebPort > 65535 || p.GatewayPort < 1 || p.GatewayPort > 65535 {
		return fmt.Errorf("invalid_tunnel_ports")
	}
	a.tunnel.mu.Lock()
	defer a.tunnel.mu.Unlock()
	if a.tunnel.cancel != nil {
		return fmt.Errorf("tunnel_already_running")
	}
	web, e := localTunnelPort(3000)
	if e != nil {
		return e
	}
	gateway, e := localTunnelPort(8443)
	if e != nil {
		return e
	}
	if gateway == web {
		gateway, e = localTunnelPort(0)
		if e != nil {
			return e
		}
	}
	known := filepath.Join(a.dir, "tunnel-known-hosts")
	if e = os.WriteFile(known, []byte(line), 0600); e != nil {
		return e
	}
	work, cancel := context.WithCancel(a.ctx)
	args := sshArgs(p.Target, known)
	// Insert options before the SSH destination. Both remote endpoints are fixed
	// to loopback, so no public service listeners are created by the manager.
	dest := args[len(args)-1]
	args = args[:len(args)-1]
	args = append(args, "-N", "-o", "ExitOnForwardFailure=yes", "-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", web, p.WebPort), "-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", gateway, p.GatewayPort), dest)
	first := exec.CommandContext(work, "ssh", args...)
	if e = first.Start(); e != nil {
		cancel()
		return fmt.Errorf("ssh_tunnel_failed")
	}
	// OpenSSH signals failure immediately for auth/forwarding errors. A brief
	// startup grace is preferable to claiming an operational connection on Start.
	result := make(chan error, 1)
	go func() { result <- first.Wait() }()
	select {
	case <-result:
		cancel()
		return fmt.Errorf("ssh_auth_failed_unlock_key")
	case <-time.After(400 * time.Millisecond):
	}
	a.tunnel.cancel = cancel
	a.tunnel.State = "connecting"
	a.tunnel.Browser = "http://127.0.0.1:" + strconv.Itoa(web)
	a.tunnel.Gateway = "https://127.0.0.1:" + strconv.Itoa(gateway)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				conn, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", web), 200*time.Millisecond)
				if e == nil {
					conn.Close()
					a.tunnel.mu.Lock()
					if work.Err() == nil {
						a.tunnel.State = "connected"
					}
					a.tunnel.mu.Unlock()
				}
			}
		}
	}()
	go func() {
		defer func() {
			cancel()
			a.tunnel.mu.Lock()
			a.tunnel.cancel = nil
			a.tunnel.State = "disconnected"
			a.tunnel.mu.Unlock()
		}()
		for {
			select {
			case <-work.Done():
				first.Process.Kill()
				<-result
				return
			case <-result:
			}
			if work.Err() != nil {
				return
			}
			a.tunnel.mu.Lock()
			a.tunnel.State = "reconnecting"
			a.tunnel.mu.Unlock()
			select {
			case <-work.Done():
				return
			case <-time.After(3 * time.Second):
			}
			first = exec.CommandContext(work, "ssh", args...)
			if e := first.Start(); e != nil {
				return
			}
			result = make(chan error, 1)
			go func(cmd *exec.Cmd, ch chan error) { ch <- cmd.Wait() }(first, result)
			a.tunnel.mu.Lock()
			a.tunnel.State = "connecting"
			a.tunnel.mu.Unlock()
		}
	}()
	return nil
}
func (a *localAgent) connectTunnel(w http.ResponseWriter, r *http.Request) {
	var p tunnelProfile
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || !p.Target.valid() {
		writeLocalJSON(w, 400, map[string]string{"error": "invalid_ssh_target"})
		return
	}
	if e := a.startTunnel(r.Context(), p); e != nil {
		writeLocalJSON(w, 409, map[string]string{"error": e.Error()})
		return
	}
	data, _ := json.Marshal(p)
	if writePrivate(filepath.Join(a.dir, "central-tunnel.json"), data) != nil {
		a.tunnel.mu.Lock()
		a.tunnel.cancel()
		a.tunnel.mu.Unlock()
		writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
		return
	}
	writeLocalJSON(w, 201, a.tunnelStatus())
}
func (a *localAgent) disconnectTunnel(w http.ResponseWriter, r *http.Request) {
	a.tunnel.mu.Lock()
	if a.tunnel.cancel != nil {
		a.tunnel.cancel()
	}
	a.tunnel.mu.Unlock()
	_ = os.Remove(filepath.Join(a.dir, "central-tunnel.json"))
	writeLocalJSON(w, 200, map[string]bool{"disconnected": true})
}
