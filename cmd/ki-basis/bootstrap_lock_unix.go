//go:build !windows

package main

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

// The kernel releases this lock after a crash; no stale lock cleanup is needed.
func lockProcessFile(dir, name string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.New("agent_process_in_use")
		}
		return nil, err
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
