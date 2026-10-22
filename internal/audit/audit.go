// Package audit persists metadata-only events in a verifiable hash chain.
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"localaiserver/internal/event"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Event = event.Event

type Record struct {
	Sequence int64  `json:"sequence"`
	Time     string `json:"time"`
	Previous string `json:"previous_hash"`
	Event    Event  `json:"event"`
	Hash     string `json:"hash"`
}
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	dsn := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "_txlock=immediate"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS schema_version(version INTEGER NOT NULL);
 INSERT INTO schema_version SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM schema_version);
 CREATE TABLE IF NOT EXISTS audit(sequence INTEGER PRIMARY KEY, payload TEXT NOT NULL, hash TEXT NOT NULL);
 CREATE TRIGGER IF NOT EXISTS audit_no_update BEFORE UPDATE ON audit BEGIN SELECT RAISE(ABORT,'append-only'); END;
 CREATE TRIGGER IF NOT EXISTS audit_no_delete BEFORE DELETE ON audit BEGIN SELECT RAISE(ABORT,'append-only'); END;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	var version int
	if err = db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil || (version < 1 || version > 3) {
		db.Close()
		return nil, errors.New("unsupported audit schema")
	}
	_, err = db.Exec(`BEGIN IMMEDIATE;
 CREATE TABLE IF NOT EXISTS join_grants(hash TEXT PRIMARY KEY, descriptor TEXT NOT NULL, expires INTEGER NOT NULL, consumed INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS devices(id TEXT PRIMARY KEY, descriptor TEXT NOT NULL, fingerprint TEXT NOT NULL, previous_fingerprint TEXT NOT NULL DEFAULT '', previous_until INTEGER NOT NULL DEFAULT 0, revoked INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS management(node_id TEXT PRIMARY KEY, plan TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS model_pins(id TEXT PRIMARY KEY, descriptor TEXT NOT NULL);
 UPDATE schema_version SET version=3;
 COMMIT;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if _, err = s.Verify(context.Background(), nil); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close drains the single database connection before returning. A canceled SQL
// transaction may still be rolling back after its caller returned; sql.DB.Close
// alone does not wait for that borrowed connection to release its file handle.
// Callers must stop request/monitor goroutines before closing their store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(ctx.Err(), s.db.Close())
	}
	closeErr := s.db.Close()
	return errors.Join(closeErr, conn.Close())
}
func digest(payload []byte) string { v := sha256.Sum256(payload); return hex.EncodeToString(v[:]) }
func (s *Store) Append(ctx context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = appendTx(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}

func appendTx(ctx context.Context, tx *sql.Tx, e Event) error {
	var seq int64
	var prev string
	err := tx.QueryRowContext(ctx, `SELECT sequence,hash FROM audit ORDER BY sequence DESC LIMIT 1`).Scan(&seq, &prev)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	r := Record{Sequence: seq + 1, Time: time.Now().UTC().Format(time.RFC3339Nano), Previous: prev, Event: e}
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit(sequence,payload,hash) VALUES(?,?,?)`, r.Sequence, string(payload), digest(payload))
	if err != nil {
		return err
	}
	return nil
}

// Verify also yields records for export; a separately stored head is needed to detect suffix truncation.
func (s *Store) Verify(ctx context.Context, yield func(Record) error) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,payload,hash FROM audit ORDER BY sequence`)
	if err != nil {
		return Record{}, err
	}
	defer rows.Close()
	var head Record
	for rows.Next() {
		var seq int64
		var payload, h string
		if err = rows.Scan(&seq, &payload, &h); err != nil {
			return head, err
		}
		var r Record
		if err = json.Unmarshal([]byte(payload), &r); err != nil {
			return head, err
		}
		if seq != head.Sequence+1 || r.Sequence != seq || r.Previous != head.Hash || digest([]byte(payload)) != h {
			return head, fmt.Errorf("audit integrity violation at sequence %d", seq)
		}
		r.Hash = h
		head = r
		if yield != nil {
			if err = yield(r); err != nil {
				return head, err
			}
		}
	}
	return head, rows.Err()
}
