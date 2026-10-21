package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"localaiserver/internal/agent"
	"localaiserver/internal/audit"
	"localaiserver/internal/config"
)

func unpackBackup(path, destination string) (string, error) {
	z, e := zip.OpenReader(path)
	if e != nil {
		return "", e
	}
	defer z.Close()
	allowed := map[string]bool{"manifest.json": true, "agent.json": true, "settings.json": true, "gateway.db": true, "config.json": true, "pki/ca.crt": true, "pki/ca.key": true, "pki/gateway.crt": true, "pki/gateway.key": true, "access.json": true, "admin-access.json": true, "new-api-key.json": true}
	var total uint64
	seen := map[string]bool{}
	for _, f := range z.File {
		if !allowed[f.Name] || seen[f.Name] || !f.Mode().IsRegular() || f.UncompressedSize64 > 128<<20 || total+f.UncompressedSize64 > 256<<20 {
			return "", errors.New("invalid_backup")
		}
		seen[f.Name] = true
		total += f.UncompressedSize64
		if e = os.MkdirAll(filepath.Dir(filepath.Join(destination, f.Name)), 0700); e != nil {
			return "", e
		}
		out, e := os.OpenFile(filepath.Join(destination, f.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return "", e
		}
		in, e := f.Open()
		if e != nil {
			out.Close()
			return "", e
		}
		_, e = io.CopyN(out, in, int64(f.UncompressedSize64))
		in.Close()
		ce := out.Close()
		if e != nil {
			return "", e
		}
		if ce != nil {
			return "", ce
		}
	}
	var manifest struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	raw, e := os.ReadFile(filepath.Join(destination, "manifest.json"))
	if e != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Version != 1 {
		return "", errors.New("invalid_backup")
	}
	switch manifest.Format {
	case "ki-basis-agent-backup":
		if !seen["settings.json"] {
			return "", errors.New("invalid_backup")
		}
		if _, e = agent.Load(filepath.Join(destination, "agent.json")); e != nil {
			return "", e
		}
	case "ki-basis-central-backup":
		for _, name := range []string{"config.json", "gateway.db", "pki/ca.crt", "pki/ca.key", "pki/gateway.crt", "pki/gateway.key"} {
			if !seen[name] {
				return "", errors.New("invalid_backup")
			}
		}
	default:
		return "", errors.New("invalid_backup")
	}
	return manifest.Format, nil
}

func (a *localAgent) restore(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runner != nil {
		writeLocalJSON(w, 409, map[string]string{"error": "restore_requires_unregistered_agent"})
		return
	}
	if _, e := os.Stat(filepath.Join(a.dir, "agent.json")); !os.IsNotExist(e) {
		writeLocalJSON(w, 409, map[string]string{"error": "restore_requires_unregistered_agent"})
		return
	}
	f, e := os.CreateTemp(a.dir, ".backup-upload-")
	if e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
		return
	}
	defer os.Remove(f.Name())
	_, e = io.Copy(f, http.MaxBytesReader(w, r.Body, 2<<20))
	ce := f.Close()
	if e != nil || ce != nil {
		writeLocalJSON(w, 400, map[string]string{"error": "invalid_backup"})
		return
	}
	stage, e := os.MkdirTemp(a.dir, ".restore-")
	if e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
		return
	}
	defer os.RemoveAll(stage)
	kind, e := unpackBackup(f.Name(), stage)
	if e != nil || kind != "ki-basis-agent-backup" {
		writeLocalJSON(w, 400, map[string]string{"error": "invalid_backup"})
		return
	}
	var settings agentSettings
	raw, e := os.ReadFile(filepath.Join(stage, "settings.json"))
	if e != nil || json.Unmarshal(raw, &settings) != nil || agent.ValidateEngineURL(settings.Engine) != nil {
		writeLocalJSON(w, 400, map[string]string{"error": "invalid_backup"})
		return
	}
	// Keep this installation's writable directory and detected runtime when the
	// original machine's runtime path no longer exists.
	settings.Models = a.settings.Models
	if _, e = os.Stat(settings.Binary); e != nil {
		settings.Binary = a.settings.Binary
	}
	if validateExistingModel(settings.ExistingModel) != nil {
		settings.ExistingModel = ""
	}
	settingsData, _ := json.Marshal(settings)
	if e = writePrivate(filepath.Join(a.dir, "settings.json"), settingsData); e == nil {
		identity, _ := os.ReadFile(filepath.Join(stage, "agent.json"))
		e = writePrivate(filepath.Join(a.dir, "agent.json"), identity)
	}
	if e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
		return
	}
	a.settings = settings
	if a.start() != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "agent_start_failed"})
		return
	}
	writeLocalJSON(w, 201, map[string]bool{"restored": true})
}

// restoreCommand only writes to an empty destination. It never overwrites a live
// installation. Desktop launchers can call the same operation without a shell.
func restoreCommand(args []string) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	archive := flags.String("archive", "", "backup ZIP")
	dir := flags.String("dir", "", "empty destination directory")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if *dir == "" || *archive == "" {
		return errors.New("archive_and_destination_required")
	}
	dest, e := filepath.Abs(*dir)
	if e != nil {
		return e
	}
	if entries, e := os.ReadDir(dest); e == nil && len(entries) != 0 {
		return errors.New("restore_destination_must_be_empty")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(filepath.Dir(dest), ".ki-restore-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	kind, e := unpackBackup(*archive, stage)
	if e != nil {
		return e
	}
	if kind == "ki-basis-central-backup" {
		raw, e := os.ReadFile(filepath.Join(stage, "config.json"))
		if e != nil {
			return e
		}
		var cfg config.Config
		if json.Unmarshal(raw, &cfg) != nil {
			return errors.New("invalid_backup")
		}
		cfg.Database = filepath.Join(dest, "gateway.db")
		cfg.TLSCert = filepath.Join(dest, "pki", "gateway.crt")
		cfg.TLSKey = filepath.Join(dest, "pki", "gateway.key")
		cfg.ControlCACert = filepath.Join(dest, "pki", "ca.crt")
		cfg.ControlCAKey = filepath.Join(dest, "pki", "ca.key")
		if e = cfg.Validate(); e != nil {
			return e
		}
		store, e := audit.Open(filepath.Join(stage, "gateway.db"))
		if e != nil {
			return e
		}
		_, e = store.Verify(context.Background(), nil)
		store.Close()
		if e != nil {
			return e
		}
		raw, _ = json.MarshalIndent(cfg, "", "  ")
		if e = writePrivate(filepath.Join(stage, "config.json"), raw); e != nil {
			return e
		}
	}
	// An empty existing destination may be removed; nonempty data was refused above.
	if e = os.Remove(dest); e != nil && !os.IsNotExist(e) {
		return e
	}
	return os.Rename(stage, dest)
}
