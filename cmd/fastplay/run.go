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
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

// RunCmdOptions holds the flag values for `fastplay run`.
type RunCmdOptions struct {
	Filter      string
	Category    string
	CompareRun  string
	ResetShadow bool
	ForceShadow bool // activate shadow workspace without resetting Library cache
}

type runDeps struct {
	ctx         context.Context
	loadConfig  func(string) (*config.Config, error)
	runner      unity.Runner
	statusPath  string
	resultStore *history.Store
	opts        RunCmdOptions
}

func runRun(w io.Writer, deps runDeps) int {
	baseCtx := deps.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	cfg, err := deps.loadConfig("fastplay.json")
	if err != nil {
		writeJSON(w, map[string]any{"schema_version": "1", "error": err.Error()})
		return 5
	}
	if err := cfg.Validate(true); err != nil {
		writeJSON(w, map[string]any{"schema_version": "1", "error": err.Error()})
		return 5
	}

	ctx, cancel := context.WithTimeout(baseCtx, time.Duration(cfg.Timeout.TotalMs)*time.Millisecond)
	defer cancel()

	if deps.runner == nil {
		deps.runner = &unity.ProcessRunner{UnityPath: cfg.UnityPath}
	}
	if deps.resultStore == nil {
		deps.resultStore = history.NewStore(cfg.ResultDir)
	}

	artifactRoot := filepath.Join(cfg.ProjectPath, ".fastplay", "runs")
	svc := &runsvc.Service{
		Runner:       deps.runner,
		Store:        deps.resultStore,
		Artifacts:    artifacts.NewStore(artifactRoot),
		StatusWriter: status.NewWriter(deps.statusPath),
	}

	resp, infraErr := svc.Run(ctx, runsvc.Request{
		Config:      cfg,
		Filter:      deps.opts.Filter,
		Category:    deps.opts.Category,
		CompareRun:  deps.opts.CompareRun,
		ResetShadow: deps.opts.ResetShadow,
		ForceShadow: deps.opts.ForceShadow,
	})
	if infraErr != nil {
		writeJSON(w, map[string]any{"schema_version": "1", "error": infraErr.Error()})
		return 1
	}

	result := resp.Result
	output := map[string]any{
		"schema_version": "1",
		"run_id":         resp.RunID,
		"exit_code":      resp.ExitCode,
		"total":          result.Total,
		"passed":         result.Passed,
		"failed":         result.Failed,
		"skipped":        result.Skipped,
		"tests":          result.Tests,
		"errors":         result.Errors,
		"new_failures":   result.NewFailures,
	}
	if result.TimeoutType != "" {
		output["timeout_type"] = result.TimeoutType
	}
	if len(resp.Warnings) > 0 {
		for _, w2 := range resp.Warnings {
			fmt.Fprintln(os.Stderr, "warning:", w2)
		}
		output["warnings"] = resp.Warnings
	}

	writeJSON(w, output)
	return resp.ExitCode
}

// runFilter, runCategory, runCompareRun, resetShadow and forceShadow are cobra flag values.
var runFilter, runCategory, runCompareRun string
var resetShadow bool
var forceShadow bool

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
				Filter:      runFilter,
				Category:    runCategory,
				CompareRun:  runCompareRun,
				ResetShadow: resetShadow,
				ForceShadow: forceShadow,
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
	runCmd.Flags().BoolVar(&resetShadow, "reset-shadow", false, "Delete and rebuild shadow workspace before running")
	runCmd.Flags().BoolVar(&forceShadow, "shadow", false, "Force shadow workspace even when Unity Editor is not open")
}
