package main

import (
	"golang.org/x/sys/windows/registry"
	"syscall"
)

func configureAutostart(executable, dir string, enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		err = key.DeleteValue("KI-Basis-Agent")
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	command := syscall.EscapeArg(executable) + " agent-ui --open-browser=false --dir " + syscall.EscapeArg(dir)
	return key.SetStringValue("KI-Basis-Agent", command)
}
