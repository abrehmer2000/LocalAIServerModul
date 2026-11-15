package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// GUI builds have no terminal, so failures must remain visible to their operator.
func reportCommandError(err error) {
	isService, _ := svc.IsWindowsService()
	if isService || !strings.EqualFold(filepath.Base(os.Args[0]), "KI-Basis-Manager.exe") {
		slog.Error("command failed", "error", err)
		return
	}
	title, _ := windows.UTF16PtrFromString("KI-Basis konnte nicht gestartet werden")
	message, _ := windows.UTF16PtrFromString(commandErrorMessage(err))
	windows.MessageBox(0, message, title, windows.MB_OK|windows.MB_ICONERROR)
}
