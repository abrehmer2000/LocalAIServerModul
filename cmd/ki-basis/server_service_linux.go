package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func serverServiceAvailable() bool {
	if os.Geteuid() != 0 {
		return false
	}
	info, err := os.Stat("/run/systemd/system")
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = exec.LookPath("systemctl")
	return err == nil
}
func serviceCommand(args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", args...).Run() == nil
}
func installServerService(executable, dir string, port int) bool {
	if !serverServiceAvailable() {
		return false
	}
	if os.Geteuid() != 0 {
		return false
	}
	if info, e := os.Stat("/run/systemd/system"); e != nil || !info.IsDir() {
		return false
	}
	path := "/etc/systemd/system/ki-basis-agent.service"
	const marker = "# Managed by KI-Basis setup\n"
	if raw, e := os.ReadFile(path); e == nil && !strings.HasPrefix(string(raw), marker) {
		return false
	} else if e != nil && !os.IsNotExist(e) {
		return false
	}
	quote := func(s string) string {
		s = strings.ReplaceAll(s, "\\", "\\\\")
		s = strings.ReplaceAll(s, "\"", "\\\"")
		s = strings.ReplaceAll(s, "%", "%%")
		s = strings.ReplaceAll(s, "\n", "\\n")
		s = strings.ReplaceAll(s, "\r", "\\r")
		return "\"" + s + "\""
	}
	unit := fmt.Sprintf(marker+"[Unit]\nDescription=KI-Basis model agent\nWants=network-online.target\nAfter=network-online.target\n[Service]\nType=simple\nExecStart=%s agent-ui --dir %s --port %d --open-browser=false --quiet-setup\nRestart=on-failure\nRestartSec=5\nKillMode=control-group\nTimeoutStopSec=20\n[Install]\nWantedBy=multi-user.target\n", quote(executable), quote(dir), port)
	if raw, e := os.ReadFile(path); e == nil && !strings.Contains(string(raw), "ExecStart="+quote(executable)+" agent-ui --dir "+quote(dir)+" --port ") {
		return false
	}
	if writePrivate(path, []byte(unit)) != nil {
		return false
	}
	if !serviceCommand("daemon-reload") {
		return false
	}
	return serviceCommand("enable", "--now", "ki-basis-agent.service") && serviceCommand("is-enabled", "--quiet", "ki-basis-agent.service") && serviceCommand("is-active", "--quiet", "ki-basis-agent.service")
}
