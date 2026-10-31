package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"localaiserver/internal/agent"
	"localaiserver/internal/control"
)

func bootstrapAgent(args []string) error {
	flags := flag.NewFlagSet("agent-bootstrap", flag.ContinueOnError)
	dir := flags.String("dir", "", "private agent directory")
	bundlePath := flags.String("bundle", "", "one-time connection file")
	runtimePath := flags.String("runtime", "", "optional runtime archive")
	installExecutable := flags.String("install-executable", "", "install the transferred executable under the bootstrap lock")
	resume := flags.Bool("resume", false, "resume the matching enrolled identity without replacing credentials")
	service := flags.Bool("service", false, "install a systemd service when supported and running as root")
	if e := flags.Parse(args); e != nil {
		return e
	}
	absolute, e := filepath.Abs(*dir)
	if e != nil || *dir == "" {
		return errors.New("agent_directory_required")
	}
	if e = os.MkdirAll(absolute, 0700); e != nil {
		return e
	}
	unlock, e := lockBootstrap(absolute)
	if e != nil {
		return e
	}
	defer unlock()
	identityPath := filepath.Join(absolute, "agent.json")
	_, identityErr := os.Stat(identityPath)
	existing := identityErr == nil
	if identityErr != nil && !os.IsNotExist(identityErr) {
		return errors.New("agent_identity_unreadable")
	}
	if *resume && !existing {
		return errors.New("agent_identity_missing")
	}
	if existing && !*resume {
		return errors.New("agent_identity_already_exists")
	}
	raw, e := os.ReadFile(*bundlePath)
	if e != nil || len(raw) > 65536 {
		return errors.New("invalid_connection_file")
	}
	var bundle control.Bundle
	if json.Unmarshal(raw, &bundle) != nil {
		return errors.New("invalid_connection_file")
	}
	if existing && (readRestartMode(absolute) == "systemd" || readRestartMode(absolute) == "systemd_pending") {
		*service = true
	}
	if *service && !serverServiceAvailable() {
		return errors.New("agent_service_unavailable_or_failed")
	}
	if existing {
		state, err := agent.Load(identityPath)
		if err != nil || state.Identity.Node.ID != bundle.NodeID || state.Gateway != bundle.Gateway || strings.TrimSpace(state.Identity.CA) != strings.TrimSpace(bundle.CA) {
			return errors.New("agent_identity_mismatch")
		}
		if _, running := reuseSetupSession(absolute); running {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"connected": true, "node_id": bundle.NodeID, "already_running": true, "restart_mode": "existing"})
		}
	}
	settings := agentSettings{Models: filepath.Join(absolute, "models")}
	if existing {
		raw, err := os.ReadFile(filepath.Join(absolute, "settings.json"))
		if err != nil || json.Unmarshal(raw, &settings) != nil {
			return errors.New("agent_settings_invalid")
		}
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	if *installExecutable != "" {
		destination, err := filepath.Abs(*installExecutable)
		if err != nil || existing || filepath.Dir(destination) != filepath.Dir(absolute) || filepath.Base(destination) != "ki-basis" {
			return errors.New("agent_install_destination_invalid")
		}
		if err = os.Rename(exe, destination); err != nil {
			return err
		}
		exe = destination
	}
	if !existing {
		for _, p := range []string{filepath.Join(filepath.Dir(exe), "runtime", "llama-server"), "/workspace/llama-runtime/llama-server", "/workspace/llama.cpp/build/bin/llama-server"} {
			if info, e := os.Stat(p); e == nil && info.Mode().IsRegular() {
				settings.Binary = p
				break
			}
		}
		if settings.Binary == "" {
			settings.Binary, _ = exec.LookPath("llama-server")
		}
		if *runtimePath != "" {
			if _, e = os.Stat(*runtimePath); e == nil {
				stage, e := os.MkdirTemp(absolute, "runtime-")
				if e != nil {
					return e
				}
				defer func() {
					if _, err := os.Stat(identityPath); os.IsNotExist(err) {
						os.RemoveAll(stage)
					}
				}()
				settings.Binary, e = extractRuntime(*runtimePath, stage)
				if e != nil {
					os.RemoveAll(stage)
					return e
				}
			} else if !os.IsNotExist(e) {
				return e
			}
		}
		for port := 8082; port < 8182; port++ {
			l, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if e == nil {
				l.Close()
				settings.Engine = fmt.Sprintf("http://127.0.0.1:%d", port)
				break
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	inspectionBundle := bundle
	if existing {
		inspectionBundle.Expires = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	}
	report := inspectSetup(ctx, settings, &inspectionBundle, false)
	if !report.Ready {
		return &bootstrapFailure{Code: "setup_checks_failed", Checks: report.Checks}
	}
	ca := filepath.Join(absolute, "join-ca.crt")
	if e = os.WriteFile(ca, []byte(bundle.CA), 0600); e != nil {
		return e
	}
	defer os.Remove(ca)
	data, _ := json.Marshal(settings)
	if e = writePrivate(filepath.Join(absolute, "settings.json"), data); e != nil {
		return e
	}
	if !existing {
		if e = agent.Join(ctx, bundle.Gateway, bundle.Token, ca, identityPath); e != nil {
			return errors.New("agent_enrollment_failed")
		}
	}
	// Remove the consumed token file. It is never placed in command arguments.
	_ = os.Remove(*bundlePath)
	if *runtimePath != "" {
		_ = os.Remove(*runtimePath)
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if *service {
		if e = writeRestartMode(absolute, "systemd_pending"); e != nil {
			return e
		}
		if !installServerService(exe, absolute, port) {
			return errors.New("agent_service_unavailable_or_failed")
		}
		if !waitSetupSession(absolute) {
			return errors.New("agent_service_start_failed")
		}
		if e = writeRestartMode(absolute, "systemd"); e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"connected": true, "node_id": bundle.NodeID, "setup_port": port, "data_directory": absolute, "service": true, "restart_mode": "systemd"})
	}
	log, e := os.OpenFile(filepath.Join(absolute, "setup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd := exec.Command(exe, "agent-ui", "--dir", absolute, "--port", fmt.Sprint(port), "--open-browser=false", "--quiet-setup")
	cmd.Stdout = log
	cmd.Stderr = log
	detachBootstrap(cmd)
	if e = cmd.Start(); e != nil {
		return e
	}
	started := waitSetupSession(absolute)
	if !started {
		stopBootstrap(cmd)
		return errors.New("agent_background_start_failed")
	}
	if e = writeRestartMode(absolute, "manual"); e != nil {
		stopBootstrap(cmd)
		return e
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"connected": true, "node_id": bundle.NodeID, "pid": pid, "setup_port": port, "data_directory": absolute, "service": false, "restart_mode": "manual"})
}

// Only structured setup diagnostics cross SSH; never raw subprocess stderr.
type bootstrapFailure struct {
	Code   string       `json:"error"`
	Checks []setupCheck `json:"checks,omitempty"`
}

func (e *bootstrapFailure) Error() string { return e.Code }
func waitSetupSession(dir string) bool {
	for i := 0; i < 40; i++ {
		if _, ok := reuseSetupSession(dir); ok {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// Host-specific launch metadata is deliberately excluded from portable backups.
func writeRestartMode(dir, mode string) error {
	raw, _ := json.Marshal(map[string]string{"mode": mode})
	return writePrivate(filepath.Join(dir, "deployment.json"), raw)
}
func readRestartMode(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "deployment.json"))
	var info struct {
		Mode string `json:"mode"`
	}
	if err != nil || json.Unmarshal(raw, &info) != nil {
		return "unknown"
	}
	switch info.Mode {
	case "manual", "systemd", "systemd_pending":
		return info.Mode
	}
	return "unknown"
}

func lockBootstrap(dir string) (func(), error) {
	release, err := lockProcessFile(dir, ".bootstrap.lock")
	if err != nil {
		if err.Error() == "agent_process_in_use" {
			return nil, errors.New("agent_setup_in_progress")
		}
		return nil, errors.New("agent_storage_unavailable")
	}
	return release, nil
}
