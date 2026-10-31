package audit

import (
	"context"
	"os"
)

// Snapshot writes a consistent SQLite image including WAL contents. The caller
// supplies a nonexistent path inside a private directory; never copy a live DB.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		return os.ErrExist
	}
	_, e := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	if e == nil {
		e = os.Chmod(path, 0600)
	}
	return e
}
