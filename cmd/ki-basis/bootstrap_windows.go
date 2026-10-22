package main

import "os/exec"

func detachBootstrap(cmd *exec.Cmd) {}

func stopBootstrap(cmd *exec.Cmd) { cmd.Process.Kill(); cmd.Wait() }
