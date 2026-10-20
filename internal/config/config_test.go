package config

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestTransportValidation(t *testing.T) {
	h := sha256.Sum256([]byte("key"))
	for _, tt := range []struct {
		url, listen string
		valid       bool
	}{
		{"http://127.0.0.1:8081", "127.0.0.1:8080", true},
		{"https://server.example:8081", "127.0.0.1:8080", true},
		{"http://server.example:8081", "127.0.0.1:8080", false},
		{"http://127.0.0.1:8081", "0.0.0.0:8080", false},
		{"https://name:secret@server.example", "127.0.0.1:8080", false},
		{"https://server.example?token=secret", "127.0.0.1:8080", false},
	} {
		c := Config{Listen: tt.listen, TimeoutSeconds: 10, MaxBodyBytes: 1024, MaxOutputTokens: 128, Keys: []Key{{ID: "app", SHA256: hex.EncodeToString(h[:])}}, Nodes: []Node{{ID: "n", URL: tt.url, Model: "m", UpstreamModel: "m", Revision: "r", Capacity: 1}}}
		if err := c.Validate(); (err == nil) != tt.valid {
			t.Errorf("url=%s listen=%s err=%v", tt.url, tt.listen, err)
		}
	}
}
