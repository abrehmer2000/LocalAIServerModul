package audit

import (
	"context"
	"database/sql"
	"errors"
	"time"
	_ "time/tzdata"
)

var ErrMaintenanceBlocked = errors.New("maintenance blocked")

// Expired leases intentionally remain a block until explicitly released. A lost
// client must not cause an unattended model restart in the middle of its import.
func (s *Store) ImportLease(ctx context.Context, id, key string, ttl time.Duration, release bool) error {
	if ttl < time.Second || ttl > 5*time.Minute {
		return errors.New("invalid import lease duration")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if release {
		_, err = tx.ExecContext(ctx, "DELETE FROM import_leases WHERE id=? AND key_id=?", id, key)
	} else {
		var owner string
		err = tx.QueryRowContext(ctx, "SELECT key_id FROM import_leases WHERE id=?", id).Scan(&owner)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && owner != key {
			return ErrCredential
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO import_leases(id,key_id,expires) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET expires=excluded.expires`, id, key, time.Now().Add(ttl).Unix())
	}
	if err != nil {
		return err
	}
	action := "renewed"
	if release {
		action = "released"
	}
	if err = appendTx(ctx, tx, Event{Type: "import.lease_" + action, KeyID: key, RequestID: id}); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) SetMaintenanceWindow(ctx context.Context, from, to time.Time, e Event) error {
	if !to.After(from) || to.Sub(from) > 24*time.Hour || to.Before(time.Now()) {
		return errors.New("invalid maintenance window")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO maintenance_window(id,not_before,not_after) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET not_before=excluded.not_before,not_after=excluded.not_after`, from.Unix(), to.Unix()); err != nil {
		return err
	}
	e.Type = "maintenance.window_announced"
	if err = appendTx(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}
func maintenanceAllowed(ctx context.Context, tx *sql.Tx, now time.Time) error {
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM import_leases").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return ErrMaintenanceBlocked
	}
	var from, to int64
	if err := tx.QueryRowContext(ctx, "SELECT not_before,not_after FROM maintenance_window WHERE id=1").Scan(&from, &to); err != nil {
		return ErrMaintenanceBlocked
	}
	if now.Unix() < from || now.Unix() >= to {
		return ErrMaintenanceBlocked
	}
	return nil
}
func ProtectedNight(now time.Time) bool {
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return true
	}
	hour := now.In(zone).Hour()
	return hour >= 20 || hour < 6
}

func (s *Store) AllowMaintenance(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return maintenanceAllowed(ctx, tx, time.Now())
}
func (s *Store) ClearImportLease(ctx context.Context, id string, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM import_leases WHERE id=?", id); err != nil {
		return err
	}
	e.Type = "import.lease_admin_released"
	e.RequestID = id
	if err = appendTx(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}
