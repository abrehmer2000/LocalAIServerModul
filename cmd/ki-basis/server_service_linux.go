package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func installServerService(executable, dir string, port int) bool {
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
	unit := fmt.Sprintf(marker+"[Unit]\nDescription=KI-Basis model agent\nWants=network-online.target\nAfter=network-online.target\n[Service]\nType=simple\nExecStart=%s agent-ui --dir %s --port %d --open-browser=false\nRestart=on-failure\nRestartSec=5\nKillMode=control-group\nTimeoutStopSec=20\n[Install]\nWantedBy=multi-user.target\n", quote(executable), quote(dir), port)
	if os.WriteFile(path, []byte(unit), 0600) != nil {
		return false
	}
	if exec.Command("systemctl", "daemon-reload").Run() != nil {
		return false
	}
	return exec.Command("systemctl", "enable", "--now", "ki-basis-agent.service").Run() == nil
}
