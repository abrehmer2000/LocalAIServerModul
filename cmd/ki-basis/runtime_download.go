package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"localaiserver/internal/agent"
)

type recommendedRuntime struct {
	Supported bool   `json:"supported"`
	Version   string `json:"version"`
	URL       string `json:"source"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Platform  string `json:"platform"`
}

type runtimeDownloadProgress struct {
	Stage      string `json:"stage"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
}

func runtimeRecommendation() recommendedRuntime {
	return runtimeRecommendationFor(runtime.GOOS, runtime.GOARCH)
}

func runtimeRecommendationFor(goos, goarch string) recommendedRuntime {
	r := recommendedRuntime{Version: "b12128", Platform: goos + "/" + goarch}
	// These digests are pinned from the official release metadata and independently
	// checked against downloaded assets. Never accept executable URLs from callers.
	const base = "https://github.com/ggml-org/llama.cpp/releases/download/b12128/"
	switch r.Platform {
	case "windows/amd64":
		r.URL = base + "llama-b12128-bin-win-cpu-x64.zip"
		r.SHA256 = "2864bc950fe1681f1867ebc1bf0d6a66d18cc5561b29c285b4504787145bd7b7"
		r.Size = 24730477
	case "linux/amd64":
		r.URL = base + "llama-b12128-bin-ubuntu-x64.tar.gz"
		r.SHA256 = "a03e1f92e039dc23b630748db27f57f6287aa09a9d015c4eec6fc037657edf1e"
		r.Size = 23080410
	default:
		return r
	}
	r.Supported = true
	return r
}

func runtimeDownloadURLAllowed(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" && u.Port() != "443" {
		return false
	}
	return u.Hostname() == "github.com" || u.Hostname() == "release-assets.githubusercontent.com"
}

func runtimeDownloadClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Minute,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 || !runtimeDownloadURLAllowed(req.URL) {
				return errors.New("runtime_redirect_rejected")
			}
			return nil
		},
	}
}

func installRecommendedRuntime(ctx context.Context, dir string, progress func(runtimeDownloadProgress)) (string, error) {
	client := runtimeDownloadClient()
	defer client.CloseIdleConnections()
	return installRuntimeRecommendation(ctx, dir, runtimeRecommendation(), client, progress)
}

func installRuntimeRecommendation(ctx context.Context, dir string, recommendation recommendedRuntime, client *http.Client, progress func(runtimeDownloadProgress)) (string, error) {
	if !recommendation.Supported {
		return "", errors.New("runtime_platform_unsupported")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errors.New("runtime_storage_failed")
	}
	archive, err := os.CreateTemp(dir, ".runtime-download-")
	if err != nil {
		return "", errors.New("runtime_storage_failed")
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	defer archive.Close()
	notify := func(stage string, n int64) {
		if progress != nil {
			progress(runtimeDownloadProgress{Stage: stage, Downloaded: n, Total: recommendation.Size})
		}
	}
	// Only transport failures are retried, with a fresh file and digest. Integrity
	// failures are never retried into an apparently successful installation.
	for attempt := 0; ; attempt++ {
		err = downloadRuntimeArchive(ctx, client, recommendation, archive, func(n int64) { notify("downloading", n) })
		var networkError *runtimeNetworkError
		if err == nil || attempt >= 1 || !errors.As(err, &networkError) || ctx.Err() != nil {
			break
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	if err != nil {
		return "", err
	}
	if err = archive.Close(); err != nil {
		return "", errors.New("runtime_storage_failed")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	notify("extracting", recommendation.Size)
	stage, err := os.MkdirTemp(dir, "runtime-"+recommendation.Version+"-")
	if err != nil {
		return "", errors.New("runtime_storage_failed")
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(stage)
		}
	}()
	binary, err := extractRuntime(archivePath, stage)
	if err != nil {
		return "", errors.New("runtime_archive_invalid")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	notify("verifying", recommendation.Size)
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, binary, "--version")
	configureRuntimeProbe(cmd)
	cmd.Env = agent.RuntimeEnvironment(binary)
	if cmd.Run() != nil {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		return "", errors.New("runtime_incompatible")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// Keep each validated runtime private and separate. Selecting it is the caller's
	// explicit settings operation, so a running engine's libraries stay untouched.
	keep = true
	notify("ready", recommendation.Size)
	return filepath.Clean(binary), nil
}

type runtimeNetworkError struct{ cause error }

func (e *runtimeNetworkError) Error() string { return "runtime_download_failed" }
func (e *runtimeNetworkError) Unwrap() error { return e.cause }

func downloadRuntimeArchive(ctx context.Context, client *http.Client, recommendation recommendedRuntime, file *os.File, progress func(int64)) error {
	u, err := url.Parse(recommendation.URL)
	if err != nil || !runtimeDownloadURLAllowed(u) || recommendation.Size <= 0 || recommendation.Size > 256<<20 {
		return errors.New("runtime_source_invalid")
	}
	digest, err := hex.DecodeString(recommendation.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return errors.New("runtime_source_invalid")
	}
	if err = file.Truncate(0); err != nil {
		return errors.New("runtime_storage_failed")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return errors.New("runtime_storage_failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, recommendation.URL, nil)
	if err != nil {
		return errors.New("runtime_source_invalid")
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &runtimeNetworkError{cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runtime_http_%d", resp.StatusCode)
	}
	if resp.ContentLength > recommendation.Size {
		return errors.New("runtime_download_size_mismatch")
	}
	hash := sha256.New()
	writer := &runtimeProgressWriter{writer: io.MultiWriter(file, hash), progress: progress}
	if progress != nil {
		progress(0)
	}
	n, err := io.Copy(writer, io.LimitReader(resp.Body, recommendation.Size+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &runtimeNetworkError{cause: err}
	}
	if n != recommendation.Size {
		return errors.New("runtime_download_size_mismatch")
	}
	if hex.EncodeToString(hash.Sum(nil)) != recommendation.SHA256 {
		return errors.New("runtime_checksum_mismatch")
	}
	return nil
}

type runtimeProgressWriter struct {
	writer   io.Writer
	progress func(int64)
	written  int64
}

func (w *runtimeProgressWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.written += int64(n)
	if w.progress != nil {
		w.progress(w.written)
	}
	return n, err
}
