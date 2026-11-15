//go:build !windows

package main

import "log/slog"

func reportCommandError(err error) { slog.Error("command failed", "error", err) }
