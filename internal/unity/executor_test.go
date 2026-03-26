package unity_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

func mustReadFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}
	return data
}

func TestExecute_AllTestsPass_Returns0(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadFixture(t, "../parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData, exitCode: 0}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	result, code := unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: sw,
	})
	if code != 0 {
		t.Errorf("expected exit 0, got %d", code)
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failures, got %d", result.Failed)
	}
}

func TestExecute_TestFailure_Returns3(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadFixture(t, "../parser/testdata/one_failure.xml")
	fake := &fakeRunner{resultsXML: xmlData, exitCode: 0}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	_, code := unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: sw,
	})
	if code != 3 {
		t.Errorf("expected exit 3, got %d", code)
	}
}

func TestExecute_NoXMLFile_Returns2(t *testing.T) {
	dir := t.TempDir()
	// fakeRunner doesn't write XML — simulates compile failure
	fake := &fakeRunner{
		exitCode: 0,
		stderr:   []byte(`Assets/Foo.cs(1,1): error CS0246: Type 'Bar' not found`),
	}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	_, code := unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"), // file won't exist
		StatusWriter: sw,
	})
	if code != 2 {
		t.Errorf("expected exit 2 (compile failure), got %d", code)
	}
}

func TestExecute_ContextCancelled_Returns4(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately — simulates signal interruption

	dir := t.TempDir()
	fake := &fakeRunner{err: context.Canceled}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	result, code := unity.Execute(ctx, fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: sw,
		TimeoutType:  "total",
	})
	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}
	// Signal cancellation: TimeoutType is empty (no timeout occurred).
	if result.TimeoutType != "" {
		t.Errorf("expected empty TimeoutType for signal cancel, got %q", result.TimeoutType)
	}
}

type spyWriter struct {
	phases []status.Phase
}

func (s *spyWriter) Write(st status.Status) error {
	s.phases = append(s.phases, st.Phase)
	return nil
}

func TestExecute_WritesStatusPhases(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadFixture(t, "../parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData, exitCode: 0}
	spy := &spyWriter{}

	unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
	})

	expected := []status.Phase{status.PhaseCompiling, status.PhaseRunning, status.PhaseDone}
	if !reflect.DeepEqual(spy.phases, expected) {
		t.Errorf("expected phase sequence %v, got %v", expected, spy.phases)
	}
}

func TestExecute_TimeoutType_AlwaysPropagated(t *testing.T) {
	// opts.TimeoutType is always propagated to the result.
	dir := t.TempDir()
	blockingRunner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			<-ctx.Done()
			return nil, nil, -1, ctx.Err()
		},
	}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, code := unity.Execute(ctx, blockingRunner, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: sw,
		TimeoutType:  "total",
	})
	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}
	if result.TimeoutType != "total" {
		t.Errorf("expected timeout_type 'total', got %q", result.TimeoutType)
	}
}

// funcRunner implements Runner via a pluggable function, for tests that need
// custom blocking or error behaviour that fakeRunner cannot provide.
type funcRunner struct {
	run func(ctx context.Context, args []string) ([]byte, []byte, int, error)
}

func (f *funcRunner) Run(ctx context.Context, args []string) ([]byte, []byte, int, error) {
	return f.run(ctx, args)
}

func TestExecute_PlayMode_PassesPlayModeToRunner(t *testing.T) {
	dir := t.TempDir()
	var capturedArgs []string
	capturingRunner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			capturedArgs = args
			return nil, nil, -1, context.Canceled
		},
	}

	unity.Execute(context.Background(), capturingRunner, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		TestPlatform: "play_mode",
	})

	idx := indexOf(capturedArgs, "-testPlatform")
	if idx == -1 || idx+1 >= len(capturedArgs) {
		t.Fatal("-testPlatform not found in args")
	}
	if capturedArgs[idx+1] != "PlayMode" {
		t.Errorf("expected PlayMode, got %q", capturedArgs[idx+1])
	}
}

// TestExecute_SignalCancel_WritesInterruptedPhase verifies that context.Canceled
// (signal interruption) writes PhaseInterrupted — not PhaseTimeoutTotal.
func TestExecute_SignalCancel_WritesInterruptedPhase(t *testing.T) {
	dir := t.TempDir()
	spy := &spyWriter{}
	fake := &fakeRunner{err: context.Canceled}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate signal

	result, code := unity.Execute(ctx, fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
		TimeoutType:  "total",
	})

	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}
	if result.TimeoutType != "" {
		t.Errorf("signal cancellation should have empty TimeoutType, got %q", result.TimeoutType)
	}
	last := spy.phases[len(spy.phases)-1]
	if last != status.PhaseInterrupted {
		t.Errorf("expected final phase %q, got %q", status.PhaseInterrupted, last)
	}
}

// TestExecute_DeadlineExceeded_WritesTimeoutTotalPhase verifies that
// context.DeadlineExceeded writes PhaseTimeoutTotal and preserves TimeoutType.
func TestExecute_DeadlineExceeded_WritesTimeoutTotalPhase(t *testing.T) {
	dir := t.TempDir()
	spy := &spyWriter{}
	blockingRunner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			<-ctx.Done()
			return nil, nil, -1, ctx.Err()
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, code := unity.Execute(ctx, blockingRunner, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
		TimeoutType:  "total",
	})

	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}
	if result.TimeoutType != "total" {
		t.Errorf("expected TimeoutType 'total', got %q", result.TimeoutType)
	}
	last := spy.phases[len(spy.phases)-1]
	if last != status.PhaseTimeoutTotal {
		t.Errorf("expected final phase %q, got %q", status.PhaseTimeoutTotal, last)
	}
}

// ── Two-phase execution tests ────────────────────────────────────────────────

func TestExecute_TwoPhase_CompileTimeout_EmitsTimeoutCompile(t *testing.T) {
	dir := t.TempDir()
	spy := &spyWriter{}
	callCount := 0
	blockingCompile := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			callCount++
			// Phase 1 (compile): block until context fires
			<-ctx.Done()
			return nil, nil, -1, ctx.Err()
		},
	}

	result, code := unity.Execute(context.Background(), blockingCompile, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
		CompileMs:    50,
		TestMs:       5000,
	})

	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}
	if result.TimeoutType != "compile" {
		t.Errorf("expected TimeoutType 'compile', got %q", result.TimeoutType)
	}
	last := spy.phases[len(spy.phases)-1]
	if last != status.PhaseTimeoutCompile {
		t.Errorf("expected final phase %q, got %q", status.PhaseTimeoutCompile, last)
	}
	if callCount != 1 {
		t.Errorf("expected runner called once (compile only), got %d", callCount)
	}
}

func TestExecute_TwoPhase_TestTimeout_EmitsTimeoutTest(t *testing.T) {
	dir := t.TempDir()
	spy := &spyWriter{}
	callCount := 0
	runner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			callCount++
			if callCount == 1 {
				// Phase 1 (compile): succeed immediately
				return nil, nil, 0, nil
			}
			// Phase 2 (test): block until context fires
			<-ctx.Done()
			return nil, nil, -1, ctx.Err()
		},
	}

	result, code := unity.Execute(context.Background(), runner, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
		CompileMs:    5000,
		TestMs:       50,
	})

	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}
	if result.TimeoutType != "test" {
		t.Errorf("expected TimeoutType 'test', got %q", result.TimeoutType)
	}
	last := spy.phases[len(spy.phases)-1]
	if last != status.PhaseTimeoutTest {
		t.Errorf("expected final phase %q, got %q", status.PhaseTimeoutTest, last)
	}
	if callCount != 2 {
		t.Errorf("expected runner called twice (compile + test), got %d", callCount)
	}
}

func TestExecute_TwoPhase_BothSucceed_Returns0(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadFixture(t, "../parser/testdata/passing.xml")
	callCount := 0
	runner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			callCount++
			if callCount == 2 {
				// Phase 2: write results XML
				if err := os.WriteFile(filepath.Join(dir, "results.xml"), xmlData, 0644); err != nil {
					return nil, nil, 1, err
				}
			}
			return nil, nil, 0, nil
		},
	}

	_, code := unity.Execute(context.Background(), runner, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		CompileMs:    5000,
		TestMs:       5000,
	})

	if code != 0 {
		t.Errorf("expected exit 0, got %d", code)
	}
	if callCount != 2 {
		t.Errorf("expected 2 runner calls (compile + test), got %d", callCount)
	}
}

func TestExecute_TwoPhase_PhaseSequence(t *testing.T) {
	// Verify phase progression: compiling → running → done in two-phase success path
	dir := t.TempDir()
	xmlData := mustReadFixture(t, "../parser/testdata/passing.xml")
	spy := &spyWriter{}
	callCount := 0
	runner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			callCount++
			if callCount == 2 {
				_ = os.WriteFile(filepath.Join(dir, "results.xml"), xmlData, 0644)
			}
			return nil, nil, 0, nil
		},
	}

	unity.Execute(context.Background(), runner, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
		CompileMs:    5000,
		TestMs:       5000,
	})

	expected := []status.Phase{status.PhaseCompiling, status.PhaseRunning, status.PhaseDone}
	if !reflect.DeepEqual(spy.phases, expected) {
		t.Errorf("expected phase sequence %v, got %v", expected, spy.phases)
	}
}

func TestExecute_TwoPhase_CompileError_SkipsTestPhase(t *testing.T) {
	dir := t.TempDir()
	callCount := 0
	runner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			callCount++
			// Phase 1: return compile error in stderr
			return nil, []byte(`Assets/Foo.cs(1,1): error CS0246: Type 'Bar' not found`), 1, nil
		},
	}

	_, code := unity.Execute(context.Background(), runner, unity.ExecuteOptions{
		ProjectPath: dir,
		ResultsFile: filepath.Join(dir, "results.xml"),
		CompileMs:   5000,
		TestMs:      5000,
	})

	if code != 2 {
		t.Errorf("expected exit 2 (compile error), got %d", code)
	}
	if callCount != 1 {
		t.Errorf("expected runner called once (no test phase after compile error), got %d", callCount)
	}
}

func TestExecute_TwoPhase_Phase1RunnerError_ReturnsExit2_SkipsPhase2(t *testing.T) {
	// A non-context runner error in compile phase should return exit 2 without
	// starting the test phase.
	dir := t.TempDir()
	callCount := 0
	someRunnerErr := fmt.Errorf("exec: unity: no such file")
	runner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			callCount++
			return nil, nil, -1, someRunnerErr
		},
	}

	_, code := unity.Execute(context.Background(), runner, unity.ExecuteOptions{
		ProjectPath: dir,
		ResultsFile: filepath.Join(dir, "results.xml"),
		CompileMs:   5000,
		TestMs:      5000,
	})

	if code != 2 {
		t.Errorf("expected exit 2 for non-context phase 1 error, got %d", code)
	}
	if callCount != 1 {
		t.Errorf("expected runner called once (phase 2 must not start), got %d", callCount)
	}
}

func TestExecute_TwoPhase_Phase2RunnerError_ReturnsExit2(t *testing.T) {
	// A non-context runner error in test phase should return exit 2.
	dir := t.TempDir()
	someRunnerErr := fmt.Errorf("exec: unity: no such file")
	callCount := 0
	runner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			callCount++
			if callCount == 1 {
				return nil, nil, 0, nil // compile phase succeeds
			}
			return nil, nil, -1, someRunnerErr
		},
	}

	_, code := unity.Execute(context.Background(), runner, unity.ExecuteOptions{
		ProjectPath: dir,
		ResultsFile: filepath.Join(dir, "results.xml"),
		CompileMs:   5000,
		TestMs:      5000,
	})

	if code != 2 {
		t.Errorf("expected exit 2 for non-context phase 2 error, got %d", code)
	}
	if callCount != 2 {
		t.Errorf("expected 2 runner calls, got %d", callCount)
	}
}

// TestExecute_TwoPhase_TotalTimeout_DuringCompile_EmitsTimeoutTotal verifies that when
// the outer total_ms context expires while in the compile phase, the result is
// timeout_type "total" (not "compile").
func TestExecute_TwoPhase_TotalTimeout_DuringCompile_EmitsTimeoutTotal(t *testing.T) {
	dir := t.TempDir()
	spy := &spyWriter{}

	blockingRunner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			<-ctx.Done()
			return nil, nil, -1, ctx.Err()
		},
	}

	// Outer ctx (total_ms) fires before CompileMs deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, code := unity.Execute(ctx, blockingRunner, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
		CompileMs:    5000,
		TestMs:       5000,
	})

	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}
	if result.TimeoutType != "total" {
		t.Errorf("expected TimeoutType 'total' when outer ctx fires during compile, got %q", result.TimeoutType)
	}
	last := spy.phases[len(spy.phases)-1]
	if last != status.PhaseTimeoutTotal {
		t.Errorf("expected final phase %q, got %q", status.PhaseTimeoutTotal, last)
	}
}

// TestExecute_TwoPhase_TotalTimeout_DuringTest_EmitsTimeoutTotal verifies that when
// the outer total_ms context expires while in the test phase, the result is
// timeout_type "total" (not "test").
func TestExecute_TwoPhase_TotalTimeout_DuringTest_EmitsTimeoutTotal(t *testing.T) {
	dir := t.TempDir()
	spy := &spyWriter{}
	callCount := 0

	runner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			callCount++
			if callCount == 1 {
				// Compile phase: return immediately so we advance to test phase.
				return nil, nil, 0, nil
			}
			// Test phase: block until the context fires.
			<-ctx.Done()
			return nil, nil, -1, ctx.Err()
		},
	}

	// Outer ctx (total_ms) fires before TestMs deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, code := unity.Execute(ctx, runner, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
		CompileMs:    5000,
		TestMs:       5000,
	})

	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}
	if result.TimeoutType != "total" {
		t.Errorf("expected TimeoutType 'total' when outer ctx fires during test, got %q", result.TimeoutType)
	}
	last := spy.phases[len(spy.phases)-1]
	if last != status.PhaseTimeoutTotal {
		t.Errorf("expected final phase %q, got %q", status.PhaseTimeoutTotal, last)
	}
	if callCount != 2 {
		t.Errorf("expected 2 runner calls (compile + test), got %d", callCount)
	}
}

// TestExecute_TwoPhase_CompilePhase_NonZeroExit_NoCompileErrors_ReturnsExit2 verifies
// that a non-zero exit from the compile phase with no recognisable compile errors
// is still treated as a failure (exit 2) rather than silently passing to phase 2.
func TestExecute_TwoPhase_CompilePhase_NonZeroExit_NoCompileErrors_ReturnsExit2(t *testing.T) {
	dir := t.TempDir()
	callCount := 0
	runner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			callCount++
			// Non-zero exit with generic (non-compile-error) stderr, e.g. license failure.
			return nil, []byte("Unity license check failed"), 1, nil
		},
	}

	result, code := unity.Execute(context.Background(), runner, unity.ExecuteOptions{
		ProjectPath: dir,
		ResultsFile: filepath.Join(dir, "results.xml"),
		CompileMs:   5000,
		TestMs:      5000,
	})

	if code != 2 {
		t.Errorf("expected exit 2 for non-zero compile phase exit, got %d", code)
	}
	if callCount != 1 {
		t.Errorf("expected runner called once (no test phase after compile failure), got %d", callCount)
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one error entry for non-zero compile exit with no compile errors")
	}
}

func TestExecute_CompileErrorsInStderr_Returns2(t *testing.T) {
	dir := t.TempDir()
	// fakeRunner writes an empty XML but also has compile errors in stderr
	emptyXML := []byte(`<?xml version="1.0"?><test-run total="0" passed="0" failed="0" skipped="0" duration="0" result="Passed"></test-run>`)
	fake := &fakeRunner{
		resultsXML: emptyXML,
		stderr:     []byte(`Assets/Foo.cs(1,1): error CS0246: Type 'Bar' not found`),
		exitCode:   0,
	}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	_, code := unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: sw,
	})
	if code != 2 {
		t.Errorf("expected exit 2 when compile errors in stderr, got %d", code)
	}
}
