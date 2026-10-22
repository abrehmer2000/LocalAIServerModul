package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Install only a per-user service; no root privileges or machine-wide changes.
func configureAutostart(executable, dir string, enabled bool) error {
	base, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	unitDir := filepath.Join(base, "systemd", "user")
	if err = os.MkdirAll(unitDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(unitDir, "ki-basis-agent.service")
	quote := func(s string) string {
		s = strings.ReplaceAll(s, "\n", "\\n")
		s = strings.ReplaceAll(s, "\r", "\\r")
		s = strings.ReplaceAll(s, "%", "%%")
		s = strings.ReplaceAll(s, "\\", "\\\\")
		s = strings.ReplaceAll(s, "\"", "\\\"")
		return "\"" + s + "\""
	}
	if enabled {
		unit := fmt.Sprintf("[Unit]\nDescription=KI-Basis Agent\nAfter=network-online.target\n[Service]\nExecStart=%s agent-ui --open-browser=false --dir %s\nRestart=on-failure\nRestartSec=5\n[Install]\nWantedBy=default.target\n", quote(executable), quote(dir))
		if err = os.WriteFile(path, []byte(unit), 0600); err != nil {
			return err
		}
		if err = exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
			return fmt.Errorf("user_service_unavailable")
		}
		if err = exec.Command("systemctl", "--user", "enable", "ki-basis-agent.service").Run(); err != nil {
			return fmt.Errorf("user_service_unavailable")
		}
	} else {
		_ = exec.Command("systemctl", "--user", "disable", "ki-basis-agent.service").Run()
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	return nil
}
