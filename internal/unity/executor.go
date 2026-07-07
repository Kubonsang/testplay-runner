package unity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

// ErrSignalInterrupt is set as the context cancel cause when SIGINT or SIGTERM
// is received. Executors check context.Cause(ctx) against this sentinel to
// distinguish signal interruption (exit 8) from timeout (exit 4).
var ErrSignalInterrupt = errors.New("signal interrupt")

// ExitNoTestsMatched (10) is returned when the run produced a valid result
// with zero executed tests while an explicit --filter/--category was given.
// Nothing was verified, so exit 0 ("all tests passed") would be a lie the
// caller cannot detect — the declared most-expensive silent failure.
const ExitNoTestsMatched = 10

// noopStatusWriter implements status.WriterInterface but discards all writes.
// It is used as a sentinel when ExecuteOptions.StatusWriter is nil so that
// all call sites can write unconditionally without nil guards.
type noopStatusWriter struct{}

func (noopStatusWriter) Write(_ status.Status) error { return nil }

// maxTailBytes is the maximum number of stderr bytes retained in a tailBuffer.
const maxTailBytes = 64 * 1024

// tailBuffer retains the last maxTailBytes of data written to it using a
// fixed-size ring buffer. After the initial fill, Write never allocates.
type tailBuffer struct {
	buf  [maxTailBytes]byte
	size int // number of valid bytes in buf (0..maxTailBytes)
	head int // index where the next byte will be written
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > maxTailBytes {
		// Only the last maxTailBytes of p are relevant.
		p = p[len(p)-maxTailBytes:]
		// Writing exactly maxTailBytes resets the ring to a linear state.
		copy(t.buf[:], p)
		t.head = 0
		t.size = maxTailBytes
		return n, nil
	}
	// Write p into the ring, wrapping around as needed.
	space := maxTailBytes - t.head
	if len(p) <= space {
		copy(t.buf[t.head:], p)
		t.head += len(p)
		if t.head == maxTailBytes {
			t.head = 0
		}
	} else {
		copy(t.buf[t.head:], p[:space])
		copy(t.buf[:], p[space:])
		t.head = len(p) - space
	}
	if t.size < maxTailBytes {
		t.size += len(p)
		if t.size > maxTailBytes {
			t.size = maxTailBytes
		}
	}
	return n, nil
}

// Bytes returns the retained bytes in order (oldest first).
func (t *tailBuffer) Bytes() []byte {
	if t.size == 0 {
		return nil
	}
	out := make([]byte, t.size)
	if t.size < maxTailBytes {
		// Buffer not yet full: data is linear from index 0.
		copy(out, t.buf[:t.size])
		return out
	}
	// Buffer full: oldest byte is at t.head.
	n := copy(out, t.buf[t.head:])
	copy(out[n:], t.buf[:t.head])
	return out
}

// ExecuteOptions configures a Unity test execution.
type ExecuteOptions struct {
	ProjectPath  string
	ResultsFile  string
	StatusWriter status.WriterInterface
	TimeoutType  string // "total" — propagated to RunResult.TimeoutType on deadline exceeded
	Filter       string
	Category     string
	TestPlatform string // "edit_mode" | "play_mode"; forwarded to BuildRunArgs

	// CompileMs and TestMs enable two-phase execution when both are > 0.
	// Phase 1 runs compile-only with CompileMs deadline (emits timeout_compile on expiry).
	// Phase 2 runs tests with TestMs deadline (emits timeout_test on expiry).
	// When either is zero, single-phase execution with the parent ctx is used.
	CompileMs int64
	TestMs    int64

	// StdoutWriter and StderrWriter receive Unity process output as it is produced.
	// If nil, the corresponding output is discarded (compile-error detection uses
	// an internal tail buffer regardless).
	StdoutWriter io.Writer
	StderrWriter io.Writer

	// ExtraArgs are appended verbatim to the Unity CLI arguments for every
	// invocation (compile and test phases). Used by Shadow Workspace mode to
	// inject -disable-assembly-updater.
	ExtraArgs []string
}

// Execute runs Unity tests using the provided Runner and returns the result + exit code.
//
// Exit codes:
//
//	0 = all tests passed
//	2 = compile failure (no results XML produced, or compile errors in stderr)
//	3 = test failure (results XML exists but contains failures)
//	4 = timeout (DeadlineExceeded)
//	8 = signal interruption (context.Canceled with ErrSignalInterrupt cause)
//
// When CompileMs and TestMs are both > 0, two-phase execution is used:
// phase 1 compiles with a CompileMs deadline (emits timeout_type "compile"),
// phase 2 runs tests with a TestMs deadline (emits timeout_type "test").
// Otherwise, single-phase execution runs compile+test in one Unity invocation.
//
// Current limitations:
//   - The "running" phase is written before Unity starts the test invocation
//     in two-phase mode, but after Unity exits in single-phase mode.
//   - No intra-process log streaming: Unity stdout/stderr is buffered until exit.
//   - Multi-process network harness (NGO server+client) is not supported; that
//     would require a different orchestration layer above Execute.
func Execute(ctx context.Context, runner Runner, opts ExecuteOptions) (*history.RunResult, int) {
	if opts.StatusWriter == nil {
		opts.StatusWriter = noopStatusWriter{}
	}
	if opts.CompileMs > 0 && opts.TestMs > 0 {
		return executeTwoPhase(ctx, runner, opts)
	}
	return executeSinglePhase(ctx, runner, opts)
}

// executeSinglePhase runs compile + test in a single Unity invocation.
func executeSinglePhase(ctx context.Context, runner Runner, opts ExecuteOptions) (*history.RunResult, int) {
	// Phase: compiling
	_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseCompiling})

	runOpts := &RunOptions{
		ResultsFilePath: opts.ResultsFile,
		Filter:          opts.Filter,
		Category:        opts.Category,
		TestPlatform:    opts.TestPlatform,
	}
	args := BuildRunArgs(opts.ProjectPath, runOpts)
	args = append(args, opts.ExtraArgs...)

	stdoutW, stderrW, tail := makeRunWriters(opts)
	_, err := runner.Run(ctx, args, stdoutW, stderrW)

	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return handleContextErr(ctx, err, opts)
		}
		return classifyLaunchFailure(err, opts)
	}

	return parseResults(opts, tail.Bytes())
}

// executeTwoPhase runs compile and test as separate Unity invocations, each
// with its own deadline (CompileMs and TestMs respectively).
func executeTwoPhase(ctx context.Context, runner Runner, opts ExecuteOptions) (*history.RunResult, int) {
	// ── Phase 1: compile only ──────────────────────────────────────────────
	_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseCompiling})

	compileCtx, compileCancel := context.WithTimeout(ctx, time.Duration(opts.CompileMs)*time.Millisecond)
	defer compileCancel()

	stdoutW, stderrW1, compileTail := makeRunWriters(opts)
	compileArgs := BuildCompileArgs(opts.ProjectPath)
	compileArgs = append(compileArgs, opts.ExtraArgs...)
	exitCode, err := runner.Run(compileCtx, compileArgs, stdoutW, stderrW1)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return classifyPhaseContextErr(ctx, opts, status.PhaseTimeoutCompile, "compile")
		}
		// Non-context runner error: Unity never ran (binary missing, path is a
		// directory, not executable) — a dependency error, not a compile failure.
		return classifyLaunchFailure(err, opts)
	}

	// Compile errors in stderr → fail without running tests.
	if compileErrors := ParseCompileErrorsWithProject(compileTail.Bytes(), opts.ProjectPath); len(compileErrors) > 0 {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      2,
			Tests:         []parser.TestCase{},
			Errors:        compileErrors,
		}, 2
	}

	// Non-zero exit with no recognisable C# compile errors.
	if exitCode != 0 {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		// License / build-target failures are known exit 6 cases. Unknown
		// non-zero exits without C# errors also stay in exit 6 because they
		// point at Unity invocation/import state rather than source compilation.
		if ParseBuildFailure(compileTail.Bytes()) {
			return &history.RunResult{
				SchemaVersion: "1",
				ExitCode:      6,
				Tests:         []parser.TestCase{},
				Errors:        []history.CompileError{},
			}, 6
		}
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      6,
			Tests:         []parser.TestCase{},
			Errors: []history.CompileError{
				{Message: fmt.Sprintf("Unity compile invocation exited with code %d (no C# compile errors in stderr); check Unity/editor logs, package import, shadow workspace cold import, license, or build-target setup", exitCode)},
			},
		}, 6
	}

	// ── Phase 2: run tests ─────────────────────────────────────────────────
	_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseRunning})

	testCtx, testCancel := context.WithTimeout(ctx, time.Duration(opts.TestMs)*time.Millisecond)
	defer testCancel()

	runOpts := &RunOptions{
		ResultsFilePath: opts.ResultsFile,
		Filter:          opts.Filter,
		Category:        opts.Category,
		TestPlatform:    opts.TestPlatform,
	}
	_, stderrW2, testTail := makeRunWriters(opts)
	testArgs := BuildRunArgs(opts.ProjectPath, runOpts)
	testArgs = append(testArgs, opts.ExtraArgs...)
	_, testErr := runner.Run(testCtx, testArgs, stdoutW, stderrW2)
	if testErr != nil {
		if errors.Is(testErr, context.DeadlineExceeded) || errors.Is(testErr, context.Canceled) {
			return classifyPhaseContextErr(ctx, opts, status.PhaseTimeoutTest, "test")
		}
		// Non-context runner error in test phase: Unity never ran.
		return classifyLaunchFailure(testErr, opts)
	}

	return parseResults(opts, testTail.Bytes())
}

// classifyPhaseContextErr determines the correct exit result when a phase context
// fires (DeadlineExceeded or Canceled). It checks the outer ctx to distinguish
// between a total_ms deadline, a signal interruption, and a phase-only deadline.
func classifyPhaseContextErr(ctx context.Context, opts ExecuteOptions, phaseTimeoutStatus status.Phase, phaseTimeoutType string) (*history.RunResult, int) {
	if outerErr := ctx.Err(); outerErr != nil {
		if errors.Is(outerErr, context.DeadlineExceeded) {
			// Outer total_ms deadline fired.
			_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseTimeoutTotal})
			return &history.RunResult{
				SchemaVersion: "1",
				ExitCode:      4,
				TimeoutType:   "total",
				Tests:         []parser.TestCase{},
				Errors:        []history.CompileError{},
			}, 4
		}
		// context.Canceled → signal interruption or unknown cancellation.
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseInterrupted})
		exitCode := 4
		if errors.Is(context.Cause(ctx), ErrSignalInterrupt) {
			exitCode = 8
		}
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      exitCode,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{},
		}, exitCode
	}
	// Outer ctx is still alive — only the phase deadline fired.
	_ = opts.StatusWriter.Write(status.Status{Phase: phaseTimeoutStatus})
	return &history.RunResult{
		SchemaVersion: "1",
		ExitCode:      4,
		TimeoutType:   phaseTimeoutType,
		Tests:         []parser.TestCase{},
		Errors:        []history.CompileError{},
	}, 4
}

// handleContextErr maps context errors to the appropriate exit result.
// Callers must guard with errors.Is(err, context.Canceled||DeadlineExceeded).
func handleContextErr(ctx context.Context, err error, opts ExecuteOptions) (*history.RunResult, int) {
	if errors.Is(err, context.DeadlineExceeded) {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseTimeoutTotal})
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      4,
			TimeoutType:   opts.TimeoutType,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{},
		}, 4
	}
	if errors.Is(err, context.Canceled) {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseInterrupted})
		exitCode := 4
		if errors.Is(context.Cause(ctx), ErrSignalInterrupt) {
			exitCode = 8
		}
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      exitCode,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{},
		}, exitCode
	}
	// Should not be reached when the caller guards correctly.
	_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseInterrupted})
	return &history.RunResult{
		SchemaVersion: "1",
		ExitCode:      4,
		Tests:         []parser.TestCase{},
		Errors:        []history.CompileError{},
	}, 4
}

// classifyLaunchFailure maps a non-context runner error — Unity could not be
// started at all (binary missing, unity_path is a directory such as a macOS
// .app bundle, not executable) — to exit 1 (dependency error) with a hint,
// instead of a fabricated exit 2 "compile failure" with empty errors[].
func classifyLaunchFailure(err error, opts ExecuteOptions) (*history.RunResult, int) {
	_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
	hint := "unity_path must point to the Unity executable itself (macOS: Unity.app/Contents/MacOS/Unity, not the .app bundle); verify with 'testplay check'"
	if errors.Is(err, os.ErrNotExist) {
		hint = "Unity binary not found — set unity_path in testplay.json (or the UNITY_PATH env var); verify with 'testplay check'"
	}
	return &history.RunResult{
		SchemaVersion: "1",
		ExitCode:      1,
		Error:         fmt.Sprintf("failed to launch Unity: %v", err),
		Hint:          hint,
		Tests:         []parser.TestCase{},
		Errors:        []history.CompileError{},
	}, 1
}

// makeRunWriters returns the stdout writer, a tee'd stderr writer, and the
// underlying tail buffer. The tail buffer captures the last maxTailBytes of
// stderr for compile-error detection regardless of whether StderrWriter is set.
func makeRunWriters(opts ExecuteOptions) (stdoutW io.Writer, stderrW io.Writer, tail *tailBuffer) {
	tail = &tailBuffer{}
	if opts.StdoutWriter != nil {
		stdoutW = opts.StdoutWriter
	} else {
		stdoutW = io.Discard
	}
	if opts.StderrWriter != nil {
		stderrW = io.MultiWriter(opts.StderrWriter, tail)
	} else {
		stderrW = tail
	}
	return
}

// classifyNoResults builds the RunResult + exit code for the case where no
// results XML was produced. It is the single source of truth for the 2-vs-6
// split and is shared by two callers so they can never drift:
//
//   - the batchmode executor (parseResults), with compile errors scraped from
//     Unity stderr and buildFailure from ParseBuildFailure; and
//   - the bridge executor (ExecuteBridge), with compile errors read from the
//     bridge's compile-errors.json sidecar and buildFailure from the bridge
//     response outcome.
//
// When compileErrors is non-empty → exit 2 with those errors. Otherwise when
// buildFailure is true → exit 6 (license/build-target). Otherwise → exit 2
// with no errors (unknown non-zero outcome producing no results).
//
// It does not write status; callers own the PhaseDone write.
func classifyNoResults(compileErrors []history.CompileError, buildFailure bool) (*history.RunResult, int) {
	if len(compileErrors) == 0 && buildFailure {
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      6,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{},
		}, 6
	}
	return &history.RunResult{
		SchemaVersion: "1",
		ExitCode:      2,
		Tests:         []parser.TestCase{},
		Errors:        compileErrors,
	}, 2
}

// parseResults reads the XML results file and returns the run result.
// It is shared between single-phase and two-phase executors.
func parseResults(opts ExecuteOptions, stderrTail []byte) (*history.RunResult, int) {
	// Check for results XML
	xmlData, xmlErr := os.ReadFile(opts.ResultsFile)
	if xmlErr != nil {
		// No XML file — determine cause from stderr.
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		compileErrors := ParseCompileErrorsWithProject(stderrTail, opts.ProjectPath)
		return classifyNoResults(compileErrors, ParseBuildFailure(stderrTail))
	}

	// Even if XML was produced, check stderr for compile errors
	// (Unity can emit compile errors and produce a partial/empty XML)
	compileErrors := ParseCompileErrorsWithProject(stderrTail, opts.ProjectPath)
	if len(compileErrors) > 0 {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      2,
			Tests:         []parser.TestCase{},
			Errors:        compileErrors,
		}, 2
	}

	// Parse XML
	parseResult, parseErr := parser.Parse(xmlData)
	if parseErr != nil {
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      2,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{{Message: fmt.Sprintf("failed to parse test results: %v", parseErr)}},
		}, 2
	}

	exitCode := 0
	if parseResult.Failed > 0 {
		exitCode = 3
	} else if parseResult.Total == 0 && (opts.Filter != "" || opts.Category != "") {
		// An explicit selection that matched nothing verified nothing;
		// exit 0 here would greenlight a run that never executed.
		exitCode = ExitNoTestsMatched
	}

	_ = opts.StatusWriter.Write(status.Status{
		Phase:    status.PhaseDone,
		Total:    parseResult.Total,
		Passed:   parseResult.Passed,
		Failed:   parseResult.Failed,
		ExitCode: &exitCode,
	})

	return &history.RunResult{
		SchemaVersion: "1",
		ExitCode:      exitCode,
		Total:         parseResult.Total,
		Passed:        parseResult.Passed,
		Failed:        parseResult.Failed,
		Skipped:       parseResult.Skipped,
		Tests:         parseResult.Tests,
		Errors:        []history.CompileError{},
	}, exitCode
}
