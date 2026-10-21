package control

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"localaiserver/internal/event"
)

func (s *Service) backup(w http.ResponseWriter, r *http.Request) {
	e := event.FromContext(r.Context())
	e.Type = "admin.backup_requested"
	e.Status = 202
	if s.store.Append(r.Context(), e) != nil {
		deny(w, 503)
		return
	}
	dir, err := os.MkdirTemp("", "ki-basis-backup-")
	if err != nil {
		deny(w, 500)
		return
	}
	defer os.RemoveAll(dir)
	snapshot := filepath.Join(dir, "gateway.db")
	if s.store.Snapshot(r.Context(), snapshot) != nil {
		deny(w, 503)
		return
	}
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	add := func(name string, data []byte) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0600)
		f, e := z.CreateHeader(h)
		if e == nil {
			_, e = f.Write(data)
		}
		return e
	}
	cfg := s.config
	// Store portable paths. Restoration chooses the destination directory.
	cfg.Database = "gateway.db"
	cfg.TLSCert = "pki/gateway.crt"
	cfg.TLSKey = "pki/gateway.key"
	cfg.ControlCACert = "pki/ca.crt"
	cfg.ControlCAKey = "pki/ca.key"
	configData, _ := json.MarshalIndent(cfg, "", "  ")
	err = add("config.json", configData)
	for name, path := range map[string]string{"gateway.db": snapshot, "pki/gateway.crt": s.config.TLSCert, "pki/gateway.key": s.config.TLSKey, "pki/ca.crt": s.config.ControlCACert, "pki/ca.key": s.config.ControlCAKey} {
		if err != nil {
			break
		}
		f, e := os.Open(path)
		if e != nil {
			err = e
			break
		}
		raw, e := io.ReadAll(io.LimitReader(f, (128<<20)+1))
		f.Close()
		if len(raw) > 128<<20 {
			e = errors.New("backup_file_too_large")
		}
		if e != nil {
			err = e
			break
		}
		err = add(name, raw)
	}
	base := filepath.Dir(s.config.Database)
	for _, name := range []string{"access.json", "admin-access.json", "new-api-key.json"} {
		if raw, e := os.ReadFile(filepath.Join(base, name)); e == nil && len(raw) <= 65536 && err == nil {
			err = add(name, raw)
		}
	}
	manifest, _ := json.Marshal(map[string]any{"format": "ki-basis-central-backup", "version": 1, "created_at": time.Now().UTC().Format(time.RFC3339), "contains_secrets": true, "models_included": false, "agents_included": false, "note": "Agent-Identitäten separat sichern. Modellgewichte und Laufzeitpakete sind nicht enthalten."})
	if err == nil {
		err = add("manifest.json", manifest)
	}
	if closeErr := z.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		deny(w, 500)
		return
	}
	e.Type = "admin.backup_created"
	e.Status = 200
	if s.store.Append(r.Context(), e) != nil {
		deny(w, 503)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="ki-basis-zentrale-backup.zip"`)
	w.Write(buffer.Bytes())
}
