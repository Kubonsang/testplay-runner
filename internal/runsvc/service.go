// internal/runsvc/service.go
package runsvc

import (
	"context"
	"fmt"
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
	if _, err := s.Artifacts.PrepareRunDir(runID); err != nil {
		return Response{}, fmt.Errorf("runsvc: prepare artifact dir: %w", err)
	}
	resultsFile := s.Artifacts.ResultsFilePath(runID)

	// Execute Unity.
	execOpts := unity.ExecuteOptions{
		ProjectPath:  req.Config.ProjectPath,
		ResultsFile:  resultsFile,
		StatusWriter: s.StatusWriter,
		TimeoutType:  "total",
		Filter:       req.Filter,
		Category:     req.Category,
		TestPlatform: req.Config.TestPlatform,
		CompileMs:    req.Config.Timeout.CompileMs,
		TestMs:       req.Config.Timeout.TestMs,
	}
	result, exitCode := unity.Execute(ctx, s.Runner, execOpts)
	result.RunID = runID
	result.SchemaVersion = "1"

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

	// Regression comparison.
	if req.CompareRun != "" {
		prev, loadErr := s.Store.Load(req.CompareRun)
		if loadErr == nil {
			result.NewFailures = history.Compare(prev, result)
		} else {
			result.NewFailures = make([]parser.TestCase, 0)
		}
	}

	var warnings []string

	// Persist to history store.
	if err := s.Store.Save(runID, result); err != nil {
		warnings = append(warnings, fmt.Sprintf("result not saved: %v", err))
	}

	// Write summary.json to artifact dir.
	summary := buildSummary(runID, result, exitCode)
	if err := s.Artifacts.SaveSummary(runID, summary); err != nil {
		warnings = append(warnings, fmt.Sprintf("summary not written: %v", err))
	}

	return Response{
		RunID:    runID,
		Result:   result,
		ExitCode: exitCode,
		Warnings: warnings,
	}, nil
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
