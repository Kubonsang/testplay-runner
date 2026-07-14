package unity_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

// TestMain doubles as the subprocess body for the ProcessRunner context tests.
// When TESTPLAY_HELPER_SLEEP=1, the test binary ignores all CLI args (Execute
// passes Unity flags like -batchmode that the testing framework would reject)
// and just sleeps until killed, standing in for a hung Unity process.
func TestMain(m *testing.M) {
	if os.Getenv("TESTPLAY_HELPER_SLEEP") == "1" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// sleepRunner returns a real ProcessRunner whose subprocess is this test
// binary in helper-sleep mode — a portable stand-in for a hung Unity.
func sleepRunner() *unity.ProcessRunner {
	return &unity.ProcessRunner{
		UnityPath: os.Args[0],
		Env:       map[string]string{"TESTPLAY_HELPER_SLEEP": "1"},
	}
}

func TestProcessRunner_Run_SurfacesDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := sleepRunner().Run(ctx, nil, io.Discard, io.Discard)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Run did not return promptly after deadline (%v)", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got err=%v", err)
	}
}

func TestProcessRunner_Run_SurfacesCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(150*time.Millisecond, cancel)

	start := time.Now()
	_, err := sleepRunner().Run(ctx, nil, io.Discard, io.Discard)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Run did not return promptly after cancel (%v)", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got err=%v", err)
	}
}

func TestExecute_RealRunner_TimeoutIsExit4(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	spy := &spyWriter{}

	result, code := unity.Execute(ctx, sleepRunner(), unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
		TimeoutType:  "total",
	})
	if code != 4 {
		t.Fatalf("expected exit 4 (timeout), got %d", code)
	}
	if result.TimeoutType != "total" {
		t.Errorf("expected timeout_type %q, got %q", "total", result.TimeoutType)
	}
	if len(spy.phases) == 0 || spy.phases[len(spy.phases)-1] != status.PhaseTimeoutTotal {
		t.Errorf("expected final status phase %q, got %v", status.PhaseTimeoutTotal, spy.phases)
	}
}

func TestExecute_RealRunner_TwoPhaseCompileTimeoutIsExit4(t *testing.T) {
	dir := t.TempDir()
	spy := &spyWriter{}

	result, code := unity.Execute(context.Background(), sleepRunner(), unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
		TimeoutType:  "total",
		CompileMs:    150,
		TestMs:       60000,
	})
	if code != 4 {
		t.Fatalf("expected exit 4 (compile timeout), got %d", code)
	}
	if result.TimeoutType != "compile" {
		t.Errorf("expected timeout_type %q, got %q", "compile", result.TimeoutType)
	}
	if len(spy.phases) == 0 || spy.phases[len(spy.phases)-1] != status.PhaseTimeoutCompile {
		t.Errorf("expected final status phase %q, got %v", status.PhaseTimeoutCompile, spy.phases)
	}
}

func TestExecute_RealRunner_SignalIsExit8(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	time.AfterFunc(150*time.Millisecond, func() { cancel(unity.ErrSignalInterrupt) })
	spy := &spyWriter{}

	result, code := unity.Execute(ctx, sleepRunner(), unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
		TimeoutType:  "total",
	})
	if code != 8 {
		t.Fatalf("expected exit 8 (signal interrupt), got %d", code)
	}
	if result.TimeoutType != "" {
		t.Errorf("expected empty timeout_type for signal interrupt, got %q", result.TimeoutType)
	}
	if len(spy.phases) == 0 || spy.phases[len(spy.phases)-1] != status.PhaseInterrupted {
		t.Errorf("expected final status phase %q, got %v", status.PhaseInterrupted, spy.phases)
	}
}
