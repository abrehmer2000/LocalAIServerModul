//go:build !windows

package main

import "os"

func protectCentralDirectory(path string) error { return os.Chmod(path, 0700) }
