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
	EngineSHA256      string `json:"engine_sha256,omitempty"`
	Template          string `json:"template,omitempty"`
	TemplateSHA256    string `json:"template_sha256,omitempty"`
	ContextPerRequest int    `json:"context_per_request,omitempty"`
	Projector         string `json:"projector,omitempty"`
	ProjectorSHA256   string `json:"projector_sha256,omitempty"`
}

func (o EngineOptions) Validate() error {
	if o.ContextPerRequest != 0 && (o.ContextPerRequest < 32768 || o.ContextPerRequest > 131072) {
		return errors.New("invalid context per request")
	}
	if o.EngineSHA256 != "" && !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(o.EngineSHA256) {
		return errors.New("invalid engine SHA-256")
	}
	if (o.Template == "") != (o.TemplateSHA256 == "") || o.Template != "" && (!filepath.IsAbs(o.Template) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(o.TemplateSHA256)) {
		return errors.New("template needs absolute path and SHA-256")
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
	if totalContext < 32768*capacity || totalContext > 131072*capacity {
		return nil, errors.New("invalid total context")
	}
	args := []string{"--ctx-size", strconv.Itoa(totalContext), "--parallel", strconv.Itoa(capacity), "--jinja", "--reasoning-format", "deepseek", "--cache-ram", "0", "--no-context-shift", "--log-disable", "--predict", "-1"}
	if o.Template != "" {
		hash, err := fileHash(ctx, o.Template)
		if err != nil || hash != o.TemplateSHA256 {
			return nil, errors.New("template hash mismatch")
		}
		args = append(args, "--chat-template-file", o.Template)
	}
	if o.Projector != "" {
		hash, err := fileHash(ctx, o.Projector)
		if err != nil || hash != o.ProjectorSHA256 {
			return nil, errors.New("projector hash mismatch")
		}
		args = append(args, "--mmproj", o.Projector)
	}
	return args, nil
}

func FileSHA256(ctx context.Context, path string) (string, error) { return fileHash(ctx, path) }
func (o EngineOptions) VerifyBinary(ctx context.Context, binary string) error {
	if o.EngineSHA256 == "" {
		return errors.New("engine_revision_unpinned")
	}
	actual, err := fileHash(ctx, binary)
	if err != nil || actual != o.EngineSHA256 {
		return errors.New("engine_revision_mismatch")
	}
	return nil
}

func (o EngineOptions) verifyArtifacts(ctx context.Context, binary, model, modelHash string, capacity, totalContext int) ([]string, error) {
	actual, err := fileHash(ctx, model)
	if err != nil || actual != modelHash {
		return nil, errors.New("hash_mismatch")
	}
	if err := o.VerifyBinary(ctx, binary); err != nil {
		return nil, err
	}
	return o.arguments(ctx, capacity, totalContext)
}
