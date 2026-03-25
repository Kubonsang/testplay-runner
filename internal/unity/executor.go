package unity

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

// ExecuteOptions configures a Unity test execution.
type ExecuteOptions struct {
	ProjectPath  string
	ResultsFile  string
	StatusWriter status.WriterInterface
	TimeoutType  string // "total" — propagated to RunResult.TimeoutType on context cancellation
	Filter       string
	Category     string
}

// Execute runs Unity tests using the provided Runner and returns the result + exit code.
//
// Exit codes:
//
//	0 = all tests passed
//	2 = compile failure (no results XML produced, or compile errors in stderr)
//	3 = test failure (results XML exists but contains failures)
//	4 = timeout / context cancelled
func Execute(ctx context.Context, runner Runner, opts ExecuteOptions) (*history.RunResult, int) {
	// Phase: compiling
	if opts.StatusWriter != nil {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseCompiling})
	}

	// Build args
	runOpts := &RunOptions{
		ResultsFilePath: opts.ResultsFile,
		Filter:          opts.Filter,
		Category:        opts.Category,
	}
	args := BuildRunArgs(opts.ProjectPath, runOpts)

	// Run Unity
	_, stderr, _, err := runner.Run(ctx, args)

	// Check for context cancellation (timeout / interrupt)
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
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

	// Phase: running (Unity finished compilation, now checking results)
	if opts.StatusWriter != nil {
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseRunning})
	}

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
			Phase:  status.PhaseDone,
			Total:  parseResult.Total,
			Passed: parseResult.Passed,
			Failed: parseResult.Failed,
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
