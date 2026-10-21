package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// askpass is called only by ssh-add. The passphrase is entered in a native
// system dialog and written directly to ssh-add, never to browser/API/logs.
func sshAskpass() error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "osascript", "-e", `text returned of (display dialog "SSH-Schlüssel für KI-Basis entsperren" default answer "" with hidden answer buttons {"Abbrechen", "Entsperren"} default button "Entsperren")`)
	case "windows":
		return errors.New("native_askpass_not_supported_on_windows")
	default:
		cmd = exec.CommandContext(ctx, "zenity", "--password", "--title=SSH-Schlüssel für KI-Basis entsperren")
	}
	out, e := cmd.Output()
	if e != nil {
		return errors.New("ssh_unlock_cancelled")
	}
	_, e = fmt.Fprint(os.Stdout, strings.TrimSuffix(string(out), "\n"))
	return e
}
func unlockSSHKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || !filepath.IsAbs(in.Key) {
		writeLocalJSON(w, 400, map[string]string{"error": "invalid_ssh_key_path"})
		return
	}
	exe, e := os.Executable()
	if e != nil {
		writeLocalJSON(w, 500, map[string]string{"error": "ssh_unlock_failed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh-add", in.Key)
	cmd.Env = append(os.Environ(), "SSH_ASKPASS="+exe, "SSH_ASKPASS_REQUIRE=force", "KI_BASIS_SSH_ASKPASS=1")
	if os.Getenv("DISPLAY") == "" {
		cmd.Env = append(cmd.Env, "DISPLAY=:0")
	}
	if cmd.Run() != nil {
		writeLocalJSON(w, 409, map[string]string{"error": "ssh_unlock_failed"})
		return
	}
	writeLocalJSON(w, 200, map[string]string{"message": "Schlüssel im SSH-Agenten entsperrt. Du kannst die Installation erneut starten."})
}
