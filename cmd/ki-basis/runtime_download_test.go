package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type runtimeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f runtimeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func runtimeFixture(t *testing.T, name, content string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0700)
	w, err := z.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(w, content); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func fixtureRecommendation(data []byte) recommendedRuntime {
	digest := sha256.Sum256(data)
	return recommendedRuntime{Supported: true, Version: "test", Platform: "test", URL: "https://github.com/ggml-org/llama.cpp/releases/download/test/test.zip", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))}
}

func fixtureRuntimeClient(data []byte) *http.Client {
	client := runtimeDownloadClient()
	client.Transport = runtimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Request: r}, nil
	})
	return client
}

func TestRuntimeRecommendationPinnedPlatforms(t *testing.T) {
	for _, platform := range []string{"windows", "linux"} {
		r := runtimeRecommendationFor(platform, "amd64")
		if !r.Supported || r.Version != "b12128" || len(r.SHA256) != 64 || r.Size < 1<<20 || strings.Contains(r.URL, "latest") {
			t.Fatalf("invalid recommendation: %+v", r)
		}
	}
	if r := runtimeRecommendationFor("darwin", "amd64"); r.Supported || r.URL != "" {
		t.Fatalf("unsupported platform was offered: %+v", r)
	}
}

func TestRuntimeDownloadSourcePolicy(t *testing.T) {
	for _, raw := range []string{"http://github.com/file", "https://github.com.evil.test/file", "https://evil.test/file", "https://user:secret@github.com/file", "https://github.com:8443/file", "file:///tmp/runtime"} {
		u, err := url.Parse(raw)
		if err == nil && runtimeDownloadURLAllowed(u) {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"https://github.com/file", "https://release-assets.githubusercontent.com/file"} {
		u, _ := url.Parse(raw)
		if !runtimeDownloadURLAllowed(u) {
			t.Errorf("rejected %s", raw)
		}
	}
}

func TestRuntimeDownloadIntegrityBoundsAndCleanup(t *testing.T) {
	data := runtimeFixture(t, "llama-server", "#!/bin/sh\nexit 0\n")
	for _, tc := range []struct {
		name string
		edit func(*recommendedRuntime)
		data []byte
		want string
	}{
		{"checksum", func(r *recommendedRuntime) { r.SHA256 = strings.Repeat("0", 64) }, data, "runtime_checksum_mismatch"},
		{"large", func(r *recommendedRuntime) { r.Size-- }, data, "runtime_download_size_mismatch"},
		{"short", func(r *recommendedRuntime) { r.Size++ }, data, "runtime_download_size_mismatch"},
		{"bad archive", func(r *recommendedRuntime) {}, []byte("invalid archive"), "runtime_archive_invalid"},
		{"traversal", func(r *recommendedRuntime) {}, runtimeFixture(t, "../llama-server", "unsafe"), "runtime_archive_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixtureRecommendation(tc.data)
			tc.edit(&r)
			dir := t.TempDir()
			_, err := installRuntimeRecommendation(context.Background(), dir, r, fixtureRuntimeClient(tc.data), nil)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatalf("failed installation left %v", entries)
			}
		})
	}
}

func TestRuntimeDownloadUnknownLengthBounded(t *testing.T) {
	data := []byte("four")
	r := fixtureRecommendation(data)
	client := runtimeDownloadClient()
	client.Transport = runtimeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("four extra")), ContentLength: -1, Request: req}, nil
	})
	file, err := os.CreateTemp(t.TempDir(), "download")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = downloadRuntimeArchive(context.Background(), client, r, file, nil); err == nil || err.Error() != "runtime_download_size_mismatch" {
		t.Fatalf("got %v", err)
	}
	info, _ := file.Stat()
	if info.Size() > r.Size+1 {
		t.Fatalf("unbounded download wrote %d bytes", info.Size())
	}
}

func TestRuntimeDownloadRedirectPolicy(t *testing.T) {
	for _, destination := range []string{"https://evil.test/runtime", "http://release-assets.githubusercontent.com/runtime", "https://github.com/loop"} {
		t.Run(destination, func(t *testing.T) {
			client := runtimeDownloadClient()
			calls := 0
			client.Transport = runtimeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Hostname() == "evil.test" {
					t.Fatal("sent request to rejected destination")
				}
				return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": {destination}}, Request: req}, nil
			})
			_, err := client.Get("https://github.com/start")
			if err == nil || calls > 4 {
				t.Fatalf("got %v after %d requests", err, calls)
			}
		})
	}
}

func TestRuntimeDownloadOfficialAssetRedirect(t *testing.T) {
	data := []byte("official asset fixture")
	client := runtimeDownloadClient()
	client.Transport = runtimeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() == "github.com" {
			return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": {"https://release-assets.githubusercontent.com/release/test"}}, Request: req}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Request: req}, nil
	})
	file, err := os.CreateTemp(t.TempDir(), "download")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = downloadRuntimeArchive(context.Background(), client, fixtureRecommendation(data), file, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDownloadCancellationCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := runtimeDownloadClient()
	client.Transport = runtimeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		cancel()
		return nil, req.Context().Err()
	})
	dir := t.TempDir()
	_, err := installRuntimeRecommendation(ctx, dir, fixtureRecommendation([]byte("unused")), client, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("cancelled installation left %v", entries)
	}
}

func TestRuntimeInstallProbeAndPreserveExisting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixture; Windows integrity tests run separately")
	}
	data := runtimeFixture(t, "bin/llama-server", "#!/bin/sh\n[ \"$1\" = \"--version\" ]\n")
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing-runtime")
	if err := os.WriteFile(existing, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	var stages []string
	binary, err := installRuntimeRecommendation(context.Background(), dir, fixtureRecommendation(data), fixtureRuntimeClient(data), func(p runtimeDownloadProgress) { stages = append(stages, p.Stage) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(binary, dir+string(filepath.Separator)) || stages[len(stages)-1] != "ready" {
		t.Fatalf("binary=%q stages=%v", binary, stages)
	}
	if got, _ := os.ReadFile(existing); string(got) != "untouched" {
		t.Fatal("existing runtime changed")
	}
	bad := runtimeFixture(t, "llama-server", "#!/bin/sh\nexit 1\n")
	if _, err = installRuntimeRecommendation(context.Background(), dir, fixtureRecommendation(bad), fixtureRuntimeClient(bad), nil); err == nil || err.Error() != "runtime_incompatible" {
		t.Fatalf("failed probe accepted: %v", err)
	}
	if _, err = os.Stat(binary); err != nil {
		t.Fatal("previous validated runtime lost", err)
	}
}

func TestRuntimeInstallCancellationBeforeProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixture")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	data := runtimeFixture(t, "llama-server", "#!/bin/sh\nexit 0\n")
	dir := t.TempDir()
	_, err := installRuntimeRecommendation(ctx, dir, fixtureRecommendation(data), fixtureRuntimeClient(data), func(p runtimeDownloadProgress) {
		if p.Stage == "verifying" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("cancelled probe left %v", entries)
	}
}

func TestRuntimeNetworkRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data := []byte("not archive")
	client := fixtureRuntimeClient(data)
	underlying := client.Transport
	calls := 0
	client.Transport = runtimeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, io.ErrUnexpectedEOF
		}
		return underlying.RoundTrip(req)
	})
	_, err := installRuntimeRecommendation(ctx, t.TempDir(), fixtureRecommendation(data), client, nil)
	if calls != 2 || err == nil || err.Error() != "runtime_archive_invalid" {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}
