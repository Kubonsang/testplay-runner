package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

// RunCmdOptions holds the flag values for `fastplay run`.
type RunCmdOptions struct {
	Filter     string
	Category   string
	CompareRun string
}

type runDeps struct {
	ctx         context.Context
	loadConfig  func(string) (*config.Config, error)
	runner      unity.Runner
	statusPath  string
	resultStore *history.Store
	saveFunc    func(string, *history.RunResult) error
	opts        RunCmdOptions
}

func runRun(w io.Writer, deps runDeps) int {
	baseCtx := deps.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	// Load config
	cfg, err := deps.loadConfig("fastplay.json")
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "new_failures": nil})
		return 5
	}
	if err := cfg.Validate(); err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "new_failures": nil})
		return 1
	}

	// Apply total timeout from config so Unity cannot hang forever.
	ctx, cancel := context.WithTimeout(baseCtx, time.Duration(cfg.Timeout.TotalMs)*time.Millisecond)
	defer cancel()

	// Lazily initialise runner and store from config when not injected (production path)
	if deps.runner == nil {
		deps.runner = &unity.ProcessRunner{UnityPath: cfg.UnityPath}
	}
	if deps.resultStore == nil {
		deps.resultStore = history.NewStore(cfg.ResultDir)
	}

	// Generate run_id
	runID := time.Now().Format("20060102-150405")

	// Create temp directory for results XML
	tmpDir, err := os.MkdirTemp("", "fastplay-*")
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "new_failures": nil})
		return 1
	}
	defer os.RemoveAll(tmpDir)
	resultsFile := filepath.Join(tmpDir, "results.xml")

	// Build execution options
	execOpts := unity.ExecuteOptions{
		ProjectPath:      cfg.ProjectPath,
		ResultsFile:      resultsFile,
		StatusWriter:     status.NewWriter(deps.statusPath),
		TimeoutType:      "total",
		Filter:           deps.opts.Filter,
		Category:         deps.opts.Category,
		CompileTimeoutMs: cfg.Timeout.CompileMs,
	}

	// Execute
	result, exitCode := unity.Execute(ctx, deps.runner, execOpts)
	result.RunID = runID
	result.SchemaVersion = "1"

	// Fix File fields to be relative to the project path
	for i := range result.Tests {
		if result.Tests[i].AbsolutePath != "" {
			result.Tests[i].File = parser.MakeRelative(cfg.ProjectPath, result.Tests[i].AbsolutePath)
		}
	}
	for i := range result.Errors {
		if result.Errors[i].AbsolutePath != "" {
			result.Errors[i].File = parser.MakeRelative(cfg.ProjectPath, result.Errors[i].AbsolutePath)
		}
	}

	// Compare runs if requested — newFailures stays nil when no --compare-run
	var newFailures []parser.TestCase
	if deps.opts.CompareRun != "" {
		prevResult, loadErr := deps.resultStore.Load(deps.opts.CompareRun)
		if loadErr == nil {
			newFailures = history.Compare(prevResult, result)
		} else {
			// When compare-run specified but not found, return empty array (not null)
			newFailures = make([]parser.TestCase, 0)
		}
	}

	// Resolve saveFunc: use injected func (for tests) or default to resultStore.Save
	saveFunc := deps.saveFunc
	if saveFunc == nil {
		saveFunc = deps.resultStore.Save
	}

	// Save result
	result.NewFailures = newFailures
	saveErr := saveFunc(runID, result)

	// Build output — newFailures nil → JSON null when no --compare-run
	output := map[string]any{
		"run_id":       runID,
		"exit_code":    exitCode,
		"total":        len(result.Tests),
		"passed":       countByResult(result.Tests, "Passed"),
		"failed":       countByResult(result.Tests, "Failed"),
		"skipped":      countByResult(result.Tests, "Skipped"),
		"tests":        result.Tests,
		"errors":       result.Errors,
		"new_failures": newFailures,
	}
	if result.TimeoutType != "" {
		output["timeout_type"] = result.TimeoutType
	}
	if saveErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to save result: %v\n", saveErr)
		output["warning"] = "result not saved: " + saveErr.Error()
	}

	writeJSON(w, output)
	return exitCode
}

func countByResult(tests []parser.TestCase, result string) int {
	n := 0
	for _, tc := range tests {
		if tc.Result == result {
			n++
		}
	}
	return n
}

// runFilter, runCategory and runCompareRun are cobra flag values.
var runFilter, runCategory, runCompareRun string

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute Unity Play Mode tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		statusPath := "fastplay-status.json"
		sigCh := setupSignals()
		go watchSignals(ctx, cancel, sigCh, func() {
			// Best-effort: write interrupted status so pollers see the phase change
			_ = status.NewWriter(statusPath).Write(status.Status{Phase: status.PhaseInterrupted})
		})

		deps := runDeps{
			ctx:        ctx,
			loadConfig: config.Load,
			// runner and resultStore are intentionally nil; runRun initialises them
			// from config after loading, avoiding a double config-load.
			statusPath: statusPath,
			opts: RunCmdOptions{
				Filter:     runFilter,
				Category:   runCategory,
				CompareRun: runCompareRun,
			},
		}
		code := runRun(cmd.OutOrStdout(), deps)
		os.Exit(code)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringVar(&runFilter, "filter", "", "Test name filter")
	runCmd.Flags().StringVar(&runCategory, "category", "", "Test category filter")
	runCmd.Flags().StringVar(&runCompareRun, "compare-run", "", "Run ID to compare against for regression detection")
}
