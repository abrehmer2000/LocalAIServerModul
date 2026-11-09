package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestImportLeaseBlocksAnnouncedMaintenanceUntilExplicitRelease(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.SetMaintenanceWindow(ctx, time.Now().Add(-time.Minute), time.Now().Add(time.Hour), Event{}); err != nil {
		t.Fatal(err)
	}
	if err = s.ImportLease(ctx, "import-1", "app", time.Second, false); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		tx, _ := s.db.BeginTx(ctx, nil)
		err := maintenanceAllowed(ctx, tx, time.Now().Add(time.Minute))
		tx.Rollback()
		if (err == nil) != want {
			t.Fatalf("allowed=%v want %v", err == nil, want)
		}
	}
	check(false) // Expired, but not released.
	if err = s.ImportLease(ctx, "import-1", "other", time.Second, false); err == nil {
		t.Fatal("lease stolen")
	}
	if err = s.ImportLease(ctx, "import-1", "app", time.Second, true); err != nil {
		t.Fatal(err)
	}
	check(true)
}
