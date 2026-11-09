//go:build !windows

package main

import "errors"

func serviceCommandMain(args []string) error {
	return errors.New("Windows SCM command unavailable; use the supplied systemd units on Linux")
}
