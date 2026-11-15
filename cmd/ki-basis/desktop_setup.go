package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"localaiserver/internal/agent"
	"localaiserver/internal/catalog"
	"localaiserver/internal/control"
	"localaiserver/internal/management"
)

func (d *desktopApp) call(ctx context.Context, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", d.gatewayURL+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("desktop_gateway_unavailable")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+d.key)
	request.Header.Set("X-User-ID", "desktop-setup")
	response, err := d.client.Do(request)
	if err != nil {
		return desktopOperationError(err, "desktop_gateway_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var rejection struct {
			Error json.RawMessage `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&rejection)
		// Upstream bodies can contain untrusted diagnostic text. Only curated codes
		// cross the setup boundary, never raw HTTP response or engine output.
		var code string
		_ = json.Unmarshal(rejection.Error, &code)
		switch code {
		case "catalog_unreachable", "catalog_access_required", "catalog_invalid", "catalog_missing_integrity", "model_hash_conflict", "node_not_ready_or_busy":
			return errors.New(code)
		}
		return errors.New("desktop_gateway_request_failed")
	}
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return err
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output) != nil {
		return errors.New("desktop_gateway_response_invalid")
	}
	return nil
}

func (d *desktopApp) prepare(ctx context.Context) error {
	a := d.agent
	a.mu.Lock()
	if a.runner != nil {
		a.mu.Unlock()
		return nil
	}
	settings := a.settings
	a.mu.Unlock()
	if info, err := os.Stat(settings.Binary); err != nil || !info.Mode().IsRegular() {
		binary, err := installRecommendedRuntime(ctx, a.dir, func(progress runtimeDownloadProgress) { d.mu.Lock(); d.progress = progress; d.mu.Unlock() })
		if err != nil {
			return err
		}
		settings.Binary = binary
	}
	if settings.Models == "" {
		settings.Models = filepath.Join(a.dir, "models")
	}
	if settings.Engine == "" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return errors.New("desktop_engine_port_unavailable")
		}
		settings.Engine = "http://" + listener.Addr().String()
		listener.Close()
	}
	settings.Runtime.ContextPerRequest = 32768
	if err := pinSettings(ctx, &settings); err != nil {
		return desktopOperationError(err, "desktop_runtime_verification_failed")
	}
	raw, _ := json.Marshal(settings)
	// Persist selected runtime before enrollment, so an interrupted connection can
	// reuse the verified package rather than download it again.
	if err := writePrivate(filepath.Join(a.dir, "settings.json"), raw); err != nil {
		return errors.New("desktop_agent_settings_storage_failed")
	}
	a.mu.Lock()
	a.settings = settings
	a.mu.Unlock()
	if _, err := os.Stat(filepath.Join(a.dir, "agent.json")); os.IsNotExist(err) {
		var bundle control.Bundle
		enroll := map[string]any{"id": desktopNodeID, "gateway": d.gatewayURL, "model": catalog.DefaultAlias, "capacity": 1, "engine_protocol": "llama.cpp", "context_per_request": 32768}
		requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err = d.call(requestCtx, "/admin/enroll", enroll, &bundle); err != nil {
			return err
		}
		if err = agent.Join(requestCtx, bundle.Gateway, bundle.Token, d.config.ControlCACert, filepath.Join(a.dir, "agent.json")); err != nil {
			return desktopOperationError(err, "desktop_agent_enrollment_failed")
		}
	} else if err != nil {
		return errors.New("desktop_agent_identity_invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	if a.runner == nil {
		if err := a.start(); err != nil {
			a.mu.Unlock()
			return errors.New("desktop_agent_start_failed")
		}
	}
	a.mu.Unlock()
	d.mu.Lock()
	d.stage = "model"
	d.mu.Unlock()
	return nil
}
func (d *desktopApp) modelAction(ctx context.Context, action string) error {
	d.agent.mu.Lock()
	connected := d.agent.runner != nil
	report := management.Report{}
	if connected && d.agent.runner.Manager != nil {
		report = d.agent.runner.Manager.Report()
	}
	d.agent.mu.Unlock()
	if !connected {
		return errors.New("desktop_prepare_first")
	}
	if action != "stop" {
		d.mu.Lock()
		pending := d.modelGeneration != "" && report.Generation != d.modelGeneration
		d.mu.Unlock()
		if action == "start" && report.State == "running" && !pending {
			return nil
		}
		if pending || report.State == "downloading" || report.State == "verifying" || report.State == "starting" || report.State == "restarting" || report.State == "running" {
			return errors.New("desktop_model_operation_in_progress")
		}
	}
	timeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var plan management.Plan
	if err := d.call(timeout, "/admin/nodes/"+desktopNodeID+"/action", map[string]string{"action": action, "gpu": "cpu"}, &plan); err != nil {
		return err
	}
	d.mu.Lock()
	d.modelGeneration = plan.Generation
	d.modelActionName = action
	d.mu.Unlock()
	return nil
}
func (d *desktopApp) testAnswer(ctx context.Context) error {
	d.mu.Lock()
	d.agent.mu.Lock()
	report := management.Report{}
	if d.agent.runner != nil && d.agent.runner.Manager != nil {
		report = d.agent.runner.Manager.Report()
	}
	d.agent.mu.Unlock()
	ready := d.readyForReport(report)
	d.mu.Unlock()
	if !ready {
		return errors.New("desktop_model_not_ready")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	input := map[string]any{"model": catalog.DefaultAlias, "messages": []map[string]string{{"role": "user", "content": "Antworte mit einem kurzen deutschen Begrüßungssatz."}}, "max_tokens": 128, "stream": false, "chat_template_kwargs": map[string]bool{"enable_thinking": false}}
	var output struct {
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := d.call(requestCtx, "/v1/chat/completions", input, &output); err != nil {
		return err
	}
	if len(output.Choices) != 1 || output.Choices[0].Finish != "stop" || strings.TrimSpace(output.Choices[0].Message.Content) == "" {
		return errors.New("desktop_test_no_complete_answer")
	}
	answer := output.Choices[0].Message.Content
	if len(answer) > 8192 {
		return errors.New("desktop_test_response_too_large")
	}
	d.mu.Lock()
	d.answer = answer
	d.stage = "complete"
	d.mu.Unlock()
	return nil
}
