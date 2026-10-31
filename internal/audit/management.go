package audit

import (
	"context"
	"encoding/json"
	"errors"
	"localaiserver/internal/management"
)

func (s *Store) SetPlan(ctx context.Context, id string, p management.Plan, actor, keyID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revoked bool
	if err = tx.QueryRowContext(ctx, "SELECT revoked FROM devices WHERE id=?", id).Scan(&revoked); err != nil || revoked {
		return ErrCredential
	}
	data, _ := json.Marshal(p)
	_, err = tx.ExecContext(ctx, `INSERT INTO management(node_id,plan) VALUES(?,?) ON CONFLICT(node_id) DO UPDATE SET plan=excluded.plan`, id, string(data))
	if err != nil {
		return err
	}
	if err = appendTx(ctx, tx, Event{Type: "model." + p.Action + "_requested", NodeID: id, Model: p.Model.ID, Actor: actor, KeyID: keyID, UserID: userID, Outcome: p.Generation}); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Plan(ctx context.Context, id string) (*management.Plan, error) {
	var data string
	err := s.db.QueryRowContext(ctx, "SELECT plan FROM management WHERE node_id=?", id).Scan(&data)
	if err != nil {
		return nil, err
	}
	var p management.Plan
	err = json.Unmarshal([]byte(data), &p)
	return &p, err
}

// PinModel makes future replicas use exactly the same artifact, even if upstream main changes.
func (s *Store) PinModel(ctx context.Context, m management.Model) (management.Model, error) {
	data, _ := json.Marshal(m)
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO model_pins(id,descriptor) VALUES(?,?)", m.ID, string(data))
	if err != nil {
		return m, err
	}
	return s.PinnedModel(ctx, m.ID)
}
func (s *Store) PinnedModel(ctx context.Context, id string) (management.Model, error) {
	var m management.Model
	var data string
	err := s.db.QueryRowContext(ctx, "SELECT descriptor FROM model_pins WHERE id=?", id).Scan(&data)
	if err != nil {
		return m, err
	}
	err = json.Unmarshal([]byte(data), &m)
	return m, err
}
func (s *Store) Recent(ctx context.Context, limit int) ([]Record, error) {
	if limit < 1 || limit > 200 {
		return nil, errors.New("invalid limit")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT payload,hash FROM audit ORDER BY sequence DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Record, 0)
	for rows.Next() {
		var data, h string
		if err = rows.Scan(&data, &h); err != nil {
			return nil, err
		}
		var r Record
		if err = json.Unmarshal([]byte(data), &r); err != nil {
			return nil, err
		}
		r.Hash = h
		out = append(out, r)
	}
	return out, rows.Err()
}
