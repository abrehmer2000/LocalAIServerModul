package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var serviceStopping = make(chan struct{})
var stopServiceOnce sync.Once

func stopService() { stopServiceOnce.Do(func() { close(serviceStopping) }) }
func terminationContext() (context.Context, context.CancelFunc) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-ctx.Done():
		case <-serviceStopping:
			cancel()
		}
	}()
	return ctx, cancel
}
