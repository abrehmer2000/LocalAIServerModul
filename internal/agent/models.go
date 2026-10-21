package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"localaiserver/internal/catalog"
	"localaiserver/internal/management"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ModelManager owns only its own process and files in its private model directory.
type ModelManager struct {
	ExistingModel              string
	mu                         sync.Mutex
	report                     management.Report
	cancel                     context.CancelFunc
	done                       chan struct{}
	dir, binary, engine, alias string
	capacity                   int
	client                     *http.Client
}

func NewModelManager(dir, binary, engine, alias string, capacity int) *ModelManager {
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		host := req.URL.Hostname()
		if len(via) > 8 || req.URL.Scheme != "https" || !(host == "huggingface.co" || strings.HasSuffix(host, ".huggingface.co") || strings.HasSuffix(host, ".hf.co")) {
			return errors.New("download_redirect_rejected")
		}
		return nil
	}}
	gpu, vram := detectGPU()
	return &ModelManager{dir: dir, binary: binary, engine: engine, alias: alias, capacity: capacity, client: client, report: management.Report{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), GPU: gpu, VRAMBytes: vram, State: "idle"}}
}
func (m *ModelManager) Report() management.Report { m.mu.Lock(); defer m.mu.Unlock(); return m.report }
func (m *ModelManager) Close() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	m.client.CloseIdleConnections()
}
func (m *ModelManager) update(gen, state, code string, downloaded, total int64, hash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.report.Generation != gen {
		return
	}
	m.report.State = state
	m.report.Error = code
	m.report.Downloaded = downloaded
	m.report.Total = total
	m.report.SHA256 = hash
}
func (m *ModelManager) Apply(ctx context.Context, p *management.Plan) {
	if p == nil {
		return
	}
	m.mu.Lock()
	if m.report.Generation == p.Generation {
		m.mu.Unlock()
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	previous := m.done
	work, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.done = make(chan struct{})
	done := m.done
	m.report.Generation = p.Generation
	m.report.Error = ""
	m.mu.Unlock()
	go func() {
		defer close(done)
		if previous != nil {
			select {
			case <-previous:
			case <-work.Done():
				return
			}
		}
		if work.Err() != nil {
			return
		}
		m.execute(work, *p)
	}()
}
func (m *ModelManager) execute(ctx context.Context, p management.Plan) {
	if p.Action == "stop" {
		m.update(p.Generation, "stopped", "", 0, 0, "")
		return
	}
	if !catalog.ValidModel(p.Model) || p.Model.ID != m.alias {
		m.update(p.Generation, "error", "invalid_model", 0, 0, "")
		return
	}
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		m.update(p.Generation, "error", "disk_full", 0, p.Model.Size, "")
		return
	}
	path := filepath.Join(m.dir, p.Model.SHA256+".gguf")
	downloadContext, cancelDownload := context.WithTimeout(ctx, 6*time.Hour)
	defer cancelDownload()
	verifiedExisting := false
	if m.ExistingModel != "" {
		info, e := os.Stat(m.ExistingModel)
		if e != nil || info.Size() != p.Model.Size {
			m.update(p.Generation, "error", "hash_mismatch", 0, p.Model.Size, "")
			return
		}
		m.update(p.Generation, "verifying", "", p.Model.Size, p.Model.Size, "")
		hash, e := fileHash(downloadContext, m.ExistingModel)
		if e != nil || hash != p.Model.SHA256 {
			m.update(p.Generation, "error", "hash_mismatch", 0, p.Model.Size, "")
			return
		}
		path = m.ExistingModel
		verifiedExisting = true
	}
	var downloadError error
	if !verifiedExisting {
		downloadError = m.download(downloadContext, p, path)
	}
	if err := downloadError; err != nil {
		if ctx.Err() == nil {
			code := "download_failed"
			if err.Error() == "hash_mismatch" || err.Error() == "disk_full" {
				code = err.Error()
			}
			m.update(p.Generation, "error", code, 0, p.Model.Size, "")
		}
		return
	}
	if p.Action == "download" {
		m.update(p.Generation, "downloaded", "", p.Model.Size, p.Model.Size, p.Model.SHA256)
		return
	}
	if p.Action != "start" {
		return
	}
	if m.binary == "" {
		m.update(p.Generation, "error", "engine_missing", 0, p.Model.Size, p.Model.SHA256)
		return
	}
	u, _ := url.Parse(m.engine)
	conn, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err == nil {
		conn.Close()
		m.update(p.Generation, "error", "external_engine_running", 0, 0, "")
		return
	}
	layers := "99"
	if p.GPU == "cpu" {
		layers = "0"
	}
	m.update(p.Generation, "starting", "", p.Model.Size, p.Model.Size, p.Model.SHA256)
	// The binary is a local operator setting, never supplied by the gateway or a model repository.
	for attempt := 0; attempt < 5 && ctx.Err() == nil; attempt++ {
		cmd := exec.CommandContext(ctx, m.binary, "--model", path, "--alias", m.alias, "--host", u.Hostname(), "--port", u.Port(), "--ctx-size", "2048", "--parallel", strconv.Itoa(m.capacity), "--n-gpu-layers", layers)
		configureProcess(cmd)
		cmd.Cancel = func() error { return killProcessTree(cmd) }
		cmd.WaitDelay = 3 * time.Second
		if err = cmd.Start(); err == nil {
			// Actual readiness is checked by Runner.Ready, not this process-start event.
			m.update(p.Generation, "running", "", p.Model.Size, p.Model.Size, p.Model.SHA256)
			err = cmd.Wait()
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(1<<attempt) * time.Second):
		}
	}
	m.update(p.Generation, "error", "engine_failed", p.Model.Size, p.Model.Size, p.Model.SHA256)
}
func fileHash(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		n, e := f.Read(buf)
		h.Write(buf[:n])
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (m *ModelManager) download(ctx context.Context, p management.Plan, path string) error {
	spec := p.Model
	if info, err := os.Stat(path); err == nil && info.Size() == spec.Size {
		m.update(p.Generation, "verifying", "", spec.Size, spec.Size, "")
		h, e := fileHash(ctx, path)
		if e == nil && h == spec.SHA256 {
			return nil
		}
		if e == nil {
			_ = os.Remove(path)
		}
		return errors.New("hash_mismatch")
	}
	part := path + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	offset := info.Size()
	if offset > spec.Size {
		if err = f.Truncate(0); err != nil {
			return err
		}
		offset = 0
	}
	if free, err := freeSpace(m.dir); err == nil && free < uint64(spec.Size-offset)+(256<<20) {
		return errors.New("disk_full")
	}
	if offset < spec.Size {
		req, _ := http.NewRequestWithContext(ctx, "GET", catalog.DownloadURL(spec), nil)
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		resp, err := m.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			offset = 0
			if err = f.Truncate(0); err != nil {
				return err
			}
		} else if resp.StatusCode != 206 || !strings.HasPrefix(resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", offset)) {
			return errors.New("download_failed")
		}
		if _, err = f.Seek(offset, 0); err != nil {
			return err
		}
		m.update(p.Generation, "downloading", "", offset, spec.Size, "")
		buf := make([]byte, 1<<20)
		last := time.Time{}
		for {
			n, e := resp.Body.Read(buf)
			if offset+int64(n) > spec.Size {
				return errors.New("download_failed")
			}
			if n > 0 {
				written, we := f.Write(buf[:n])
				if we != nil {
					return errors.New("disk_full")
				}
				offset += int64(written)
			}
			if time.Since(last) > 250*time.Millisecond {
				m.update(p.Generation, "downloading", "", offset, spec.Size, "")
				last = time.Now()
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	if offset != spec.Size {
		return errors.New("download_failed")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	m.update(p.Generation, "verifying", "", offset, spec.Size, "")
	hash, err := fileHash(ctx, part)
	if err != nil {
		return err
	}
	if hash != spec.SHA256 {
		os.Remove(part)
		return errors.New("hash_mismatch")
	}
	return os.Rename(part, path)
}

func detectGPU() (string, uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil || len(out) > 4096 {
		return "", 0
	}
	names := []string{}
	var memory uint64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		size, e := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
		if e != nil || len(name) > 128 {
			continue
		}
		names = append(names, name)
		memory += size * (1 << 20)
	}
	return strings.Join(names, ", "), memory
}
