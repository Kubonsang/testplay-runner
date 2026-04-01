package main

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/unity"
)

func TestSignalHandler_CancelsContext(t *testing.T) {
	ctx, causeCancel := context.WithCancelCause(context.Background())
	defer causeCancel(nil)

	sigCh := make(chan os.Signal, 1)
	done := make(chan struct{})

	go watchSignals(ctx, causeCancel, sigCh, func() {
		close(done)
	})

	sigCh <- syscall.SIGINT

	select {
	case <-done:
		// good
	case <-time.After(5 * time.Second):
		t.Fatal("signal handler did not fire in time")
	}

	// Verify the cancel cause is ErrSignalInterrupt.
	if cause := context.Cause(ctx); !errors.Is(cause, unity.ErrSignalInterrupt) {
		t.Errorf("expected ErrSignalInterrupt cause, got %v", cause)
	}
}
