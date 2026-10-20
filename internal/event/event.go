// Package event defines the metadata-only audit contract independently of storage.
package event

type Event struct {
	Actor            string `json:"actor,omitempty"`
	RequestID        string `json:"request_id,omitempty"`
	Type             string `json:"type"`
	KeyID            string `json:"key_id,omitempty"`
	UserID           string `json:"user_id,omitempty"`
	Model            string `json:"model,omitempty"`
	Revision         string `json:"revision,omitempty"`
	NodeID           string `json:"node_id,omitempty"`
	Status           int    `json:"status,omitempty"`
	Outcome          string `json:"outcome,omitempty"`
	DurationMS       int64  `json:"duration_ms,omitempty"`
	PromptTokens     *int64 `json:"prompt_tokens,omitempty"`
	CompletionTokens *int64 `json:"completion_tokens,omitempty"`
}
