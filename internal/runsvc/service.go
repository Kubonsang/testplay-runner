// internal/runsvc/service.go
package runsvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

const heartbeatInterval = 5 * time.Second

// ResultStore is the subset of history.Store used by Service.
// Defining it here (consumer side) follows standard Go interface practice
// and allows test injection without depending on the concrete type.
type ResultStore interface {
	Save(string, *history.RunResult) error
	Load(string) (*history.RunResult, error)
}

// Service orchestrates a single fastplay run.
// It is intentionally decoupled from cobra/CLI concerns; all inputs come
// through Request and all outputs through Response.
type Service struct {
	Runner       unity.Runner
	Store        ResultStore            // *history.Store satisfies this
	Artifacts    *artifacts.Store
	StatusWriter status.WriterInterface // may be nil
	Clock        func() time.Time       // defaults to time.Now if nil
}

// Request carries all inputs for a single fastplay run.
type Request struct {
	Config      *config.Config
	Filter      string
	Category    string
	CompareRun  string
	ResetShadow bool // when true, delete and rebuild .fastplay-shadow/ before running
	ForceShadow bool // activate shadow workspace without resetting Library cache
}

// Response carries all outputs of a single fastplay run.
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
	clock := s.Clock
	if clock == nil {
		clock = time.Now
	}

	runID := generateRunID(clock())

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

	// Determine execution backend: shadow if editor has the project open.
	var ws *shadow.Workspace
	if req.ForceShadow || req.ResetShadow || shadow.IsLocked(req.Config.ProjectPath) {
		var wsErr error
		if req.ResetShadow {
			ws, wsErr = shadow.Reset(ctx, req.Config.ProjectPath)
		} else {
			ws, wsErr = shadow.Prepare(ctx, req.Config.ProjectPath)
		}
		if wsErr != nil {
			return Response{}, fmt.Errorf("runsvc: prepare shadow workspace: %w", wsErr)
		}
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
	result, exitCode := unity.Execute(ctx, s.Runner, execOpts)

	finishedAt := clock()

	result.RunID = runID
	result.SchemaVersion = "1"
	result.ExitCode = exitCode

	var warnings []string

	// Regression comparison.
	if req.CompareRun != "" {
		prev, loadErr := s.Store.Load(req.CompareRun)
		if loadErr == nil {
			result.NewFailures = history.Compare(prev, result)
			if result.NewFailures == nil {
				result.NewFailures = make([]parser.TestCase, 0)
			}
		} else {
			result.NewFailures = make([]parser.TestCase, 0)
			warnings = append(warnings, fmt.Sprintf("compare-run %q not found: %v", req.CompareRun, loadErr))
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

	// Persist to history store.
	if err := s.Store.Save(runID, result); err != nil {
		warnings = append(warnings, fmt.Sprintf("result not saved: %v", err))
	}

	// Write summary.json to artifact dir.
	summary := buildSummary(runID, result, exitCode)
	if err := s.Artifacts.SaveSummary(runID, summary); err != nil {
		warnings = append(warnings, fmt.Sprintf("summary not written: %v", err))
	}

	// Write manifest.json.
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
		"total":          result.Total,
		"passed":         result.Passed,
		"failed":         result.Failed,
		"skipped":        result.Skipped,
	}
	if result.TimeoutType != "" {
		s["timeout_type"] = result.TimeoutType
	}
	return s
}
