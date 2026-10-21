// Package backup defines the shared, portable backup contract for central and agents.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const Agent = "ki-basis-agent-backup"
const Central = "ki-basis-central-backup"

type Manifest struct {
	Format          string            `json:"format"`
	Version         int               `json:"version"`
	CreatedAt       string            `json:"created_at"`
	ContainsSecrets bool              `json:"contains_secrets"`
	ModelsIncluded  bool              `json:"models_included"`
	RuntimeIncluded bool              `json:"runtime_included"`
	Files           map[string]string `json:"files,omitempty"`
}

func Digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func Encode(kind string, files map[string]string) ([]byte, error) {
	return json.Marshal(Manifest{Format: kind, Version: 2, CreatedAt: time.Now().UTC().Format(time.RFC3339), ContainsSecrets: true, Files: files})
}

// Versions 1 and 2 share the same file layout. Version 2 additionally checks every payload.
func Validate(raw []byte, files map[string]string) (string, error) {
	var m Manifest
	if json.Unmarshal(raw, &m) != nil || (m.Version != 1 && m.Version != 2) {
		return "", errors.New("invalid_backup")
	}
	required := []string{}
	allowed := map[string]bool{}
	switch m.Format {
	case Agent:
		required = []string{"agent.json", "settings.json"}
	case Central:
		required = []string{"gateway.db", "config.json", "pki/ca.crt", "pki/ca.key", "pki/gateway.crt", "pki/gateway.key"}
		for _, name := range []string{"access.json", "admin-access.json", "new-api-key.json"} {
			allowed[name] = true
		}
	default:
		return "", errors.New("invalid_backup")
	}
	for _, name := range required {
		allowed[name] = true
		if _, ok := files[name]; !ok {
			return "", errors.New("invalid_backup")
		}
	}
	for name, digest := range files {
		if !allowed[name] || (m.Version == 2 && m.Files[name] != digest) {
			return "", errors.New("invalid_backup")
		}
	}
	if m.Version == 2 && len(m.Files) != len(files) {
		return "", errors.New("invalid_backup")
	}
	return m.Format, nil
}
