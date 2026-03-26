package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
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
	saveFunc    func(string, *history.RunResult) error // kept for test injection
	opts        RunCmdOptions
}

func runRun(w io.Writer, deps runDeps) int {
	baseCtx := deps.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	cfg, err := deps.loadConfig("fastplay.json")
	if err != nil {
		writeJSON(w, map[string]any{"schema_version": "1", "error": err.Error(), "new_failures": nil})
		return 5
	}
	if err := cfg.Validate(true); err != nil {
		writeJSON(w, map[string]any{"schema_version": "1", "error": err.Error(), "new_failures": nil})
		return 1
	}

	ctx, cancel := context.WithTimeout(baseCtx, time.Duration(cfg.Timeout.TotalMs)*time.Millisecond)
	defer cancel()

	if deps.runner == nil {
		deps.runner = &unity.ProcessRunner{UnityPath: cfg.UnityPath}
	}
	if deps.resultStore == nil {
		deps.resultStore = history.NewStore(cfg.ResultDir)
	}

	artifactRoot := filepath.Join(filepath.Dir(cfg.ResultDir), "runs")
	svc := &runsvc.Service{
		Runner:       deps.runner,
		Store:        deps.resultStore,
		Artifacts:    artifacts.NewStore(artifactRoot),
		StatusWriter: status.NewWriter(deps.statusPath),
	}
	// Allow test injection of saveFunc.
	if deps.saveFunc != nil {
		svc.Store = &saveOverrideStore{inner: deps.resultStore, save: deps.saveFunc}
	}

	resp, infraErr := svc.Run(ctx, runsvc.Request{
		Config:     cfg,
		Filter:     deps.opts.Filter,
		Category:   deps.opts.Category,
		CompareRun: deps.opts.CompareRun,
	})
	if infraErr != nil {
		writeJSON(w, map[string]any{"schema_version": "1", "error": infraErr.Error(), "new_failures": nil})
		return 1
	}

	result := resp.Result
	output := map[string]any{
		"schema_version": "1",
		"run_id":         resp.RunID,
		"exit_code":      resp.ExitCode,
		"total":          len(result.Tests),
		"passed":         countByResult(result.Tests, "Passed"),
		"failed":         countByResult(result.Tests, "Failed"),
		"skipped":        countByResult(result.Tests, "Skipped"),
		"tests":          result.Tests,
		"errors":         result.Errors,
		"new_failures":   result.NewFailures,
	}
	if result.TimeoutType != "" {
		output["timeout_type"] = result.TimeoutType
	}
	for _, w2 := range resp.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w2)
		output["warning"] = w2 // last warning wins (Phase A: at most one)
	}

	writeJSON(w, output)
	return resp.ExitCode
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

// saveOverrideStore wraps history.Store but substitutes the Save method.
// Used only in tests that inject a failing saveFunc.
// Note: this adapter is only used in TestRunCmd_SaveFailure_IncludesWarning,
// which does NOT set CompareRun, so Load() is never called in that test path.
type saveOverrideStore struct {
	inner *history.Store
	save  func(string, *history.RunResult) error
}

func (s *saveOverrideStore) Save(runID string, r *history.RunResult) error {
	return s.save(runID, r)
}

func (s *saveOverrideStore) Load(runID string) (*history.RunResult, error) {
	return s.inner.Load(runID)
}

// runFilter, runCategory and runCompareRun are cobra flag values.
var runFilter, runCategory, runCompareRun string

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute Unity tests",
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
