package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
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
	f, err := os.Open(model)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != n.ModelSHA256 {
		return errors.New("model file SHA-256 differs from enrollment grant")
	}
	u, _ := url.Parse(r.upstream.String())
	port := u.Port()
	if port == "" {
		return errors.New("engine port and valid context size required")
	}
	if err := r.Runtime.VerifyBinary(ctx, binary); err != nil {
		return err
	}
	runtimeArgs, err := r.Runtime.arguments(ctx, n.Capacity, contextSize)
	if err != nil {
		return err
	}
	for attempt := 0; ctx.Err() == nil; attempt++ {
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
