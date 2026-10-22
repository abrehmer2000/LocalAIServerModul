package backup

import (
	"encoding/json"
	"testing"
)

func TestManifestRejectsTamperingAndMixedBackupRoles(t *testing.T) {
	files := map[string]string{"agent.json": Digest([]byte("identity")), "settings.json": Digest([]byte("settings"))}
	raw, err := Encode(Agent, files)
	if err != nil {
		t.Fatal(err)
	}
	if kind, err := Validate(raw, files); err != nil || kind != Agent {
		t.Fatalf("valid backup rejected: %s %v", kind, err)
	}
	changed := map[string]string{"agent.json": Digest([]byte("changed")), "settings.json": files["settings.json"]}
	if _, err = Validate(raw, changed); err == nil {
		t.Fatal("tampered identity accepted")
	}
	files["pki/ca.key"] = Digest([]byte("central secret"))
	if _, err = Validate(raw, files); err == nil {
		t.Fatal("mixed central/agent files accepted")
	}
}
func TestLegacyZIPContractRemainsReadable(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"format": Agent, "version": 1})
	if _, err := Validate(raw, map[string]string{"agent.json": "", "settings.json": ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(raw, map[string]string{"agent.json": ""}); err == nil {
		t.Fatal("missing settings accepted")
	}
}
