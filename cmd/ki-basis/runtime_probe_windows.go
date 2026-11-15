package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// A console engine's version check must not open a terminal over the setup UI.
func configureRuntimeProbe(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
