//go:build !windows

package main

import "os/exec"

func configureRuntimeProbe(cmd *exec.Cmd) {}
