package agent

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"time"
)

// RunEngine uses fixed arguments, never a shell, and stops the owned process tree on cancellation.
func (r *Runner) RunEngine(ctx context.Context, binary, model string, contextSize int) error {
	r.mu.RLock()
	n := r.state.Identity.Node
	r.mu.RUnlock()
	if n.ModelSHA256 == "" {
		return errors.New("managed engine requires model_sha256 in its join grant")
	}
	u, _ := url.Parse(r.upstream.String())
	port := u.Port()
	if port == "" {
		return errors.New("engine port and valid context size required")
	}
	contextPerSlot := r.Runtime.ContextPerRequest
	if contextPerSlot == 0 {
		contextPerSlot = 32768
	}
	if n.Capacity < 1 {
		return errors.New("invalid engine capacity")
	}
	if contextSize != 0 {
		contextPerSlot = contextSize / n.Capacity
	}
	r.actualContext.Store(int64(contextPerSlot))
	for attempt := 0; ctx.Err() == nil; attempt++ {
		runtimeArgs, err := r.Runtime.verifyArtifacts(ctx, binary, model, n.ModelSHA256, n.Capacity, contextSize)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, binary, append([]string{"--model", model, "--alias", n.UpstreamModel, "--host", u.Hostname(), "--port", port}, runtimeArgs...)...)
		cmd.Env = RuntimeEnvironment(binary)
		configureProcess(cmd)
		cmd.Cancel = func() error { return killProcessTree(cmd) }
		cmd.WaitDelay = 3 * time.Second
		// Engine output can contain customer data. Do not forward it into agent logs.
		err = startProcess(cmd)
		if err == nil {
			r.engineStarts.Add(1)
			err = waitProcess(cmd)
		}
		if ctx.Err() != nil {
			return nil
		}
		if attempt > 4 {
			attempt = 4
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}
