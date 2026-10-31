package agent

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

func configureProcess(cmd *exec.Cmd) {}
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
	if err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
