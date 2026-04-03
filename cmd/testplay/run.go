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
	"github.com/Kubonsang/testplay-runner/internal/scenario"
	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

// RunCmdOptions holds the flag values for `testplay run`.
type RunCmdOptions struct {
	Filter      string
	Category    string
	CompareRun  string
	ResetShadow bool
	ForceShadow bool // activate shadow workspace without resetting Library cache
	ClearCache  bool // remove cached Library before shadow workspace creation
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

	cfg, err := deps.loadConfig(configPath)
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

	artifactRoot := filepath.Join(cfg.ProjectPath, ".testplay", "runs")
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
		ClearCache:  deps.opts.ClearCache,
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

	// Best-effort prune of old results and artifacts.
	if cfg.Retention.MaxRuns != nil && *cfg.Retention.MaxRuns > 0 {
		_, _ = deps.resultStore.Prune(*cfg.Retention.MaxRuns)
		_, _ = svc.Artifacts.Prune(*cfg.Retention.MaxRuns)
	}

	return resp.ExitCode
}

// scenarioDeps holds injectable dependencies for runScenario.
// In production all fields are zero/nil; runScenario fills in real implementations.
// In tests, run is set to a fake InstanceRunner.
type scenarioDeps struct {
	ctx        context.Context
	run        scenario.InstanceRunner // nil = real runner constructed from each instance's config
	clearCache bool                    // passed through to each instance's runsvc.Request
}

// runScenario loads a scenario file, runs all instances concurrently, and writes
// the aggregated JSON result to w. Returns the scenario exit code (max of instances).
func runScenario(w io.Writer, specPath string, deps scenarioDeps) int {
	ctx := deps.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	spec, err := scenario.Load(specPath)
	if err != nil {
		writeJSON(w, map[string]any{"schema_version": "1", "error": err.Error()})
		return 5
	}

	run := deps.run
	var configs map[string]*config.Config
	if run == nil {
		// Pre-load all instance configs for early validation and scenario-level timeout.
		configs = make(map[string]*config.Config, len(spec.Instances))
		var totalMs int64
		for _, inst := range spec.Instances {
			cfgPath := spec.ConfigPath(inst)
			cfg, loadErr := config.Load(cfgPath)
			if loadErr != nil {
				writeJSON(w, map[string]any{"schema_version": "1", "error": loadErr.Error()})
				return 5
			}
			if valErr := cfg.Validate(true); valErr != nil {
				writeJSON(w, map[string]any{"schema_version": "1", "error": valErr.Error()})
				return 5
			}
			configs[inst.Role] = cfg
			totalMs += cfg.Timeout.TotalMs
		}

		// Outer safety-net: sum of all instance timeouts bounds the entire scenario.
		var scenarioCancel context.CancelFunc
		ctx, scenarioCancel = context.WithTimeout(ctx, time.Duration(totalMs)*time.Millisecond)
		defer scenarioCancel()

		run = func(ctx context.Context, instSpec scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
			cfg := configs[instSpec.Role]

			instanceCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Timeout.TotalMs)*time.Millisecond)
			defer cancel()

			artifactRoot := filepath.Join(cfg.ProjectPath, ".testplay", "runs")
			// Per-instance status file for external polling by agents.
			var sw status.WriterInterface = status.NewWriter(fmt.Sprintf("testplay-status-%s.json", instSpec.Role))
			// Wrap with ReadyNotifier so this instance signals its readyCh when the
			// target phase is reached. readyCh is nil for instances with no dependents.
			if readyCh != nil {
				sw = scenario.NewReadyNotifier(sw, instSpec.EffectiveReadyPhase(), readyCh)
			}

			svc := &runsvc.Service{
				Runner:       &unity.ProcessRunner{UnityPath: cfg.UnityPath, Env: instSpec.Env},
				Store:        history.NewStore(cfg.ResultDir),
				Artifacts:    artifacts.NewStore(artifactRoot),
				StatusWriter: sw,
			}
			return svc.Run(instanceCtx, runsvc.Request{
				Config:             cfg,
				ClearCache:         deps.clearCache,
				SkipCacheWriteBack: true, // avoid concurrent writes to shared cache dir
			})
		}
	}

	// RunScenario never returns a non-nil error per its contract; instance errors
	// are recorded in InstanceResult.Err instead.
	scenarioResult, _ := scenario.RunScenario(ctx, spec, run)

	instances := make([]map[string]any, len(scenarioResult.Instances))
	for i, inst := range scenarioResult.Instances {
		if inst.Err != nil {
			instances[i] = map[string]any{
				"role":  inst.Role,
				"error": inst.Err.Error(),
			}
			continue
		}
		r := inst.Response.Result
		if r == nil {
			instances[i] = map[string]any{
				"role":      inst.Role,
				"exit_code": inst.Response.ExitCode,
			}
			continue
		}
		m := map[string]any{
			"role":         inst.Role,
			"run_id":       inst.Response.RunID,
			"exit_code":    inst.Response.ExitCode,
			"total":        r.Total,
			"passed":       r.Passed,
			"failed":       r.Failed,
			"skipped":      r.Skipped,
			"tests":        r.Tests,
			"errors":       r.Errors,
			"new_failures": r.NewFailures,
		}
		if r.TimeoutType != "" {
			m["timeout_type"] = r.TimeoutType
		}
		if len(inst.Response.Warnings) > 0 {
			m["warnings"] = inst.Response.Warnings
		}
		instances[i] = m
	}

	output := map[string]any{
		"schema_version": "1",
		"exit_code":      scenarioResult.ExitCode,
		"instances":      instances,
	}
	if len(scenarioResult.OrchestratorErrors) > 0 {
		output["orchestrator_errors"] = scenarioResult.OrchestratorErrors
	}

	writeJSON(w, output)

	// Best-effort prune of old results and artifacts for each instance.
	// Uses configs loaded during the production run path when available; when a
	// test-injected runner is in use (configs == nil), configs are loaded on demand
	// (best-effort — load failures are silently skipped).
	{
		pruneConfigs := configs
		if pruneConfigs == nil {
			pruneConfigs = make(map[string]*config.Config, len(spec.Instances))
			for _, inst := range spec.Instances {
				cfgPath := spec.ConfigPath(inst)
				cfg, loadErr := config.Load(cfgPath)
				if loadErr != nil {
					continue
				}
				if valErr := cfg.Validate(false); valErr != nil {
					continue
				}
				pruneConfigs[inst.Role] = cfg
			}
		}
		seen := make(map[string]struct{})
		for _, inst := range spec.Instances {
			cfg := pruneConfigs[inst.Role]
			if cfg == nil || cfg.Retention.MaxRuns == nil || *cfg.Retention.MaxRuns <= 0 {
				continue
			}
			maxRuns := *cfg.Retention.MaxRuns
			artifactRoot := filepath.Join(cfg.ProjectPath, ".testplay", "runs")
			key := cfg.ResultDir + "|" + artifactRoot
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			_, _ = history.NewStore(cfg.ResultDir).Prune(maxRuns)
			_, _ = artifacts.NewStore(artifactRoot).Prune(maxRuns)
		}
	}

	return scenarioResult.ExitCode
}

// runFilter, runCategory, runCompareRun, resetShadow, forceShadow and clearCache are cobra flag values.
var runFilter, runCategory, runCompareRun string
var resetShadow bool
var forceShadow bool
var clearCache bool
var scenarioPath string

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute Unity tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, causeCancel := context.WithCancelCause(context.Background())
		defer causeCancel(nil)

		statusPath := "testplay-status.json"
		sigCh := setupSignals()
		go watchSignals(ctx, causeCancel, sigCh, func() {
			// Best-effort: write interrupted status so pollers see the phase change
			_ = status.NewWriter(statusPath).Write(status.Status{Phase: status.PhaseInterrupted})
		})

		var code int
		if scenarioPath != "" {
			code = runScenario(cmd.OutOrStdout(), scenarioPath, scenarioDeps{ctx: ctx, clearCache: clearCache})
		} else {
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
					ClearCache:  clearCache,
				},
			}
			code = runRun(cmd.OutOrStdout(), deps)
		}
		os.Exit(code)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringVar(&runFilter, "filter", "", "Test name filter")
	runCmd.Flags().StringVar(&runCategory, "category", "", "Test category filter")
	runCmd.Flags().StringVar(&runCompareRun, "compare-run", "", "Run ID to compare against for regression detection")
	runCmd.Flags().BoolVar(&resetShadow, "reset-shadow", false, "Force shadow workspace (equivalent to --shadow; kept for compatibility)")
	runCmd.Flags().BoolVar(&forceShadow, "shadow", false, "Force shadow workspace even when Unity Editor is not open")
	runCmd.Flags().BoolVar(&clearCache, "clear-cache", false, "Remove cached Library before shadow workspace creation")
	runCmd.Flags().StringVar(&scenarioPath, "scenario", "", "Path to scenario JSON file for multi-instance execution")
}
