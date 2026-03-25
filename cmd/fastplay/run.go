package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/fastplay/runner/internal/config"
	"github.com/fastplay/runner/internal/history"
	"github.com/fastplay/runner/internal/parser"
	"github.com/fastplay/runner/internal/status"
	"github.com/fastplay/runner/internal/unity"
)

// RunCmdOptions holds the flag values for `fastplay run`.
type RunCmdOptions struct {
	Filter     string
	Category   string
	CompareRun string
}

type runDeps struct {
	loadConfig  func(string) (*config.Config, error)
	runner      unity.Runner
	statusPath  string
	resultStore *history.Store
	opts        RunCmdOptions
}

func runRun(w io.Writer, deps runDeps) int {
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
		ProjectPath:  cfg.ProjectPath,
		ResultsFile:  resultsFile,
		StatusWriter: status.NewWriter(deps.statusPath),
		TimeoutType:  "total",
		Filter:       deps.opts.Filter,
		Category:     deps.opts.Category,
	}

	// Execute
	ctx := context.Background()
	result, exitCode := unity.Execute(ctx, deps.runner, execOpts)
	result.RunID = runID
	result.SchemaVersion = "1"

	// Ensure tests is never null
	if result.Tests == nil {
		result.Tests = make([]parser.TestCase, 0)
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

	// Save result
	result.NewFailures = newFailures
	_ = deps.resultStore.Save(runID, result)

	// Build output — newFailures nil → JSON null when no --compare-run
	output := map[string]any{
		"run_id":       runID,
		"exit_code":    exitCode,
		"total":        len(result.Tests),
		"passed":       countByResult(result.Tests, "Passed"),
		"failed":       countByResult(result.Tests, "Failed"),
		"tests":        result.Tests,
		"errors":       result.Errors,
		"new_failures": newFailures,
	}
	if result.TimeoutType != "" {
		output["timeout_type"] = result.TimeoutType
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
		cfg, err := config.Load("fastplay.json")
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}

		runner := &unity.ProcessRunner{UnityPath: cfg.UnityPath}
		store := history.NewStore(cfg.ResultDir)

		deps := runDeps{
			loadConfig:  config.Load,
			runner:      runner,
			statusPath:  "fastplay-status.json",
			resultStore: store,
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
