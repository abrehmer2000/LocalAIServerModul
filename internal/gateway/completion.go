package gateway

import (
	"encoding/json"
	"errors"
	"localaiserver/internal/event"
	"strings"
	"unicode/utf8"
)

// prepareCompletion validates the transport envelope separately from generated
// text. A malformed/truncated model JSON string is never a gateway outage.
func prepareCompletion(data []byte, request chatRequest, model, id string, e *event.Event) ([]byte, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("invalid UTF-8 envelope")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil || obj == nil || obj["error"] != nil {
		return nil, errors.New("invalid envelope")
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(obj["choices"], &choices) != nil || len(choices) != 1 {
		return nil, errors.New("invalid choices")
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(choices[0]["message"], &msg) != nil || msg == nil {
		return nil, errors.New("invalid message")
	}
	var content string
	if msg["content"] == nil || string(msg["content"]) == "null" || json.Unmarshal(msg["content"], &content) != nil {
		return nil, errors.New("content must be a string")
	}
	var usage struct {
		Prompt     *int64 `json:"prompt_tokens"`
		Completion *int64 `json:"completion_tokens"`
	}
	if json.Unmarshal(obj["usage"], &usage) != nil || usage.Prompt == nil || usage.Completion == nil || *usage.Prompt < 0 || *usage.Completion < 0 {
		return nil, errors.New("engine token usage required")
	}
	var finish string
	if json.Unmarshal(choices[0]["finish_reason"], &finish) != nil || (finish != "stop" && finish != "length" && finish != "content_filter") {
		return nil, errors.New("engine finish reason required")
	}
	// The supported legacy wrapper occurs at the start, never in ordinary quotes.
	wrapped := strings.TrimLeft(content, " \t\r\n")
	if strings.HasPrefix(wrapped, "<think>") {
		content = wrapped
		if end := strings.Index(content, "</think>"); end >= 0 {
			msg["reasoning_content"], _ = json.Marshal(content[len("<think>"):end])
			content = content[end+len("</think>"):]
		} else {
			msg["reasoning_content"], _ = json.Marshal(strings.TrimPrefix(content, "<think>"))
			content = ""
			finish = "length"
		}
		msg["content"], _ = json.Marshal(content)
	}
	limit := request.MaxTokens
	if request.MaxCompletionTokens != nil {
		limit = request.MaxCompletionTokens
	}
	if limit != nil && *usage.Completion >= int64(*limit) {
		finish = "length"
	}
	choices[0]["finish_reason"], _ = json.Marshal(finish)
	choices[0]["message"], _ = json.Marshal(msg)
	obj["choices"], _ = json.Marshal(choices)
	var upstreamID string
	if json.Unmarshal(obj["id"], &upstreamID) != nil || upstreamID == "" {
		obj["id"], _ = json.Marshal("chatcmpl-" + id)
	}
	obj["model"], _ = json.Marshal(model)
	// Postvalidation is metadata only. In particular length never requires valid
	// generated JSON, and a quality failure does not trip the node circuit breaker.
	if finish != "length" && request.ResponseFormat != nil && request.ResponseFormat.Type != "text" {
		if validateCompletion(data, request.ResponseFormat) != nil {
			e.Validation = "output_nonconforming"
		}
	}
	e.PromptTokens = usage.Prompt
	e.CompletionTokens = usage.Completion
	return json.Marshal(obj)
}
