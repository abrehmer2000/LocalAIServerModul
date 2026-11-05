package agent

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
)

// EngineOptions are local operator settings, never commands received from a peer.
// ContextPerRequest is multiplied by the number of llama.cpp slots.
type EngineOptions struct {
	ContextPerRequest int    `json:"context_per_request,omitempty"`
	Projector         string `json:"projector,omitempty"`
	ProjectorSHA256   string `json:"projector_sha256,omitempty"`
}

func (o EngineOptions) Validate() error {
	if o.ContextPerRequest != 0 && (o.ContextPerRequest < 128 || o.ContextPerRequest > 131072) {
		return errors.New("invalid context per request")
	}
	if o.Projector == "" && o.ProjectorSHA256 == "" {
		return nil
	}
	if !filepath.IsAbs(o.Projector) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(o.ProjectorSHA256) {
		return errors.New("projector needs absolute path and SHA-256")
	}
	return nil
}

func (o EngineOptions) arguments(ctx context.Context, capacity, totalContext int) ([]string, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if capacity < 1 || capacity > 128 {
		return nil, errors.New("invalid capacity")
	}
	if totalContext == 0 {
		perRequest := o.ContextPerRequest
		if perRequest == 0 {
			perRequest = 32768
		}
		totalContext = perRequest * capacity
	}
	if totalContext < 128*capacity || totalContext > 131072*capacity {
		return nil, errors.New("invalid total context")
	}
	args := []string{"--ctx-size", strconv.Itoa(totalContext), "--parallel", strconv.Itoa(capacity), "--jinja", "--reasoning-format", "deepseek", "--cache-ram", "0", "--no-context-shift"}
	if o.Projector != "" {
		hash, err := fileHash(ctx, o.Projector)
		if err != nil || hash != o.ProjectorSHA256 {
			return nil, errors.New("projector hash mismatch")
		}
		args = append(args, "--mmproj", o.Projector)
	}
	return args, nil
}
