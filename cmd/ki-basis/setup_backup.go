package main

import (
	"archive/zip"
	"bytes"
	"context"
	"localaiserver/internal/backup"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Only the identity and settings are exported. Runtime binaries, models, logs,
// unrelated SSH keys and arbitrary paths are never traversed by this endpoint.
func (a *localAgent) backup(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runner == nil {
		writeLocalJSON(w, 409, map[string]string{"error": "connect_before_backup"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if a.runner.AuditLocal(ctx, "backup_export_requested") != nil {
		writeLocalJSON(w, 503, map[string]string{"error": "management_audit_unavailable"})
		return
	}
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	digests := map[string]string{}
	for _, name := range []string{"agent.json", "settings.json"} {
		data, e := os.ReadFile(filepath.Join(a.dir, name))
		if e != nil {
			z.Close()
			writeLocalJSON(w, 500, map[string]string{"error": "backup_failed"})
			return
		}
		digests[name] = backup.Digest(data)
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0600)
		f, e := z.CreateHeader(header)
		if e != nil {
			writeLocalJSON(w, 500, map[string]string{"error": "backup_failed"})
			return
		}
		if _, e = f.Write(data); e != nil {
			writeLocalJSON(w, 500, map[string]string{"error": "backup_failed"})
			return
		}
	}
	manifest, _ := backup.Encode(backup.Agent, digests)
	f, e := z.Create("manifest.json")
	if e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "backup_failed"})
		return
	}
	if _, e = f.Write(manifest); e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "backup_failed"})
		return
	}
	if z.Close() != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "backup_failed"})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="ki-basis-agent-backup.zip"`)
	w.Write(buffer.Bytes())
}
