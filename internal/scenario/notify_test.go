package scenario_test

import (
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/scenario"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

// nullWriter discards all writes.
type nullWriter struct{}

func (nullWriter) Write(status.Status) error { return nil }

func TestReadyNotifier_FiresOnTargetPhase(t *testing.T) {
	t.Parallel()
	readyCh := make(chan struct{}, 1)
	notifier := scenario.NewReadyNotifier(nullWriter{}, "compiling", readyCh)

	if err := notifier.Write(status.Status{Phase: status.PhaseCompiling}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case <-readyCh:
		// expected
	default:
		t.Fatal("expected readyCh to be closed after target phase")
	}
}

func TestReadyNotifier_DoesNotFireOnOtherPhase(t *testing.T) {
	t.Parallel()
	readyCh := make(chan struct{}, 1)
	notifier := scenario.NewReadyNotifier(nullWriter{}, "compiling", readyCh)

	if err := notifier.Write(status.Status{Phase: status.PhaseRunning}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case <-readyCh:
		t.Fatal("readyCh should not be closed for non-target phase")
	default:
		// expected
	}
}

func TestReadyNotifier_FiresOnlyOnce(t *testing.T) {
	t.Parallel()
	readyCh := make(chan struct{})
	notifier := scenario.NewReadyNotifier(nullWriter{}, "compiling", readyCh)

	// First write — closes the channel
	_ = notifier.Write(status.Status{Phase: status.PhaseCompiling})
	// Second write — must not panic (double close)
	_ = notifier.Write(status.Status{Phase: status.PhaseCompiling})
}

func TestReadyNotifier_ForwardsToInner(t *testing.T) {
	t.Parallel()
	var got []status.Status
	inner := &collectingWriter{writes: &got}
	readyCh := make(chan struct{}, 1)
	notifier := scenario.NewReadyNotifier(inner, "compiling", readyCh)

	_ = notifier.Write(status.Status{Phase: status.PhaseRunning})
	_ = notifier.Write(status.Status{Phase: status.PhaseCompiling})

	if len(got) != 2 {
		t.Errorf("expected 2 forwarded writes, got %d", len(got))
	}
}

type collectingWriter struct {
	writes *[]status.Status
}

func (w *collectingWriter) Write(s status.Status) error {
	*w.writes = append(*w.writes, s)
	return nil
}
