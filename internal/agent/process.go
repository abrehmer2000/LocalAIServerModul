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
	"strconv"
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
	if port == "" || contextSize < 128 || contextSize > 131072 {
		return errors.New("engine port and valid context size required")
	}
	for attempt := 0; attempt < 5; attempt++ {
		cmd := exec.CommandContext(ctx, binary, "--model", model, "--alias", n.UpstreamModel, "--host", u.Hostname(), "--port", port, "--ctx-size", strconv.Itoa(contextSize), "--parallel", strconv.Itoa(n.Capacity))
		cmd.Env = RuntimeEnvironment(binary)
		configureProcess(cmd)
		cmd.Cancel = func() error { return killProcessTree(cmd) }
		cmd.WaitDelay = 3 * time.Second
		// Engine output can contain customer data. Do not forward it into agent logs.
		err = cmd.Run()
		if ctx.Err() != nil {
			return nil
		}
		if attempt == 4 {
			break
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return errors.New("engine restart limit reached")
}
