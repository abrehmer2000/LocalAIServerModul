package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"localaiserver/internal/agent"
	"localaiserver/internal/control"
)

func bootstrapAgent(args []string) error {
	flags := flag.NewFlagSet("agent-bootstrap", flag.ContinueOnError)
	dir := flags.String("dir", "", "private agent directory")
	bundlePath := flags.String("bundle", "", "one-time connection file")
	runtimePath := flags.String("runtime", "", "optional runtime archive")
	service := flags.Bool("service", false, "install a systemd service when supported and running as root")
	if e := flags.Parse(args); e != nil {
		return e
	}
	absolute, e := filepath.Abs(*dir)
	if e != nil || *dir == "" {
		return errors.New("agent_directory_required")
	}
	if _, e = os.Stat(filepath.Join(absolute, "agent.json")); !os.IsNotExist(e) {
		return errors.New("agent_identity_already_exists")
	}
	if e = os.MkdirAll(absolute, 0700); e != nil {
		return e
	}
	raw, e := os.ReadFile(*bundlePath)
	if e != nil || len(raw) > 65536 {
		return errors.New("invalid_connection_file")
	}
	var bundle control.Bundle
	if json.Unmarshal(raw, &bundle) != nil {
		return errors.New("invalid_connection_file")
	}
	settings := agentSettings{Models: filepath.Join(absolute, "models")}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	report := inspectSetup(ctx, settings, &bundle, false)
	if !report.Ready {
		return errors.New("setup_checks_failed_runtime_network_or_storage")
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
	if e = agent.Join(ctx, bundle.Gateway, bundle.Token, ca, filepath.Join(absolute, "agent.json")); e != nil {
		return e
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
	if *service && installServerService(exe, absolute, port) {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"connected": true, "node_id": bundle.NodeID, "setup_port": port, "data_directory": absolute, "service": true})
	}
	log, e := os.OpenFile(filepath.Join(absolute, "setup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd := exec.Command(exe, "agent-ui", "--dir", absolute, "--port", fmt.Sprint(port), "--open-browser=false")
	cmd.Stdout = log
	cmd.Stderr = log
	detachBootstrap(cmd)
	if e = cmd.Start(); e != nil {
		return e
	}
	started := false
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	for i := 0; i < 40; i++ {
		res, e := client.Get(fmt.Sprintf("http://127.0.0.1:%d/setup/status", port))
		if e == nil {
			res.Body.Close()
			if res.StatusCode == 401 {
				started = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !started {
		cmd.Process.Kill()
		cmd.Wait()
		return errors.New("agent_background_start_failed")
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"connected": true, "node_id": bundle.NodeID, "pid": pid, "setup_port": port, "data_directory": absolute, "service": false})
}
