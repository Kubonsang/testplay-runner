// internal/runsvc/service.go
package runsvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
	"github.com/Kubonsang/testplay-runner/internal/bridge"
	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/listcache"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/runid"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

const heartbeatInterval = 5 * time.Second

// Backend identifiers recorded in RunResult.Backend / the CLI output.
const (
	backendProcess = "process"
	backendShadow  = "shadow"
	backendBridge  = "bridge"
)

// defaultBridgeIdleDeadlineMs bounds how long the warm bridge may wait for the
// editor to become idle (compile/import to settle) before answering busy.
const defaultBridgeIdleDeadlineMs = 30000

// twoPhaseRequested reports whether the config asks for two-phase execution.
// The warm bridge cannot honestly reproduce the strict compile/test phase
// split, so two-phase always runs cold.
func twoPhaseRequested(c *config.Config) bool {
	return c.Timeout.CompileMs > 0 && c.Timeout.TestMs > 0
}

// bridgeIdleDeadline is the idle-wait budget for the bridge, never exceeding
// the overall total_ms run budget.
func bridgeIdleDeadline(c *config.Config) int64 {
	idle := int64(defaultBridgeIdleDeadlineMs)
	if c.Timeout.TotalMs > 0 && c.Timeout.TotalMs < idle {
		idle = c.Timeout.TotalMs
	}
	return idle
}

// describeSelection renders the explicit test selection flags for warnings.
func describeSelection(filter, category string) string {
	switch {
	case filter != "" && category != "":
		return fmt.Sprintf("--filter %q --category %q", filter, category)
	case filter != "":
		return fmt.Sprintf("--filter %q", filter)
	default:
		return fmt.Sprintf("--category %q", category)
	}
}

// prefixWarnings tags each message with a source prefix (e.g. "bridge: ...").
func prefixWarnings(prefix string, msgs []string) []string {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, prefix+": "+m)
	}
	return out
}

// ResultStore is the subset of history.Store used by Service.
// Defining it here (consumer side) follows standard Go interface practice
// and allows test injection without depending on the concrete type.
type ResultStore interface {
	Save(string, *history.RunResult) error
	Load(string) (*history.RunResult, error)
}

// Service orchestrates a single testplay run.
// It is intentionally decoupled from cobra/CLI concerns; all inputs come
// through Request and all outputs through Response.
type Service struct {
	Runner       unity.Runner
	Store        ResultStore // *history.Store satisfies this
	Artifacts    *artifacts.Store
	StatusWriter status.WriterInterface // may be nil
	Clock        func() time.Time       // defaults to time.Now if nil
}

// Request carries all inputs for a single testplay run.
type Request struct {
	Config             *config.Config
	Filter             string
	Category           string
	CompareRun         string
	ResetShadow        bool // activates shadow workspace; equivalent to ForceShadow with per-run isolation
	ForceShadow        bool // activate shadow workspace without resetting Library cache
	ClearCache         bool // remove cached Library before preparing shadow workspace
	SkipCacheWriteBack bool // skip Library cache write-back (used in scenario mode to avoid concurrent writes)
	ForceBridge        bool // --bridge: prefer the warm bridge (still subject to the Pristine Gate)
	DisableBridge      bool // --no-bridge: never select the warm bridge for this run
	WorkspaceBackend   string
	KeepWorkspace      bool
	WorkspaceStoreRoot string
}

// Response carries all outputs of a single testplay run.
type Response struct {
	RunID    string
	Result   *history.RunResult
	ExitCode int
	Warnings []string
}

// Run executes a Unity test run and returns its Response.
// It never returns a non-nil error for Unity-side failures (those are
// encoded as ExitCode); it returns an error only for unrecoverable
// infrastructure failures (e.g. cannot create artifact directory).
func (s *Service) Run(ctx context.Context, req Request) (Response, error) {
	if !ValidWorkspaceBackend(req.WorkspaceBackend) {
		return Response{}, fmt.Errorf("runsvc: invalid workspace backend %q", req.WorkspaceBackend)
	}
	if req.WorkspaceBackend != "" && req.ForceBridge {
		return Response{}, fmt.Errorf("runsvc: --bridge cannot be combined with --workspace-backend")
	}
	if _, err := resolveWorkspaceStoreRoot(req.Config.ProjectPath, req.WorkspaceStoreRoot); err != nil {
		return Response{}, fmt.Errorf("runsvc: workspace store root: %w", err)
	}

	clock := s.Clock
	if clock == nil {
		clock = time.Now
	}

	runID := runid.Generate(clock())

	// Prepare artifact directory and get results XML path.
	runDir, err := s.Artifacts.PrepareRunDir(runID)
	if err != nil {
		return Response{}, fmt.Errorf("runsvc: prepare artifact dir: %w", err)
	}
	resultsFile := s.Artifacts.ResultsFilePath(runID)

	// Build status writer: combine snapshot writer with per-run event log,
	// then stamp run_id into every write via runIDWriter.
	var sw status.WriterInterface = s.StatusWriter
	var mgr *status.Manager // kept for heartbeat access
	if sw != nil {
		eventsPath := filepath.Join(runDir, "events.ndjson")
		mgr = status.NewManager(sw, status.NewEventLog(eventsPath))
		sw = mgr
		sw = &runIDWriter{inner: sw, runID: runID}
	}

	// Open log files for streaming writes during execution.
	stdoutLog, stderrLog, err := s.Artifacts.OpenRunLogs(runID)
	if err != nil {
		return Response{}, fmt.Errorf("runsvc: open run logs: %w", err)
	}
	defer stdoutLog.Close()
	defer stderrLog.Close()

	startedAt := clock()

	// Write initial status snapshot with run-scoped metadata so that
	// heartbeats and phase updates can inherit StartedAt/PID/ArtifactRoot.
	if sw != nil {
		_ = sw.Write(status.Status{
			Phase:        status.PhaseCompiling,
			StartedAt:    startedAt.UTC().Format(time.RFC3339),
			PID:          os.Getpid(),
			ArtifactRoot: runDir,
		})
	}

	// Heartbeat goroutine: ticks every heartbeatInterval while Unity runs,
	// updating last_heartbeat_at so external pollers can detect stale runs.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	if mgr != nil {
		go func() {
			ticker := time.NewTicker(heartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-hbCtx.Done():
					return
				case <-ticker.C:
					_ = mgr.Heartbeat()
				}
			}
		}()
	}

	// Clear cache if requested.
	if req.ClearCache && req.WorkspaceBackend != WorkspaceBackendImage {
		_ = shadow.ClearCacheAt(legacyCacheRoot(req))
	}

	var warnings []string
	var hasSystemError bool

	var result *history.RunResult
	var exitCode int

	// ── Tier-1: warm-editor bridge ─────────────────────────────────────────
	// Selected only when the caller has not forced the cold path, the bridge is
	// enabled, two-phase is not requested (a warm editor cannot honestly
	// reproduce the strict compile/test phase split), and a live, compatible
	// bridge is present (Probe). On ANY decline the run falls through to the
	// unchanged shadow/process cold path below — correctness wins by default.
	ranBridge := false
	bridgeFallbackReason := ""
	bridgeEligible := req.WorkspaceBackend == "" &&
		!req.ForceShadow && !req.ResetShadow && !req.ClearCache &&
		!req.DisableBridge && !twoPhaseRequested(req.Config) &&
		(req.Config.BridgeEnabled() || req.ForceBridge)
	if bridgeEligible {
		expectedVer := bridge.ExpectedUnityVersion(req.Config.UnityPath, req.Config.ProjectPath)
		handshake, probeOK, reason := bridge.Probe(req.Config.ProjectPath, expectedVer, clock(), 0)
		// --bridge enables the preferred tier even when config disables it, but
		// never bypasses Probe. Protocol/session/project identity failures must
		// fall back before a request is published.
		if probeOK {
			bridgeOpts := unity.ExecuteOptions{
				ProjectPath:  req.Config.ProjectPath,
				ResultsFile:  resultsFile,
				StatusWriter: sw,
				TimeoutType:  "total",
				Filter:       req.Filter,
				Category:     req.Category,
				TestPlatform: req.Config.TestPlatform,
			}
			br := unity.ExecuteBridge(
				ctx,
				bridge.NewClient(req.Config.ProjectPath, handshake.BridgeSessionID),
				bridgeOpts,
				runID,
				bridgeIdleDeadline(req.Config),
			)
			if br.FellBack {
				bridgeFallbackReason = "bridge declined the run (busy or could not guarantee a pristine domain)"
			} else {
				result = br.Result
				exitCode = br.ExitCode
				result.Backend = backendBridge
				warnings = append(warnings, prefixWarnings("bridge", br.Warnings)...)
				ranBridge = true
			}
		} else {
			bridgeFallbackReason = reason
		}
	}

	// ── Tier-2/3: cold path — shadow if the editor holds the project open,
	// else a fresh batchmode process against the real project. Unchanged. ───
	var ws *shadow.Workspace
	var workspaceMetrics *history.WorkspaceMetrics
	workspaceCleanupPending := false
	if !ranBridge {
		useShadow := req.WorkspaceBackend != "" ||
			req.ForceShadow || req.ResetShadow || shadow.IsLocked(req.Config.ProjectPath)
		if useShadow {
			var wsErr error
			if req.WorkspaceBackend == WorkspaceBackendImage {
				ws, workspaceMetrics, wsErr = s.prepareImageWorkspace(
					ctx, req, runID, stdoutLog, stderrLog,
				)
			} else {
				ws, workspaceMetrics, wsErr = s.prepareLegacyWorkspace(ctx, req, runID)
			}
			if wsErr != nil {
				if errors.Is(wsErr, os.ErrPermission) {
					return Response{ExitCode: 7}, fmt.Errorf("runsvc: prepare shadow workspace: %w", wsErr)
				}
				return Response{}, fmt.Errorf("runsvc: prepare shadow workspace: %w", wsErr)
			}
			workspaceCleanupPending = true
			defer func() {
				if workspaceCleanupPending && !req.KeepWorkspace {
					_ = ws.Cleanup()
				}
			}()
		}

		execProjectPath := req.Config.ProjectPath
		var extraArgs []string
		if ws != nil {
			execProjectPath = ws.ShadowPath
			extraArgs = []string{"-disable-assembly-updater"}
		}

		// Execute Unity.
		execOpts := unity.ExecuteOptions{
			ProjectPath:  execProjectPath,
			ResultsFile:  resultsFile,
			StatusWriter: sw,
			TimeoutType:  "total",
			Filter:       req.Filter,
			Category:     req.Category,
			TestPlatform: req.Config.TestPlatform,
			CompileMs:    req.Config.Timeout.CompileMs,
			TestMs:       req.Config.Timeout.TestMs,
			StdoutWriter: stdoutLog,
			StderrWriter: stderrLog,
			ExtraArgs:    extraArgs,
		}
		unityStarted := time.Now()
		result, exitCode = unity.Execute(ctx, s.Runner, execOpts)
		if workspaceMetrics != nil {
			workspaceMetrics.UnityExecutionMs = time.Since(unityStarted).Milliseconds()
			workspaceMetrics.TestExecutionMs = testExecutionMilliseconds(result)
			workspaceMetrics.UnityStartupMs = workspaceMetrics.UnityExecutionMs - workspaceMetrics.TestExecutionMs
			if workspaceMetrics.UnityStartupMs < 0 {
				workspaceMetrics.UnityStartupMs = 0
			}
		}
		if ws != nil {
			result.Backend = backendShadow
		} else {
			result.Backend = backendProcess
		}
	}

	// Disclose when an explicit --bridge could not run and we honored
	// correctness by falling back (force changes preference, never the bar).
	if req.ForceBridge && !ranBridge && bridgeFallbackReason != "" {
		warnings = append(warnings, fmt.Sprintf("bridge requested but unavailable: %s; ran %s backend instead", bridgeFallbackReason, result.Backend))
	}

	// Zero-test disclosure: a run that executed nothing must never look like
	// a quiet success to an automated caller.
	if exitCode == unity.ExitNoTestsMatched {
		warnings = append(warnings, fmt.Sprintf("no tests matched %s; nothing was executed — refresh candidate names with 'testplay list'", describeSelection(req.Filter, req.Category)))
	} else if exitCode == 0 && result.Total == 0 {
		warnings = append(warnings, "test run executed 0 tests; exit 0 verified nothing")
	}

	finishedAt := clock()

	result.RunID = runID
	result.SchemaVersion = "1"
	result.ExitCode = exitCode

	// Regression comparison.
	if req.CompareRun != "" {
		prev, loadErr := s.Store.Load(req.CompareRun)
		if loadErr == nil {
			result.NewFailures = history.Compare(prev, result)
			if result.NewFailures == nil {
				result.NewFailures = make([]parser.TestCase, 0)
			}
		} else {
			// A comparison that never happened must read as "no comparison"
			// (null), not "no regressions" (empty array) — Rule #6.
			result.NewFailures = nil
			warnings = append(warnings, fmt.Sprintf("compare-run %q not found: %v — new_failures is null (no comparison performed)", req.CompareRun, loadErr))
		}
	}

	// Remap shadow workspace paths to source project paths.
	// Must run after NewFailures is populated so shadow paths in NewFailures are also remapped.
	if ws != nil {
		ws.RemapPaths(result)
	}

	// Normalise paths: make file fields relative to project.
	for i := range result.Tests {
		if result.Tests[i].AbsolutePath != "" {
			result.Tests[i].File = parser.MakeRelative(req.Config.ProjectPath, result.Tests[i].AbsolutePath)
		}
	}
	for i := range result.Errors {
		if result.Errors[i].AbsolutePath != "" {
			result.Errors[i].File = parser.MakeRelative(req.Config.ProjectPath, result.Errors[i].AbsolutePath)
		}
	}
	for i := range result.NewFailures {
		if result.NewFailures[i].AbsolutePath != "" {
			result.NewFailures[i].File = parser.MakeRelative(req.Config.ProjectPath, result.NewFailures[i].AbsolutePath)
		}
	}

	// Write Library cache back on successful runs (exit 0 or 3).
	// Skipped in scenario mode to avoid concurrent writes to the shared cache dir.
	// The image backend never writes a test-mutated Library back into its base.
	if ws != nil &&
		workspaceMetrics.WorkspaceBackend == WorkspaceBackendLegacy &&
		!req.SkipCacheWriteBack && (exitCode == 0 || exitCode == 3) {
		if cacheErr := ws.UpdateLibraryCache(ctx); cacheErr != nil {
			warnings = append(warnings, fmt.Sprintf("library cache not updated: %v", cacheErr))
		}
		workspaceMetrics.CacheWriteBackMs = ws.Metrics.LegacyCacheWriteBack.Milliseconds()
	}

	if ws != nil && workspaceMetrics != nil {
		workspaceUsage, sizeErr := shadow.MeasureDirectoryUsage(ws.ShadowPath)
		if sizeErr == nil {
			workspaceMetrics.WorkspaceLogicalBytes = workspaceUsage.LogicalBytes
			workspaceMetrics.WorkspacePhysicalBytes = workspaceUsage.AllocatedBytes
			workspaceMetrics.PhysicalBytesAdded += workspaceUsage.AllocatedBytes
		}

		var persistentPhysicalBytes int64
		if workspaceMetrics.WorkspaceBackend == WorkspaceBackendImage {
			persistentPhysicalBytes = workspaceMetrics.ImageStorePhysicalBytes
		} else {
			cacheUsage, cacheErr := shadow.MeasureDirectoryUsage(
				legacyCacheRoot(req),
			)
			if cacheErr == nil {
				persistentPhysicalBytes = cacheUsage.AllocatedBytes
			}
			updateObservedPeak(
				workspaceMetrics,
				workspaceMetrics.WorkspacePhysicalBytes+
					ws.Metrics.LegacyCacheWritePeakPhysicalBytes,
			)
		}
		updateObservedPeak(
			workspaceMetrics,
			persistentPhysicalBytes+workspaceMetrics.WorkspacePhysicalBytes,
		)

		if req.KeepWorkspace {
			workspaceMetrics.WorkspaceKept = true
			workspaceMetrics.RetainedPhysicalBytes =
				persistentPhysicalBytes + workspaceMetrics.WorkspacePhysicalBytes
		} else {
			cleanupStarted := time.Now()
			if cleanupErr := ws.Cleanup(); cleanupErr != nil {
				warnings = append(warnings, fmt.Sprintf("shadow workspace cleanup failed: %v", cleanupErr))
				workspaceMetrics.RetainedPhysicalBytes =
					persistentPhysicalBytes + workspaceMetrics.WorkspacePhysicalBytes
			} else {
				workspaceCleanupPending = false
				workspaceMetrics.CleanupReclaimedPhysicalBytes =
					workspaceMetrics.WorkspacePhysicalBytes
				workspaceMetrics.RetainedPhysicalBytes = persistentPhysicalBytes
			}
			workspaceMetrics.CleanupMs = time.Since(cleanupStarted).Milliseconds()
		}
		result.WorkspaceMetrics = workspaceMetrics
	}

	// Persist after workspace lifecycle metrics are final.
	if err := s.Store.Save(runID, result); err != nil {
		warnings = append(warnings, fmt.Sprintf("result not saved: %v", err))
		hasSystemError = true
	}

	// Write list cache — persists the full test inventory so subsequent
	// `testplay list` calls can return complete: true.
	// Non-fatal: a failure here is a quality-of-life degradation, not a system error.
	// Skipped in scenario mode (concurrent writes, mixed test sets per instance)
	// and for filtered runs — a --filter/--category subset is not the full
	// inventory and would poison the cache's complete: true claim.
	if !req.SkipCacheWriteBack && req.Filter == "" && req.Category == "" && (exitCode == 0 || exitCode == 3) {
		_ = listcache.Write(req.Config.ProjectPath, runID, result.Tests, listcache.Metadata{
			FullInventory: true,
			TestPlatform:  req.Config.TestPlatform,
		})
	}

	// Override exit code for runner infrastructure failures.
	// This distinguishes "tests passed but results lost" (exit 9)
	// from "tests actually passed" (exit 0).
	if hasSystemError {
		exitCode = 9
	}

	// Write summary.json and manifest.json to artifact dir.
	// Placed after the exit code override so these files capture the final
	// exit code (including exit 9 when infrastructure fails).
	summary := buildSummary(runID, result, exitCode)
	if err := s.Artifacts.SaveSummary(runID, summary); err != nil {
		warnings = append(warnings, fmt.Sprintf("summary not written: %v", err))
		exitCode = 9
	}

	manifest := artifacts.Manifest{
		SchemaVersion: "1",
		RunID:         runID,
		ArtifactRoot:  s.Artifacts.RunDir(runID),
		ResultsXML:    s.Artifacts.ResultsFilePath(runID),
		StdoutLog:     s.Artifacts.StdoutFilePath(runID),
		StderrLog:     s.Artifacts.StderrFilePath(runID),
		StartedAt:     startedAt.UTC().Format(time.RFC3339),
		FinishedAt:    finishedAt.UTC().Format(time.RFC3339),
		ExitCode:      exitCode,
	}
	if err := s.Artifacts.SaveManifest(runID, manifest); err != nil {
		warnings = append(warnings, fmt.Sprintf("manifest not written: %v", err))
		exitCode = 9
	}

	return Response{
		RunID:    runID,
		Result:   result,
		ExitCode: exitCode,
		Warnings: warnings,
	}, nil
}

// runIDWriter wraps a WriterInterface to stamp RunID into every status write.
type runIDWriter struct {
	inner status.WriterInterface
	runID string
}

func (r *runIDWriter) Write(s status.Status) error {
	s.RunID = r.runID
	return r.inner.Write(s)
}

// buildSummary constructs the map written to summary.json.
func buildSummary(runID string, result *history.RunResult, exitCode int) map[string]any {
	s := map[string]any{
		"schema_version": "1",
		"run_id":         runID,
		"exit_code":      exitCode,
		"backend":        result.Backend,
		"total":          result.Total,
		"passed":         result.Passed,
		"failed":         result.Failed,
		"skipped":        result.Skipped,
	}
	if result.TimeoutType != "" {
		s["timeout_type"] = result.TimeoutType
	}
	if result.WorkspaceMetrics != nil {
		s["workspace_metrics"] = result.WorkspaceMetrics
	}
	return s
}
