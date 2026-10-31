package audit

import (
	"context"
	"localaiserver/internal/config"
	"localaiserver/internal/management"
	"path/filepath"
	"testing"
	"time"
)

func TestPlansPersistAuditAndModelPinsDoNotDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	n := config.Node{ID: "node", Model: "chat"}
	if err = s.IssueJoin(ctx, "grant", n, time.Now().Add(time.Minute), "admin"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Join(ctx, "grant", func(config.Node) (string, string, error) { return "cert", "fingerprint", nil }); err != nil {
		t.Fatal(err)
	}
	m := management.Model{ID: "model", SHA256: "pinned"}
	if _, err = s.PinModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	m.SHA256 = "new-main"
	actual, err := s.PinModel(ctx, m)
	if err != nil || actual.SHA256 != "pinned" {
		t.Fatal("model pin drifted")
	}
	plan := management.Plan{Generation: "generation", Action: "download", Model: actual}
	if err = s.SetPlan(ctx, "node", plan, "admin:operator", "admin-key", "operator"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saved, err := s.Plan(ctx, "node")
	if err != nil || saved.Generation != plan.Generation {
		t.Fatal("plan not durable")
	}
	if err = s.Revoke(ctx, "node", "admin"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetPlan(ctx, "node", plan, "admin", "key", "operator"); err == nil {
		t.Fatal("revoked device accepted plan")
	}
	records, err := s.Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range records {
		if r.Event.Type == "model.download_requested" {
			found = true
			if r.Event.UserID != "operator" || r.Event.KeyID != "admin-key" {
				t.Fatal("missing attribution")
			}
		}
	}
	if !found {
		t.Fatal("management mutation not audited")
	}
	if _, err = s.Verify(ctx, nil); err != nil {
		t.Fatal(err)
	}
}
