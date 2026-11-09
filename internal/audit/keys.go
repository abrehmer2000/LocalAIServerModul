package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"localaiserver/internal/config"
)

func (s *Store) LoadKeys(ctx context.Context) ([]config.Key, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT descriptor FROM api_key_sets WHERE id=1").Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []config.Key
	err = json.Unmarshal([]byte(raw), &keys)
	return keys, err
}
func (s *Store) StoreKeys(ctx context.Context, keys []config.Key, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO api_key_sets(id,descriptor) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET descriptor=excluded.descriptor`, string(raw)); err != nil {
		return err
	}
	e.Type = "keys.reloaded"
	e.Outcome = "completed"
	if err = appendTx(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}
