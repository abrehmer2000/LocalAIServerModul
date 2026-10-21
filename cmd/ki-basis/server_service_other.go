//go:build !linux

package main

func installServerService(executable, dir string, port int) bool { return false }
