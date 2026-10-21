package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// picker invokes a native dialog on the manager machine. It returns only the
// user-selected path; SSH private key contents never enter the browser.
func pickSetupFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || (in.Kind != "key" && in.Kind != "runtime") {
		writeLocalJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "osascript", "-e", `POSIX path of (choose file with prompt "Datei für KI-Basis auswählen (auf diesem Mac)")`)
	case "windows":
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-STA", "-Command", `Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.OpenFileDialog; if ($dialog.ShowDialog() -eq 'OK') { [Console]::Write($dialog.FileName) }`)
	default:
		cmd = exec.CommandContext(ctx, "zenity", "--file-selection", "--title=Datei für KI-Basis auswählen")
	}
	out, e := cmd.Output()
	path := strings.TrimSpace(string(out))
	if e != nil || path == "" || !filepath.IsAbs(path) {
		writeLocalJSON(w, 409, map[string]string{"error": "file_picker_unavailable_or_cancelled"})
		return
	}
	writeLocalJSON(w, 200, map[string]string{"path": path})
}
