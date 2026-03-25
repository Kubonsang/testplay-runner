package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// watchSignals listens on sigCh for SIGINT/SIGTERM.
// On signal: calls cancel() to propagate cancellation, then calls onInterrupt().
func watchSignals(ctx context.Context, cancel context.CancelFunc, sigCh chan os.Signal, onInterrupt func()) {
	select {
	case <-ctx.Done():
		// context already cancelled (e.g., timeout)
		return
	case <-sigCh:
		cancel()
		if onInterrupt != nil {
			onInterrupt()
		}
	}
}

// setupSignals sets up OS signal handling and returns the signal channel.
// Call this from main() to catch SIGINT and SIGTERM.
func setupSignals() chan os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	return sigCh
}
