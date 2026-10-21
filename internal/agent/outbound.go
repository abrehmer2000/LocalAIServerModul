package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"localaiserver/internal/tunnel"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RunOutbound needs no listening socket. Each worker accepts at most one inference.
func (r *Runner) RunOutbound(ctx context.Context, onError func(error)) {
	r.mu.RLock()
	capacity := r.state.Identity.Node.Capacity
	r.mu.RUnlock()
	var wg sync.WaitGroup
	for i := 0; i < capacity; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.pollLoop(ctx, onError) }()
	}
	wg.Wait()
}
func (r *Runner) gatewayClient() (*http.Client, string, error) {
	r.mu.RLock()
	state, pair := r.state, r.pair
	r.mu.RUnlock()
	c, err := client([]byte(state.Identity.CA), &pair)
	if err != nil {
		return nil, "", err
	}
	c.Timeout = 0
	return c, state.Gateway, nil
}
func (r *Runner) pollLoop(ctx context.Context, onError func(error)) {
	for ctx.Err() == nil {
		err := r.pollOnce(ctx)
		if err != nil && ctx.Err() == nil {
			onError(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}
}
func (r *Runner) pollOnce(ctx context.Context) error {
	c, base, err := r.gatewayClient()
	if err != nil {
		return err
	}
	defer c.CloseIdleConnections()
	pollCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	req, _ := http.NewRequestWithContext(pollCtx, "GET", base+"/internal/tasks", nil)
	resp, err := c.Do(req)
	if err != nil {
		cancel()
		return errors.New("outbound_connection_failed")
	}
	if resp.StatusCode == 204 {
		resp.Body.Close()
		cancel()
		return nil
	}
	var task tunnel.Task
	if resp.StatusCode != 200 {
		resp.Body.Close()
		cancel()
		return errors.New("outbound_request_rejected")
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 24<<20)).Decode(&task)
	resp.Body.Close()
	cancel()
	if err != nil || len(task.ID) != 32 || len(task.Body) > 16<<20 {
		return errors.New("outbound_invalid_task")
	}
	workCtx, stop := context.WithTimeout(ctx, time.Hour)
	defer stop()
	// A separate long poll propagates client disconnects while the engine is generating.
	watchDone := make(chan struct{})
	defer func() { stop(); <-watchDone }()
	go func() {
		defer close(watchDone)
		for workCtx.Err() == nil {
			watchCtx, end := context.WithTimeout(workCtx, 20*time.Second)
			q, _ := http.NewRequestWithContext(watchCtx, "GET", base+"/internal/tasks/"+task.ID+"/cancel", nil)
			reply, e := c.Do(q)
			if e != nil {
				end()
				stop()
				return
			}
			reply.Body.Close()
			end()
			if reply.StatusCode != 204 {
				stop()
				return
			}
		}
	}()
	q, _ := http.NewRequestWithContext(workCtx, "POST", r.upstream.String()+"/v1/chat/completions", bytes.NewReader(task.Body))
	q.Header.Set("Content-Type", "application/json")
	local := &http.Client{Transport: r.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	output, err := local.Do(q)
	status := 502
	contentType := "application/json"
	var body io.ReadCloser = io.NopCloser(bytes.NewBufferString(`{"error":"engine_unavailable"}`))
	if err == nil {
		status = output.StatusCode
		contentType = output.Header.Get("Content-Type")
		body = output.Body
	}
	defer body.Close()
	upload, _ := http.NewRequestWithContext(workCtx, "POST", base+"/internal/tasks/"+task.ID+"/result", body)
	upload.Header.Set("X-Inference-Status", strconv.Itoa(status))
	upload.Header.Set("Content-Type", contentType)
	result, err := c.Do(upload)
	if err != nil {
		return errors.New("outbound_result_interrupted")
	}
	defer result.Body.Close()
	if result.StatusCode != 204 {
		return errors.New("outbound_result_rejected")
	}
	return nil
}
