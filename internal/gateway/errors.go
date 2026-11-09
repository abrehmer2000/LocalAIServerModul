package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func canonicalCode(code string) string {
	switch code {
	case "invalid_api_key":
		return "unauthorized"
	case "admin_required", "cross_site_request":
		return "forbidden"
	case "invalid_user_id", "invalid_content_type", "user_mismatch", "upstream_invalid_request", "not_found":
		return "invalid_request"
	case "audit_unavailable", "upstream_unavailable":
		return "model_unavailable"
	case "invalid_upstream", "internal_error", "stream_interrupted":
		return "upstream_failure"
	case "timeout_or_cancelled":
		return "timeout"
	case "client_disconnected":
		return "client_disconnect"
	default:
		return code
	}
}
func retryable(code string) bool {
	return code == "model_busy" || code == "model_loading" || code == "model_unavailable"
}

// Only bounded engine error metadata is classified. Raw engine messages never
// leave this function or enter audit/log output.
func classifyUpstream(resp *http.Response) (int, string) {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var obj struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(b, &obj)
	code := obj.Error.Code
	switch code {
	case "context_length_exceeded":
		return 400, code
	case "vision_not_supported":
		return 422, code
	case "model_loading", "model_unavailable":
		return 503, code
	case "model_busy":
		return 503, code
	case "timeout":
		return 504, code
	}
	// llama.cpp releases that report a type/message instead of an OpenAI code.
	text := strings.ToLower(obj.Error.Type + " " + obj.Error.Message)
	if resp.StatusCode == 400 || resp.StatusCode == 413 || resp.StatusCode == 422 {
		if strings.Contains(text, "context") && (strings.Contains(text, "exceed") || strings.Contains(text, "too long")) {
			return 400, "context_length_exceeded"
		}
		if strings.Contains(text, "vision") || strings.Contains(text, "image") && strings.Contains(text, "not support") {
			return 422, "vision_not_supported"
		}
	}
	if resp.StatusCode == 503 && strings.Contains(text, "load") {
		return 503, "model_loading"
	}
	if resp.StatusCode == 504 {
		return 504, "timeout"
	}
	return upstreamError(resp.StatusCode)
}
func contextOutcome(ctx context.Context) string {
	if ctx.Err() == context.Canceled {
		return "client_disconnect"
	}
	return "timeout"
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	apiError(w, status, code, message)
}
