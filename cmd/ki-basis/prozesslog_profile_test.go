package main

import (
	"encoding/json"
	"localaiserver/internal/config"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProzesslogProfilePreservesInstallation(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.json")
	output := filepath.Join(dir, "profile.json")
	c := config.Config{Listen: "127.0.0.1:8080", Database: "customer.db", TimeoutSeconds: 180, MaxBodyBytes: 1 << 20, MaxOutputTokens: 2048, Keys: []config.Key{{ID: "app", SHA256: strings.Repeat("a", 64)}}, Nodes: []config.Node{{ID: "n", URL: "http://127.0.0.1:8081", Model: "chat", UpstreamModel: "engine", Revision: "fixed", Capacity: 1}}}
	original, _ := json.Marshal(c)
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--config", source, "--output", output}
	if err := prozesslogProfileCommand(args); err != nil {
		t.Fatal(err)
	}
	next, err := config.Load(output)
	if err != nil {
		t.Fatal(err)
	}
	if next.TimeoutSeconds != 900 || next.MaxBodyBytes != 16<<20 || next.MaxOutputTokens != config.DefaultOutputTokens || next.Nodes[0].Capacity != 1 || next.Database != c.Database || !reflect.DeepEqual(next.Keys[0], c.Keys[0]) {
		t.Fatal("incorrect migration")
	}
	actual, _ := os.ReadFile(source)
	if string(actual) != string(original) {
		t.Fatal("source modified")
	}
	if err := prozesslogProfileCommand(args); err == nil {
		t.Fatal("existing output overwritten")
	}
}
