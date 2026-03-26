// internal/runsvc/service.go
package runsvc

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

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
	Config     *config.Config
	Filter     string
	Category   string
	CompareRun string
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

	runID := clock().Format("20060102-150405")

	// Prepare artifact directory and get results XML path.
	runDir, err := s.Artifacts.PrepareRunDir(runID)
	if err != nil {
		return Response{}, fmt.Errorf("runsvc: prepare artifact dir: %w", err)
	}
	resultsFile := s.Artifacts.ResultsFilePath(runID)

	// Build status writer: combine snapshot writer with per-run event log,
	// then stamp run_id into every write via runIDWriter.
	var sw status.WriterInterface = s.StatusWriter
	if sw != nil {
		eventsPath := filepath.Join(runDir, "events.ndjson")
		sw = status.NewManager(sw, status.NewEventLog(eventsPath))
		sw = &runIDWriter{inner: sw, runID: runID}
	}

	// Wrap runner to capture raw stdout/stderr for artifact storage.
	cap := &logCapture{inner: s.Runner}

	startedAt := clock()

	// Execute Unity.
	execOpts := unity.ExecuteOptions{
		ProjectPath:  req.Config.ProjectPath,
		ResultsFile:  resultsFile,
		StatusWriter: sw,
		TimeoutType:  "total",
		Filter:       req.Filter,
		Category:     req.Category,
		TestPlatform: req.Config.TestPlatform,
		CompileMs:    req.Config.Timeout.CompileMs,
		TestMs:       req.Config.Timeout.TestMs,
	}
	result, exitCode := unity.Execute(ctx, cap, execOpts)

	finishedAt := clock()

	result.RunID = runID
	result.SchemaVersion = "1"
	result.ExitCode = exitCode

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

	// Persist to history store.
	if err := s.Store.Save(runID, result); err != nil {
		warnings = append(warnings, fmt.Sprintf("result not saved: %v", err))
	}

	// Write summary.json to artifact dir.
	summary := buildSummary(runID, result, exitCode)
	if err := s.Artifacts.SaveSummary(runID, summary); err != nil {
		warnings = append(warnings, fmt.Sprintf("summary not written: %v", err))
	}

	// Write stdout.log and stderr.log.
	if err := s.Artifacts.SaveRawLogs(runID, cap.stdout, cap.stderr); err != nil {
		warnings = append(warnings, fmt.Sprintf("raw logs not written: %v", err))
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

// logCapture wraps a Runner to accumulate stdout and stderr across all Run calls.
// In two-phase execution both invocations are captured and concatenated.
// Not goroutine-safe: Run calls must be sequential, which is guaranteed by
// both executeSinglePhase and executeTwoPhase calling runner.Run sequentially.
type logCapture struct {
	inner  unity.Runner
	stdout []byte
	stderr []byte
}

func (l *logCapture) Run(ctx context.Context, args []string) ([]byte, []byte, int, error) {
	stdout, stderr, code, err := l.inner.Run(ctx, args)
	l.stdout = append(l.stdout, stdout...)
	l.stderr = append(l.stderr, stderr...)
	return stdout, stderr, code, err
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
