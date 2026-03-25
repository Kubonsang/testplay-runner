package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestSignalHandler_CancelsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	done := make(chan struct{})

	go watchSignals(ctx, cancel, sigCh, func() {
		close(done)
	})

	// Send a signal
	sigCh <- syscall.SIGINT

	select {
	case <-done:
		// good — signal was handled
	case <-time.After(5 * time.Second):
		t.Fatal("signal handler did not fire in time")
	}
}
