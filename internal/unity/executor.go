package unity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

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
}

// Execute runs Unity tests using the provided Runner and returns the result + exit code.
//
// Exit codes:
//
//	0 = all tests passed
//	2 = compile failure (no results XML produced, or compile errors in stderr)
//	3 = test failure (results XML exists but contains failures)
//	4 = timeout (DeadlineExceeded) or signal interruption (Canceled)
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
	if opts.CompileMs > 0 && opts.TestMs > 0 {
		return executeTwoPhase(ctx, runner, opts)
	}
	return executeSinglePhase(ctx, runner, opts)
}

// executeSinglePhase runs compile + test in a single Unity invocation.
func executeSinglePhase(ctx context.Context, runner Runner, opts ExecuteOptions) (*history.RunResult, int) {
	// Phase: compiling
	if opts.StatusWriter != nil {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseCompiling})
	}

	runOpts := &RunOptions{
		ResultsFilePath: opts.ResultsFile,
		Filter:          opts.Filter,
		Category:        opts.Category,
		TestPlatform:    opts.TestPlatform,
	}
	args := BuildRunArgs(opts.ProjectPath, runOpts)

	_, stderr, _, err := runner.Run(ctx, args)

	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return handleContextErr(err, opts)
	}

	// Phase: running (Unity finished compilation, now checking results)
	if opts.StatusWriter != nil {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseRunning})
	}

	return parseResults(opts, stderr)
}

// executeTwoPhase runs compile and test as separate Unity invocations, each
// with its own deadline (CompileMs and TestMs respectively).
func executeTwoPhase(ctx context.Context, runner Runner, opts ExecuteOptions) (*history.RunResult, int) {
	// ── Phase 1: compile only ──────────────────────────────────────────────
	if opts.StatusWriter != nil {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseCompiling})
	}

	compileCtx, compileCancel := context.WithTimeout(ctx, time.Duration(opts.CompileMs)*time.Millisecond)
	defer compileCancel()

	_, stderr, exitCode, err := runner.Run(compileCtx, BuildCompileArgs(opts.ProjectPath))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return classifyPhaseContextErr(ctx, opts, status.PhaseTimeoutCompile, "compile")
		}
		// Non-context runner error (e.g. Unity binary missing) → compile failure.
		if opts.StatusWriter != nil {
			_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		}
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      2,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{},
		}, 2
	}

	// Compile errors in stderr → fail without running tests.
	if compileErrors := ParseCompileErrorsWithProject(stderr, opts.ProjectPath); len(compileErrors) > 0 {
		if opts.StatusWriter != nil {
			_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		}
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      2,
			Tests:         []parser.TestCase{},
			Errors:        compileErrors,
		}, 2
	}

	// Non-zero exit with no recognisable compile errors (e.g. license failure, bad args).
	if exitCode != 0 {
		if opts.StatusWriter != nil {
			_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		}
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      2,
			Tests:         []parser.TestCase{},
			Errors: []history.CompileError{
				{Message: fmt.Sprintf("compile phase exited with code %d (no compile errors in stderr)", exitCode)},
			},
		}, 2
	}

	// ── Phase 2: run tests ─────────────────────────────────────────────────
	if opts.StatusWriter != nil {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseRunning})
	}

	testCtx, testCancel := context.WithTimeout(ctx, time.Duration(opts.TestMs)*time.Millisecond)
	defer testCancel()

	runOpts := &RunOptions{
		ResultsFilePath: opts.ResultsFile,
		Filter:          opts.Filter,
		Category:        opts.Category,
		TestPlatform:    opts.TestPlatform,
	}
	_, testStderr, _, testErr := runner.Run(testCtx, BuildRunArgs(opts.ProjectPath, runOpts))
	if testErr != nil {
		if errors.Is(testErr, context.DeadlineExceeded) || errors.Is(testErr, context.Canceled) {
			return classifyPhaseContextErr(ctx, opts, status.PhaseTimeoutTest, "test")
		}
		// Non-context runner error in test phase → no results available.
		if opts.StatusWriter != nil {
			_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		}
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      2,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{},
		}, 2
	}

	return parseResults(opts, testStderr)
}

// classifyPhaseContextErr determines the correct exit result when a phase context
// fires (DeadlineExceeded or Canceled). It checks the outer ctx to distinguish
// between a total_ms deadline, a signal interruption, and a phase-only deadline.
func classifyPhaseContextErr(ctx context.Context, opts ExecuteOptions, phaseTimeoutStatus status.Phase, phaseTimeoutType string) (*history.RunResult, int) {
	if outerErr := ctx.Err(); outerErr != nil {
		if errors.Is(outerErr, context.DeadlineExceeded) {
			// Outer total_ms deadline fired.
			if opts.StatusWriter != nil {
				_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseTimeoutTotal})
			}
			return &history.RunResult{
				SchemaVersion: "1",
				ExitCode:      4,
				TimeoutType:   "total",
				Tests:         []parser.TestCase{},
				Errors:        []history.CompileError{},
			}, 4
		}
		// context.Canceled → signal interruption.
		if opts.StatusWriter != nil {
			_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseInterrupted})
		}
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      4,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{},
		}, 4
	}
	// Outer ctx is still alive — only the phase deadline fired.
	if opts.StatusWriter != nil {
		_ = opts.StatusWriter.Write(status.Status{Phase: phaseTimeoutStatus})
	}
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
func handleContextErr(err error, opts ExecuteOptions) (*history.RunResult, int) {
	if errors.Is(err, context.DeadlineExceeded) {
		if opts.StatusWriter != nil {
			_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseTimeoutTotal})
		}
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      4,
			TimeoutType:   opts.TimeoutType,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{},
		}, 4
	}
	if errors.Is(err, context.Canceled) {
		if opts.StatusWriter != nil {
			_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseInterrupted})
		}
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      4,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{},
		}, 4
	}
	// Should not be reached when the caller guards correctly.
	if opts.StatusWriter != nil {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseInterrupted})
	}
	return &history.RunResult{
		SchemaVersion: "1",
		ExitCode:      4,
		Tests:         []parser.TestCase{},
		Errors:        []history.CompileError{},
	}, 4
}

// parseResults reads the XML results file and returns the run result.
// It is shared between single-phase and two-phase executors.
func parseResults(opts ExecuteOptions, stderr []byte) (*history.RunResult, int) {
	// Check for results XML
	xmlData, xmlErr := os.ReadFile(opts.ResultsFile)
	if xmlErr != nil {
		// No XML file — compile failure
		if opts.StatusWriter != nil {
			_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		}
		compileErrors := ParseCompileErrorsWithProject(stderr, opts.ProjectPath)
		return &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      2,
			Tests:         []parser.TestCase{},
			Errors:        compileErrors,
		}, 2
	}

	// Even if XML was produced, check stderr for compile errors
	// (Unity can emit compile errors and produce a partial/empty XML)
	compileErrors := ParseCompileErrorsWithProject(stderr, opts.ProjectPath)
	if len(compileErrors) > 0 {
		if opts.StatusWriter != nil {
			_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		}
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
	}

	if opts.StatusWriter != nil {
		_ = opts.StatusWriter.Write(status.Status{
			Phase:    status.PhaseDone,
			Total:    parseResult.Total,
			Passed:   parseResult.Passed,
			Failed:   parseResult.Failed,
			ExitCode: &exitCode,
		})
	}

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
