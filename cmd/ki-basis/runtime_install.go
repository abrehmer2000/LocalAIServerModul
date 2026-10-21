package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"localaiserver/internal/agent"
)

const runtimeLimit int64 = 2 << 30

// extractRuntime accepts operator-selected runtime archives, never model plans or
// remote executable URLs. Reject links and traversal; unpack into a new directory.
func extractRuntime(file, directory string) (string, error) {
	var total int64
	entries := 0
	write := func(name string, mode os.FileMode, reader io.Reader, size int64) error {
		entries++
		if entries > 4096 || size < 0 || total+size > runtimeLimit {
			return errors.New("runtime_archive_too_large")
		}
		total += size
		if strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.HasPrefix(name, "/") {
			return errors.New("unsafe_runtime_archive")
		}
		clean := filepath.Clean(filepath.FromSlash(name))
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return errors.New("unsafe_runtime_archive")
		}
		dest := filepath.Join(directory, clean)
		if mode.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		if !mode.IsRegular() {
			return errors.New("runtime_archive_links_not_supported")
		}
		if e := os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return e
		}
		f, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if e != nil {
			return e
		}
		_, e = io.CopyN(f, reader, size)
		closeErr := f.Close()
		if e != nil {
			return e
		}
		return closeErr
	}
	f, e := os.Open(file)
	if e != nil {
		return "", e
	}
	f.Close()
	z, e := zip.OpenReader(file)
	if e == nil {
		defer z.Close()
		for _, entry := range z.File {
			if entry.UncompressedSize64 > uint64(runtimeLimit) {
				return "", errors.New("runtime_archive_too_large")
			}
			r, e := entry.Open()
			if e != nil {
				return "", e
			}
			e = write(entry.Name, entry.Mode(), r, int64(entry.UncompressedSize64))
			r.Close()
			if e != nil {
				return "", e
			}
		}
	} else {
		f, e := os.Open(file)
		if e != nil {
			return "", e
		}
		defer f.Close()
		gz, e := gzip.NewReader(f)
		if e != nil {
			return "", errors.New("runtime_archive_format")
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		// Official Unix archives may contain relative library symlinks. Copy their
		// targets only after all regular files exist; never materialize filesystem links.
		type link struct{ name, target string }
		var links []link
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return "", e
			}
			if h.Typeflag == tar.TypeSymlink {
				if len(links) >= 4096 || strings.ContainsAny(h.Name+h.Linkname, "\\:") || filepath.IsAbs(h.Name) || filepath.IsAbs(h.Linkname) {
					return "", errors.New("unsafe_runtime_archive")
				}
				target := filepath.Clean(filepath.Join(filepath.Dir(h.Name), h.Linkname))
				name := filepath.Clean(h.Name)
				if target == ".." || strings.HasPrefix(target, "../") || name == ".." || strings.HasPrefix(name, "../") {
					return "", errors.New("unsafe_runtime_archive")
				}
				links = append(links, link{name, target})
				continue
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
				return "", errors.New("unsafe_runtime_archive")
			}
			if e = write(h.Name, h.FileInfo().Mode(), tr, h.Size); e != nil {
				return "", e
			}
		}
		for len(links) > 0 {
			var pending []link
			progress := false
			for _, l := range links {
				r, e := os.Open(filepath.Join(directory, l.target))
				if os.IsNotExist(e) {
					pending = append(pending, l)
					continue
				}
				if e != nil {
					return "", e
				}
				info, e := r.Stat()
				if e != nil || !info.Mode().IsRegular() {
					r.Close()
					return "", errors.New("unsafe_runtime_archive")
				}
				e = write(l.name, 0700, r, info.Size())
				r.Close()
				if e != nil {
					return "", e
				}
				progress = true
			}
			if !progress {
				return "", errors.New("runtime_archive_unresolved_link")
			}
			links = pending
		}
	}
	var binary string
	e = filepath.WalkDir(directory, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() && (d.Name() == "llama-server" || d.Name() == "llama-server.exe") {
			if binary != "" {
				return errors.New("runtime_archive_ambiguous")
			}
			binary = path
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	if binary == "" {
		return "", errors.New("engine_binary_not_found")
	}
	return binary, nil
}

func (a *localAgent) importRuntime(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Never replace a running engine's libraries. New packages are private and
	// versioned; settings select them only after a successful launch probe.
	stage, e := os.MkdirTemp(a.dir, "runtime-")
	if e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
		return
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(stage)
		}
	}()
	f, e := os.CreateTemp(a.dir, ".runtime-upload-")
	if e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
		return
	}
	name := f.Name()
	defer os.Remove(name)
	_, e = io.Copy(f, http.MaxBytesReader(w, r.Body, 1<<30))
	ce := f.Close()
	if e != nil || ce != nil {
		writeLocalJSON(w, 400, map[string]string{"error": "runtime_upload_failed"})
		return
	}
	binary, e := extractRuntime(name, stage)
	if e != nil {
		writeLocalJSON(w, 400, map[string]string{"error": "runtime_archive_invalid"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Env = agent.RuntimeEnvironment(binary)
	if cmd.Run() != nil {
		writeLocalJSON(w, 400, map[string]string{"error": "runtime_incompatible"})
		return
	}
	keep = true
	writeLocalJSON(w, 201, map[string]string{"binary": binary})
}
