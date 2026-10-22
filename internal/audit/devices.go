package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"localaiserver/internal/config"
	"time"
)

var ErrCredential = errors.New("invalid, expired, consumed or revoked credential")

type Device struct {
	Node          config.Node
	Fingerprint   string
	Previous      string
	PreviousUntil int64
	Revoked       bool
}

func (s *Store) IssueJoin(ctx context.Context, hash string, n config.Node, expires time.Time, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM devices WHERE id=?`, n.ID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists != 0 {
		return errors.New("device ID already exists; use a new ID")
	}
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO join_grants(hash,descriptor,expires) VALUES(?,?,?)`, hash, string(data), expires.Unix())
	if err != nil {
		return err
	}
	if err = appendTx(ctx, tx, Event{Type: "node.join_token_issued", NodeID: n.ID, Model: n.Model, Actor: actor}); err != nil {
		return err
	}
	return tx.Commit()
}

// Join consumes the grant and appends its audit event in the same transaction.
func (s *Store) Join(ctx context.Context, hash string, issue func(config.Node) (string, string, error)) (config.Node, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n config.Node
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return n, "", err
	}
	defer tx.Rollback()
	var data string
	err = tx.QueryRowContext(ctx, `SELECT descriptor FROM join_grants WHERE hash=? AND consumed=0 AND expires>?`, hash, time.Now().Unix()).Scan(&data)
	if err != nil {
		return n, "", ErrCredential
	}
	if err = json.Unmarshal([]byte(data), &n); err != nil {
		return n, "", err
	}
	cert, fingerprint, err := issue(n)
	if err != nil {
		return n, "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO devices(id,descriptor,fingerprint) VALUES(?,?,?)`, n.ID, data, fingerprint)
	if err != nil {
		return n, "", err
	}
	_, err = tx.ExecContext(ctx, `UPDATE join_grants SET consumed=1 WHERE hash=?`, hash)
	if err != nil {
		return n, "", err
	}
	if err = appendTx(ctx, tx, Event{Type: "node.joined", NodeID: n.ID, Model: n.Model, Revision: n.Revision, Actor: "device:" + n.ID}); err != nil {
		return n, "", err
	}
	return n, cert, tx.Commit()
}
func (s *Store) Devices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT descriptor,fingerprint,previous_fingerprint,previous_until,revoked FROM devices ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		var data string
		if err = rows.Scan(&data, &d.Fingerprint, &d.Previous, &d.PreviousUntil, &d.Revoked); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &d.Node); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) Device(ctx context.Context, id, fingerprint string) (config.Node, error) {
	var n config.Node
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT descriptor FROM devices WHERE id=? AND revoked=0 AND (fingerprint=? OR (previous_fingerprint=? AND previous_until>?))`, id, fingerprint, fingerprint, time.Now().Unix()).Scan(&data)
	if err != nil {
		return n, ErrCredential
	}
	err = json.Unmarshal([]byte(data), &n)
	return n, err
}
func (s *Store) Renew(ctx context.Context, id, authenticatedFingerprint, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE devices SET previous_fingerprint=?,previous_until=?,fingerprint=? WHERE id=? AND revoked=0 AND (fingerprint=? OR (previous_fingerprint=? AND previous_until>?))`, authenticatedFingerprint, time.Now().Add(10*time.Minute).Unix(), fingerprint, id, authenticatedFingerprint, authenticatedFingerprint, time.Now().Unix())
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return ErrCredential
	}
	if err = appendTx(ctx, tx, Event{Type: "node.certificate_renewed", NodeID: id, Actor: "device:" + id}); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Revoke(ctx context.Context, id, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE devices SET revoked=1 WHERE id=? AND revoked=0`, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return sql.ErrNoRows
	}
	if err = appendTx(ctx, tx, Event{Type: "node.revoked", NodeID: id, Actor: actor}); err != nil {
		return err
	}
	return tx.Commit()
}
