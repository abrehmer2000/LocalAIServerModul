//go:build !linux && !windows

package main

import "errors"

func configureAutostart(executable, dir string, enabled bool) error {
	return errors.New("autostart_not_supported")
}
