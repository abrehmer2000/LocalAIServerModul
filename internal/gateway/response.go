package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

var errSchemaTooLarge = errors.New("schema exceeds 64 KiB")

type responseFormat struct {
	Type       string `json:"type"`
	JSONSchema *struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Strict      *bool           `json:"strict,omitempty"`
		Schema      json.RawMessage `json:"schema"`
	} `json:"json_schema,omitempty"`
	compiled *jsonschema.Schema
}

func (f *responseFormat) prepare() error {
	if f == nil {
		return nil
	}
	switch f.Type {
	case "text", "json_object":
		if f.JSONSchema != nil {
			return errors.New("unexpected json_schema")
		}
	case "json_schema":
		if f.JSONSchema != nil && len(f.JSONSchema.Schema) > 64<<10 {
			return errSchemaTooLarge
		}
		if f.JSONSchema == nil || f.JSONSchema.Name == "" || len(f.JSONSchema.Name) > 64 || len(f.JSONSchema.Schema) > 64<<10 {
			return errors.New("invalid json_schema")
		}
		c := jsonschema.NewCompiler()
		// Compilation must never fetch external references or read local files.
		c.LoadURL = func(string) (io.ReadCloser, error) { return nil, errors.New("external schema references forbidden") }
		if err := c.AddResource("https://gateway.invalid/schema", bytes.NewReader(f.JSONSchema.Schema)); err != nil {
			return errors.New("invalid schema")
		}
		var err error
		f.compiled, err = c.Compile("https://gateway.invalid/schema")
		if err != nil {
			return errors.New("unsupported or invalid schema")
		}
	default:
		return errors.New("unsupported response_format")
	}
	return nil
}

func validateCompletion(data []byte, f *responseFormat) error {
	var obj struct {
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &obj) != nil || len(obj.Choices) != 1 || obj.Choices[0].Message.Content == nil {
		return errors.New("completion content missing")
	}
	content := *obj.Choices[0].Message.Content
	// An empty completion may signal a thinking-token budget exhaustion. Let the
	// application perform its one explicit retry without thinking.
	if strings.Contains(content, "<think>") || strings.Contains(content, "</think>") {
		return errors.New("reasoning must be separate")
	}
	if f == nil || f.Type == "text" {
		return nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid structured JSON")
	}
	if f.Type == "json_object" {
		if _, ok := value.(map[string]any); !ok {
			return errors.New("JSON object required")
		}
	}
	if f.compiled != nil {
		return f.compiled.Validate(value)
	}
	return nil
}

func upstreamError(status int) (int, string) {
	switch status {
	case 429:
		return 429, "upstream_failure"
	case 503:
		return 503, "model_unavailable"
	case 400, 413, 415, 422:
		return status, "invalid_request"
	default:
		return 502, "upstream_failure"
	}
}

func retryAfter(value string) string {
	seconds, err := strconv.Atoi(value)
	if err != nil {
		if deadline, e := time.Parse(time.RFC1123, value); e == nil {
			seconds = int(time.Until(deadline).Seconds())
		} else {
			seconds = 1
		}
	}
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 30 {
		seconds = 30
	}
	return fmt.Sprint(seconds)
}
