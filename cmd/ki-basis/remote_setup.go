package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"localaiserver/internal/control"
)

type sshTarget struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Key  string `json:"key"`
}

func (t sshTarget) valid() bool {
	return len(t.Host) <= 253 && (net.ParseIP(t.Host) != nil || regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`).MatchString(t.Host)) && t.Port > 0 && t.Port <= 65535 && regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$`).MatchString(t.User) && (t.Key == "" || filepath.IsAbs(t.Key))
}
func scanHost(ctx context.Context, t sshTarget) (string, string, error) {
	if !t.valid() {
		return "", "", errors.New("invalid_ssh_target")
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, e := exec.CommandContext(c, "ssh-keyscan", "-T", "5", "-p", strconv.Itoa(t.Port), "-t", "ed25519,ecdsa,rsa", t.Host).Output()
	if e != nil {
		return "", "", errors.New("ssh_host_unreachable")
	}
	// Pick a stable algorithm order across scans, independent of connection timing.
	keys := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 {
			keys[fields[1]] = line
		}
	}
	for _, algorithm := range []string{"ssh-ed25519", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "ssh-rsa"} {
		line, ok := keys[algorithm]
		if !ok {
			continue
		}
		fields := strings.Fields(line)
		raw, e := base64.StdEncoding.DecodeString(fields[2])
		if e != nil {
			continue
		}
		hash := sha256.Sum256(raw)
		return "SHA256:" + base64.RawStdEncoding.EncodeToString(hash[:]), line + "\n", nil
	}
	return "", "", errors.New("ssh_host_key_unavailable")
}
func sshArgs(t sshTarget, known string) []string {
	args := []string{"-F", os.DevNull, "-T", "-p", strconv.Itoa(t.Port), "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + known, "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3"}
	if t.Key != "" {
		args = append(args, "-i", t.Key, "-o", "IdentitiesOnly=yes")
	}
	return append(args, t.User+"@"+t.Host)
}
func sshRun(ctx context.Context, t sshTarget, known, command string, input io.Reader) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ssh", append(sshArgs(t, known), command)...)
	cmd.Stdin = input
	// Do not return raw SSH output: it can contain local paths or remote startup messages.
	var out boundedSSHOutput
	cmd.Stdout = &out
	if e := cmd.Run(); e != nil {
		return out.Bytes(), errors.New("ssh_auth_or_command_failed")
	}
	return out.Bytes(), nil
}
func remotePayload(osName, arch string) (string, error) {
	exe, e := os.Executable()
	if e != nil {
		return "", e
	}
	if osName == runtime.GOOS && arch == runtime.GOARCH {
		return exe, nil
	}
	path := filepath.Join(filepath.Dir(exe), "agents", osName+"-"+arch, "ki-basis")
	info, e := os.Stat(path)
	if e != nil || !info.Mode().IsRegular() {
		return "", errors.New("ssh_agent_package_missing")
	}
	return path, nil
}
func (a *localAgent) remoteCheck(w http.ResponseWriter, r *http.Request) {
	var t sshTarget
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&t) != nil || decoder.Decode(new(any)) != io.EOF || !t.valid() {
		writeLocalJSON(w, 400, map[string]string{"error": "invalid_ssh_target"})
		return
	}
	fingerprint, _, e := scanHost(r.Context(), t)
	if e != nil {
		writeLocalJSON(w, 502, map[string]string{"error": e.Error()})
		return
	}
	writeLocalJSON(w, 200, map[string]string{"fingerprint": fingerprint, "message": "Host-Schlüssel mit der Serverkonsole oder Administration vergleichen. Erst danach bestätigen."})
}
func (a *localAgent) remoteInstall(w http.ResponseWriter, r *http.Request) {
	if !a.remoteMu.TryLock() {
		writeLocalJSON(w, 409, map[string]string{"error": "ssh_install_in_progress"})
		return
	}
	defer a.remoteMu.Unlock()
	var in struct {
		Target      sshTarget      `json:"target"`
		Fingerprint string         `json:"fingerprint"`
		Bundle      control.Bundle `json:"bundle"`
		Runtime     string         `json:"runtime_archive"`
		Service     bool           `json:"service"`
		Resume      bool           `json:"resume"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF || !in.Target.valid() {
		writeLocalJSON(w, 400, map[string]string{"error": "invalid_ssh_target"})
		return
	}
	expiry, e := time.Parse(time.RFC3339, in.Bundle.Expires)
	if in.Bundle.Version != 1 || (!in.Resume && (e != nil || time.Now().After(expiry))) {
		writeLocalJSON(w, 400, map[string]string{"error": "connection_file_expired"})
		return
	}
	fingerprint, line, e := scanHost(r.Context(), in.Target)
	if e != nil || fingerprint != in.Fingerprint || in.Fingerprint == "" {
		writeLocalJSON(w, 409, map[string]string{"error": "ssh_host_key_changed"})
		return
	}
	if !in.Resume && in.Runtime != "" && (!filepath.IsAbs(in.Runtime) || !(strings.HasSuffix(strings.ToLower(in.Runtime), ".zip") || strings.HasSuffix(strings.ToLower(in.Runtime), ".tar.gz") || strings.HasSuffix(strings.ToLower(in.Runtime), ".tgz"))) {
		writeLocalJSON(w, 400, map[string]string{"error": "runtime_archive_not_found"})
		return
	}
	// All key material remains on the manager. Only the public host key, agent,
	// optional runtime archive and one-time enrollment bundle reach the target.
	tmp, e := os.MkdirTemp(a.dir, ".ssh-install-")
	if e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
		return
	}
	defer os.RemoveAll(tmp)
	known := filepath.Join(tmp, "known_hosts")
	if os.WriteFile(known, []byte(line), 0600) != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	out, e := sshRun(ctx, in.Target, known, "uname -s; uname -m", nil)
	if e != nil {
		writeLocalJSON(w, 502, map[string]string{"error": "ssh_auth_failed_unlock_key"})
		return
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[len(fields)-2] != "Linux" {
		writeLocalJSON(w, 409, map[string]string{"error": "ssh_linux_required"})
		return
	}
	arch := map[string]string{"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[fields[len(fields)-1]]
	if arch == "" {
		writeLocalJSON(w, 409, map[string]string{"error": "ssh_architecture_unsupported"})
		return
	}
	payload := ""
	if !in.Resume {
		payload, e = remotePayload("linux", arch)
	}
	if e != nil {
		writeLocalJSON(w, 409, map[string]string{"error": "ssh_agent_package_missing"})
		return
	}
	// Pre-build the small transfer archive before any target changes. This also
	// ensures a missing local runtime file cannot leave a half-installed target.
	upload, e := os.CreateTemp(tmp, "payload-")
	if e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
		return
	}
	tw := tar.NewWriter(upload)
	add := func(path, name string) error {
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<30 {
			return errors.New("invalid_payload")
		}
		if e = tw.WriteHeader(&tar.Header{Name: name, Size: info.Size(), Mode: 0700}); e != nil {
			return e
		}
		_, e = io.Copy(tw, f)
		return e
	}
	if !in.Resume {
		e = add(payload, "ki-basis")
	}
	if !in.Resume && e == nil && in.Runtime != "" {
		e = add(in.Runtime, "runtime-package")
	}
	bundle, _ := json.Marshal(in.Bundle)
	if e == nil {
		e = tw.WriteHeader(&tar.Header{Name: "connection.json", Size: int64(len(bundle)), Mode: 0600})
		if e == nil {
			_, e = tw.Write(bundle)
		}
	}
	if e == nil {
		e = tw.Close()
	}
	upload.Close()
	if e != nil {
		writeLocalJSON(w, 400, map[string]string{"error": "runtime_archive_not_found"})
		return
	}
	f, e := os.Open(upload.Name())
	if e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "storage_failed"})
		return
	}
	defer f.Close()
	// Fixed destination, fixed commands, no interpolation of user-controlled shell
	// text. Refuse to overwrite an existing identity; retries remain reviewable.
	command := `umask 077; d="$HOME/.local/share/KI-Basis"; mkdir -p "$d" || exit 1; s=$(mktemp -d "$d/.transfer-XXXXXX") || exit 1; trap 'rm -rf "$s"' EXIT; tar -xf - -C "$s" || exit 1; `
	if in.Resume {
		command += `test -f "$d/ki-basis" || exit 1; "$d/ki-basis" agent-bootstrap --resume --dir "$d/agent" --bundle "$s/connection.json"`
	} else {
		command += `if test -e "$d/agent/agent.json"; then printf '%s\n' 'KI_BASIS_SETUP:{"error":"agent_identity_already_exists"}'; exit 1; fi; "$s/ki-basis" agent-bootstrap --install-executable "$d/ki-basis" --dir "$d/agent" --bundle "$s/connection.json" --runtime "$s/runtime-package"`
	}
	if in.Service {
		command += " --service"
	}
	result, e := sshRun(ctx, in.Target, known, command, f)
	if e != nil {
		if diagnostic, ok := parseBootstrapFailure(result); ok {
			writeLocalJSON(w, 409, diagnostic)
		} else {
			writeLocalJSON(w, 502, map[string]string{"error": "ssh_install_failed_check_target"})
		}
		return
	}
	var outcome struct {
		Connected      bool   `json:"connected"`
		NodeID         string `json:"node_id"`
		Service        bool   `json:"service"`
		AlreadyRunning bool   `json:"already_running"`
	}
	if json.Unmarshal(result, &outcome) != nil || !outcome.Connected || outcome.NodeID != in.Bundle.NodeID {
		writeLocalJSON(w, 502, map[string]string{"error": "ssh_install_result_invalid"})
		return
	}
	message := "Agent installiert und angemeldet. Weiter im zentralen Dashboard. Start im Hintergrund; automatischer Start nach Neustart ist nicht eingerichtet."
	if outcome.AlreadyRunning {
		message = "Die passende Agent-Identität ist bereits aktiv. Es wurde kein zweiter Prozess gestartet und keine Anmeldung ersetzt. Den Neustartmodus im ursprünglichen Installationsabschluss prüfen."
	} else if outcome.Service {
		message = "Agent installiert und angemeldet. Systemdienst für automatischen Start eingerichtet. Weiter im zentralen Dashboard."
	}
	writeLocalJSON(w, 201, map[string]any{"connected": true, "node_id": in.Bundle.NodeID, "service": outcome.Service, "message": message})
}

// agent-bootstrap is the noninteractive half of SSH provisioning. The imported
// runtime is probed before consuming an enrollment grant. Identity reuse is refused.
func agentBootstrapCommand(args []string) error {
	// Implemented separately so the same provider-neutral provisioning works with
	// cloud-init and administrative deployment tools as well as the browser manager.
	err := bootstrapAgent(args)
	if err != nil {
		failure := bootstrapFailure{Code: "agent_setup_failed"}
		var detail *bootstrapFailure
		if errors.As(err, &detail) {
			failure = *detail
		} else if bootstrapErrorAllowed(err.Error()) {
			failure.Code = err.Error()
		}
		raw, _ := json.Marshal(failure)
		fmt.Fprintln(os.Stdout, "KI_BASIS_SETUP:"+string(raw))
	}
	return err
}

func bootstrapErrorAllowed(code string) bool {
	switch code {
	case "agent_storage_unavailable", "agent_identity_missing", "agent_setup_in_progress", "agent_identity_already_exists", "agent_identity_mismatch", "agent_identity_unreadable", "agent_settings_invalid", "agent_enrollment_failed", "agent_service_unavailable_or_failed", "agent_service_start_failed", "agent_background_start_failed", "runtime_incompatible", "runtime_archive_invalid", "setup_checks_failed":
		return true
	}
	return false
}
func parseBootstrapFailure(raw []byte) (bootstrapFailure, bool) {
	if len(raw) > 64<<10 {
		return bootstrapFailure{}, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "KI_BASIS_SETUP:") {
			continue
		}
		var failure bootstrapFailure
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "KI_BASIS_SETUP:")), &failure) != nil || !bootstrapErrorAllowed(failure.Code) {
			continue
		}
		// Only fixed diagnostic IDs/states reach the UI; remote messages are discarded.
		clean := []setupCheck{}
		for _, check := range failure.Checks {
			if check.State == "error" {
				if message, ok := remoteCheckMessages[check.ID]; ok {
					clean = append(clean, setupCheck{check.ID, "error", message})
				}
			}
		}
		failure.Checks = clean
		return failure, true
	}
	return bootstrapFailure{}, false
}

var remoteCheckMessages = map[string]string{
	"runtime":    "Laufzeit fehlt oder startet nicht. Ein passendes Linux-llama.cpp-Paket auswählen und erneut installieren.",
	"port":       "Kein freier Modellserver-Port verfügbar. Vorhandenen Modellprozess prüfen.",
	"storage":    "Speicherpfad nicht zugänglich. Schreibbaren dauerhaften Speicher bereitstellen.",
	"space":      "Für das Standardmodell ist zu wenig Speicher frei (etwa 14,7 GB erforderlich).",
	"writable":   "Speicher ist nicht beschreibbar. Rechte des SSH-Benutzers prüfen.",
	"model":      "Die gespeicherte Modelldatei fehlt. Im lokalen Agent-Assistenten den Pfad korrigieren.",
	"connection": "Zentrale vom Zielserver nicht sicher erreichbar. HTTPS-Adresse und Zertifikat prüfen; bei 127.0.0.1 muss der Tunnel auf diesem Zielserver bestehen. Der Manager-Tunnel genügt nicht.",
}

// Preserve bounded protocol output while continuing to drain the SSH process.
type boundedSSHOutput struct{ bytes.Buffer }

func (b *boundedSSHOutput) Write(data []byte) (int, error) {
	count := len(data)
	if remaining := (64 << 10) - b.Len(); remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		b.Buffer.Write(data)
	}
	return count, nil
}
