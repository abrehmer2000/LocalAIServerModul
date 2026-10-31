package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"localaiserver/internal/backup"
)

// One-time bridge for the two original pilot backups. Never extracts host SSH
// keys, binaries, model weights or arbitrary archive paths. Normal backups use ZIP.
func unpackLegacyBackup(source, destination string) (string, error) {
	f, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(io.LimitReader(f, 1<<30))
	if err != nil {
		return "", errors.New("invalid_backup")
	}
	defer gz.Close()
	limited := &io.LimitedReader{R: gz, N: 2 << 30}
	reader := tar.NewReader(limited)
	centralNames := map[string]bool{"config.json": true, "gateway.db": true, "pki/ca.crt": true, "pki/ca.key": true, "pki/gateway.crt": true, "pki/gateway.key": true, "access.json": true, "admin-access.json": true, "new-api-key.json": true}
	central := map[string][]byte{}
	agentFiles := map[string][]byte{}
	var total int64
	for count := 0; ; count++ {
		if count > 10000 {
			return "", errors.New("invalid_backup")
		}
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", errors.New("invalid_backup")
		}
		name := strings.TrimPrefix(header.Name, "./")
		if name != path.Clean(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") {
			continue
		}
		target := map[string][]byte(nil)
		relative := ""
		if strings.HasPrefix(name, "workspace/ki-daten/") {
			relative = strings.TrimPrefix(name, "workspace/ki-daten/")
			if centralNames[relative] {
				target = central
			}
			if relative == "agent/agent.json" || relative == "agent/settings.json" {
				relative = strings.TrimPrefix(relative, "agent/")
				target = agentFiles
			}
		}
		if target == nil {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > 128<<20 || total+header.Size > 256<<20 {
			return "", errors.New("invalid_backup")
		}
		if _, duplicate := target[relative]; duplicate {
			return "", errors.New("invalid_backup")
		}
		data, e := io.ReadAll(io.LimitReader(reader, header.Size+1))
		if e != nil || int64(len(data)) != header.Size {
			return "", errors.New("invalid_backup")
		}
		total += header.Size
		target[relative] = data
	}
	// Consume the gzip trailer too, so truncated/corrupt legacy archives fail closed.
	if _, err = io.Copy(io.Discard, limited); err != nil || limited.N == 0 {
		return "", errors.New("invalid_backup")
	}
	kind, files := backup.Agent, agentFiles
	if _, ok := central["config.json"]; ok {
		kind, files = backup.Central, central
	}
	digests := map[string]string{}
	for name, data := range files {
		digests[name] = backup.Digest(data)
	}
	manifest, _ := backup.Encode(kind, digests)
	if _, err = backup.Validate(manifest, digests); err != nil {
		return "", err
	}
	converted, err := os.CreateTemp(filepath.Dir(destination), ".pilot-converted-*.zip")
	if err != nil {
		return "", err
	}
	defer os.Remove(converted.Name())
	writer := zip.NewWriter(converted)
	files["manifest.json"] = manifest
	for name, data := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0600)
		entry, e := writer.CreateHeader(header)
		if e != nil {
			writer.Close()
			converted.Close()
			return "", e
		}
		if _, e = entry.Write(data); e != nil {
			writer.Close()
			converted.Close()
			return "", e
		}
	}
	if err = writer.Close(); err != nil {
		converted.Close()
		return "", err
	}
	if err = converted.Close(); err != nil {
		return "", err
	}
	return unpackBackup(converted.Name(), destination)
}
