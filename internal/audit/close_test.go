package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCloseReleasesDatabaseAfterCanceledTransaction(t *testing.T) {
	for i := 0; i < 12; i++ {
		path := filepath.Join(t.TempDir(), "canceled.db")
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			cancel()
			store.Close()
			t.Fatal(err)
		}
		cancel()
		// Rollback can race with database/sql's automatic cancellation rollback.
		_ = tx.Rollback()
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
		// Windows rejects removal while SQLite still owns a file handle.
		if err = os.Remove(path); err != nil {
			t.Fatal("Close returned with a live database handle:", err)
		}
		if err = store.Close(); err != nil {
			t.Fatal("Close is not idempotent:", err)
		}
	}
}
