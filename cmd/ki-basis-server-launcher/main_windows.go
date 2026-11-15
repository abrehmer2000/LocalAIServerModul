// KI-Basis Server opens the local administration UI through an explicit UAC prompt.
package main

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

func main() {
	executable, err := os.Executable()
	if err == nil {
		manager := filepath.Join(filepath.Dir(executable), "KI-Basis-Manager.exe")
		var programData string
		programData, err = windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
		if err == nil {
			verb, _ := windows.UTF16PtrFromString("runas")
			file, _ := windows.UTF16PtrFromString(manager)
			args, _ := windows.UTF16PtrFromString("desktop-open --dir " + syscall.EscapeArg(filepath.Join(programData, "KI-Basis", "desktop")))
			err = windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL)
		}
	}
	if err != nil && err != windows.ERROR_CANCELLED {
		title, _ := windows.UTF16PtrFromString("KI-Basis Server")
		message, _ := windows.UTF16PtrFromString("Die Serververwaltung konnte nicht geöffnet werden. Bitte Dienststatus und Installation prüfen.")
		windows.MessageBox(0, message, title, windows.MB_OK|windows.MB_ICONERROR)
		os.Exit(1)
	}
}
