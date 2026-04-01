// internal/runsvc/service_test.go
package runsvc_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

// fakeRunner writes resultsXML to the -testResults path and returns exitCode.
type fakeRunner struct {
	resultsXML []byte
	stderr     []byte
	exitCode   int
	err        error
	lastArgs   []string
}

func (f *fakeRunner) Run(_ context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	f.lastArgs = args
	for i, a := range args {
		if a == "-testResults" && i+1 < len(args) && f.resultsXML != nil {
			_ = os.WriteFile(args[i+1], f.resultsXML, 0644)
		}
	}
	if stderr != nil && len(f.stderr) > 0 {
		_, _ = stderr.Write(f.stderr)
	}
	return f.exitCode, f.err
}

func mustReadFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture %s: %v", path, err)
	}
	return data
}

func baseConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     filepath.Join(dir, "results"),
		Timeout:       config.Timeouts{TotalMs: 300000},
		TestPlatform:  "edit_mode",
	}
	return cfg, dir
}

func TestService_AllPass_ExitCode0(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData}

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		StatusWriter: status.NewWriter(filepath.Join(dir, "status.json")),
		Clock:     func() time.Time { return time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC) },
	}

	resp, err := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("expected exit 0, got %d", resp.ExitCode)
	}
	if resp.RunID == "" {
		t.Error("RunID must be non-empty")
	}
}

func TestService_StatusSnapshot_ContainsRunID(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData}
	statusPath := filepath.Join(dir, "status.json")

	svc := &runsvc.Service{
		Runner:       fake,
		Store:        history.NewStore(cfg.ResultDir),
		Artifacts:    artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		StatusWriter: status.NewWriter(statusPath),
		Clock:        func() time.Time { return time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC) },
	}

	resp, err := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("status.json not written: %v", err)
	}
	var snap map[string]any
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("status.json invalid JSON: %v", err)
	}
	if snap["run_id"] != resp.RunID {
		t.Errorf("status run_id = %v, want %q", snap["run_id"], resp.RunID)
	}
}

func TestService_EventsNDJSON_WrittenToArtifactDir(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData}
	artifactRoot := filepath.Join(dir, ".fastplay", "runs")

	svc := &runsvc.Service{
		Runner:       fake,
		Store:        history.NewStore(cfg.ResultDir),
		Artifacts:    artifacts.NewStore(artifactRoot),
		StatusWriter: status.NewWriter(filepath.Join(dir, "status.json")),
		Clock:        func() time.Time { return time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC) },
	}

	resp, err := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	eventsPath := filepath.Join(artifactRoot, resp.RunID, "events.ndjson")
	data, readErr := os.ReadFile(eventsPath)
	if readErr != nil {
		t.Fatalf("events.ndjson not written: %v", readErr)
	}
	if len(data) == 0 {
		t.Fatal("events.ndjson is empty")
	}

	// Each line must be valid JSON with event and run_id fields.
	var parsedLines []map[string]any
	for i, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("line %d: invalid JSON: %v", i, err)
		}
		if m["event"] == nil {
			t.Errorf("line %d: missing event field", i)
		}
		if m["run_id"] != resp.RunID {
			t.Errorf("line %d: run_id = %v, want %q", i, m["run_id"], resp.RunID)
		}
		parsedLines = append(parsedLines, m)
	}

	// Last event must be run_finished.
	if len(parsedLines) == 0 {
		t.Fatal("no events parsed from events.ndjson")
	}
	if last := parsedLines[len(parsedLines)-1]["event"]; last != "run_finished" {
		t.Errorf("last event = %v, want run_finished", last)
	}
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

func TestService_TestFailure_ExitCode3(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/one_failure.xml")
	fake := &fakeRunner{resultsXML: xmlData}

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		Clock:     func() time.Time { return time.Now() },
	}

	resp, _ := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if resp.ExitCode != 3 {
		t.Errorf("expected exit 3, got %d", resp.ExitCode)
	}
}

func TestService_CompileFailure_ExitCode2(t *testing.T) {
	cfg, dir := baseConfig(t)
	// No resultsXML → simulate compile failure (no results.xml produced)
	fake := &fakeRunner{stderr: []byte("Assets/Foo.cs(1,1): error CS0246: Type 'Bar' not found")}

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		Clock:     func() time.Time { return time.Now() },
	}

	resp, _ := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if resp.ExitCode != 2 {
		t.Errorf("expected exit 2, got %d", resp.ExitCode)
	}
}

func TestService_CompileFailure_WithNonZeroExitCode_ExitCode2(t *testing.T) {
	cfg, dir := baseConfig(t)
	// Simulate Unity exiting non-zero with compile errors in stderr
	fake := &fakeRunner{
		stderr:   []byte("Assets/Foo.cs(1,1): error CS0246: Type 'Bar' not found"),
		exitCode: 1,
	}

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		Clock:     func() time.Time { return time.Now() },
	}

	resp, _ := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if resp.ExitCode != 2 {
		t.Errorf("expected exit 2, got %d", resp.ExitCode)
	}
}

func TestService_RunID_MatchesClock(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData}

	fixed := time.Date(2026, 3, 26, 14, 30, 55, 0, time.UTC)
	svc := &runsvc.Service{
		Runner:    fake,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		Clock:     func() time.Time { return fixed },
	}

	resp, _ := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if !strings.HasPrefix(resp.RunID, "20260326-143055-") {
		t.Errorf("expected run_id with prefix '20260326-143055-', got %q", resp.RunID)
	}
	parts := strings.Split(resp.RunID, "-")
	if len(parts) != 3 || len(parts[2]) != 8 {
		t.Errorf("unexpected run_id format %q: want 'YYYYMMDD-HHMMSS-xxxxxxxx'", resp.RunID)
	}
}

func TestService_SummaryJSON_WrittenToArtifactDir(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData}
	artifactRoot := filepath.Join(dir, ".fastplay", "runs")
	fixed := time.Date(2026, 3, 26, 14, 30, 55, 0, time.UTC)

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(artifactRoot),
		Clock:     func() time.Time { return fixed },
	}

	resp, _ := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	summaryPath := filepath.Join(artifactRoot, resp.RunID, "summary.json")
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("summary.json not written: %v", err)
	}
	var summary map[string]any
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("summary.json invalid JSON: %v", err)
	}
	if summary["run_id"] != resp.RunID {
		t.Errorf("summary run_id mismatch: got %v", summary["run_id"])
	}
}

func TestService_ManifestAndLogs_WrittenToArtifactDir(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData, stderr: []byte("unity stderr")}
	artifactRoot := filepath.Join(dir, ".fastplay", "runs")

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(artifactRoot),
		Clock:     func() time.Time { return time.Date(2026, 3, 26, 14, 30, 55, 0, time.UTC) },
	}

	resp, err := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	runDir := filepath.Join(artifactRoot, resp.RunID)

	// manifest.json must exist and contain required fields
	manifestPath := filepath.Join(runDir, "manifest.json")
	mData, mErr := os.ReadFile(manifestPath)
	if mErr != nil {
		t.Fatalf("manifest.json not written: %v", mErr)
	}
	var m map[string]any
	if err := json.Unmarshal(mData, &m); err != nil {
		t.Fatalf("manifest.json invalid JSON: %v", err)
	}
	for _, field := range []string{"schema_version", "run_id", "artifact_root", "results_xml", "stdout_log", "stderr_log", "started_at", "finished_at", "exit_code"} {
		if m[field] == nil {
			t.Errorf("manifest.json missing field %q", field)
		}
	}
	if m["run_id"] != resp.RunID {
		t.Errorf("manifest run_id = %v, want %q", m["run_id"], resp.RunID)
	}

	// stderr.log must contain the captured stderr
	stderrPath := filepath.Join(runDir, "stderr.log")
	got, readErr := os.ReadFile(stderrPath)
	if readErr != nil {
		t.Fatalf("stderr.log not written: %v", readErr)
	}
	if string(got) != "unity stderr" {
		t.Errorf("stderr.log = %q, want %q", got, "unity stderr")
	}

	// stdout.log must exist (may be empty)
	if _, statErr := os.Stat(filepath.Join(runDir, "stdout.log")); statErr != nil {
		t.Errorf("stdout.log not written: %v", statErr)
	}
}

func TestService_Filter_ForwardedToRunner(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData}

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		Clock:     func() time.Time { return time.Now() },
	}

	svc.Run(context.Background(), runsvc.Request{Config: cfg, Filter: "MyTest.Foo"})

	found := false
	for i, a := range fake.lastArgs {
		if a == "-testFilter" && i+1 < len(fake.lastArgs) && fake.lastArgs[i+1] == "MyTest.Foo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected -testFilter MyTest.Foo in runner args, got: %v", fake.lastArgs)
	}
}

func TestService_CompareRun_PopulatesNewFailures(t *testing.T) {
	cfg, dir := baseConfig(t)
	store := history.NewStore(cfg.ResultDir)

	// Seed a prev run where TestSub passed
	prevID := "20250301-090000"
	_ = store.Save(prevID, &history.RunResult{
		RunID: prevID, SchemaVersion: "1",
		Tests: []parser.TestCase{{Name: "MyTests.TestSub", Result: "Passed"}},
	})

	xmlData := mustReadFixture(t, "../../internal/parser/testdata/one_failure.xml")
	fake := &fakeRunner{resultsXML: xmlData}

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     store,
		Artifacts: artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		Clock:     func() time.Time { return time.Now() },
	}

	resp, _ := svc.Run(context.Background(), runsvc.Request{Config: cfg, CompareRun: prevID})
	if resp.Result.NewFailures == nil {
		t.Error("expected NewFailures to be populated when CompareRun set")
	}
}

func TestService_CompareRun_NewFailuresHaveRelativePath(t *testing.T) {
	cfg, dir := baseConfig(t)
	store := history.NewStore(cfg.ResultDir)

	// Seed a prev run where TestSub passed.
	prevID := "20250301-090000"
	_ = store.Save(prevID, &history.RunResult{
		RunID: prevID, SchemaVersion: "1",
		Tests: []parser.TestCase{{Name: "MyTests.TestSub", Result: "Passed"}},
	})

	xmlData := mustReadFixture(t, "../../internal/parser/testdata/one_failure.xml")
	fake := &fakeRunner{resultsXML: xmlData}

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     store,
		Artifacts: artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		Clock:     func() time.Time { return time.Now() },
	}

	resp, _ := svc.Run(context.Background(), runsvc.Request{Config: cfg, CompareRun: prevID})
	if resp.Result.NewFailures == nil {
		t.Fatal("expected NewFailures to be populated")
	}
	for _, nf := range resp.Result.NewFailures {
		if nf.AbsolutePath != "" && nf.File == "" {
			t.Errorf("NewFailures[%s]: AbsolutePath=%q but File is empty — relative path normalisation missing", nf.Name, nf.AbsolutePath)
		}
	}
}

func TestService_NoCompareRun_NewFailuresIsNil(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData}

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		Clock:     func() time.Time { return time.Now() },
	}

	resp, _ := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if resp.Result.NewFailures != nil {
		t.Error("expected NewFailures nil when no CompareRun")
	}
}

// runnerFunc is a function-based unity.Runner for tests that need custom logic.
type runnerFunc func(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error)

func (f runnerFunc) Run(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	return f(ctx, args, stdout, stderr)
}

func TestService_UsesShadowProjectPath_WhenLocked(t *testing.T) {
	// Build a minimal project directory with the lockfile present.
	projectDir := t.TempDir()
	for _, d := range []string{"Assets", "ProjectSettings", "Packages", "Temp"} {
		_ = os.MkdirAll(filepath.Join(projectDir, d), 0755)
	}
	_ = os.WriteFile(filepath.Join(projectDir, "Temp", "UnityLockfile"), []byte{}, 0644)

	// Add actual source files so copyDir has something to copy.
	_ = os.WriteFile(filepath.Join(projectDir, "Assets", "Player.cs"), []byte("// Player"), 0644)
	_ = os.WriteFile(filepath.Join(projectDir, "ProjectSettings", "ProjectVersion.txt"), []byte("m_EditorVersion: 6000.3.8f1"), 0644)

	var usedProjectPath string
	// filesExistDuringRun checks shadow file presence while the shadow dir is still live
	// (before ws.Cleanup() is deferred by service.Run on return).
	filesExistDuringRun := make(map[string]bool)
	runner := runnerFunc(func(_ context.Context, args []string, _, _ io.Writer) (int, error) {
		for i, a := range args {
			if a == "-projectPath" && i+1 < len(args) {
				usedProjectPath = args[i+1]
			}
		}
		// Check shadow files while the workspace is still live.
		for _, rel := range []string{
			filepath.Join("Assets", "Player.cs"),
			filepath.Join("ProjectSettings", "ProjectVersion.txt"),
		} {
			_, err := os.Stat(filepath.Join(usedProjectPath, rel))
			filesExistDuringRun[rel] = (err == nil)
		}
		return 0, nil
	})

	resultDir := filepath.Join(projectDir, ".fastplay", "results")
	svc := &runsvc.Service{
		Runner:    runner,
		Store:     history.NewStore(resultDir),
		Artifacts: artifacts.NewStore(filepath.Join(projectDir, ".fastplay", "runs")),
	}

	cfg := &config.Config{
		UnityPath:    "/fake/unity",
		ProjectPath:  projectDir,
		TestPlatform: "edit_mode",
		Timeout:      config.Timeouts{TotalMs: 5000},
	}
	_, _ = svc.Run(context.Background(), runsvc.Request{Config: cfg})

	shadowPrefix := filepath.Join(projectDir, ".fastplay-shadow-")
	if !strings.HasPrefix(usedProjectPath, shadowPrefix) {
		t.Errorf("expected shadow projectPath to have prefix %q, got %q", shadowPrefix, usedProjectPath)
	}

	// Verify that copyDir actually copied the source files into the shadow during the run.
	for _, rel := range []string{
		filepath.Join("Assets", "Player.cs"),
		filepath.Join("ProjectSettings", "ProjectVersion.txt"),
	} {
		if !filesExistDuringRun[rel] {
			t.Errorf("expected shadow copy of %q to exist during run", rel)
		}
	}
}

func TestService_ResetShadow_RebuildsShadow(t *testing.T) {
	projectDir := t.TempDir()
	for _, d := range []string{"Assets", "ProjectSettings", "Packages"} {
		_ = os.MkdirAll(filepath.Join(projectDir, d), 0755)
	}

	var usedProjectPath string
	runner := runnerFunc(func(_ context.Context, args []string, _, _ io.Writer) (int, error) {
		for i, a := range args {
			if a == "-projectPath" && i+1 < len(args) {
				usedProjectPath = args[i+1]
			}
		}
		return 0, nil
	})

	resultDir := filepath.Join(projectDir, ".fastplay", "results")
	svc := &runsvc.Service{
		Runner:    runner,
		Store:     history.NewStore(resultDir),
		Artifacts: artifacts.NewStore(filepath.Join(projectDir, ".fastplay", "runs")),
	}

	cfg := &config.Config{
		UnityPath:    "/fake/unity",
		ProjectPath:  projectDir,
		TestPlatform: "edit_mode",
		Timeout:      config.Timeouts{TotalMs: 5000},
	}

	_, _ = svc.Run(context.Background(), runsvc.Request{Config: cfg, ResetShadow: true})

	shadowPrefix := filepath.Join(projectDir, ".fastplay-shadow-")
	if !strings.HasPrefix(usedProjectPath, shadowPrefix) {
		t.Errorf("expected shadow projectPath to have prefix %q, got %q", shadowPrefix, usedProjectPath)
	}
}

func TestService_UsesSourceProjectPath_WhenNotLocked(t *testing.T) {
	projectDir := t.TempDir()

	var usedProjectPath string
	runner := runnerFunc(func(_ context.Context, args []string, _, _ io.Writer) (int, error) {
		for i, a := range args {
			if a == "-projectPath" && i+1 < len(args) {
				usedProjectPath = args[i+1]
			}
		}
		return 0, nil
	})

	resultDir := filepath.Join(projectDir, ".fastplay", "results")
	svc := &runsvc.Service{
		Runner:    runner,
		Store:     history.NewStore(resultDir),
		Artifacts: artifacts.NewStore(filepath.Join(projectDir, ".fastplay", "runs")),
	}

	cfg := &config.Config{
		UnityPath:    "/fake/unity",
		ProjectPath:  projectDir,
		TestPlatform: "edit_mode",
		Timeout:      config.Timeouts{TotalMs: 5000},
	}
	_, _ = svc.Run(context.Background(), runsvc.Request{Config: cfg})

	if usedProjectPath != projectDir {
		t.Errorf("expected original projectPath %q, got %q", projectDir, usedProjectPath)
	}
}

func TestService_ShadowPrepareFailure_ReturnsError(t *testing.T) {
	// Trigger shadow mode via UnityLockfile, then cause shadow.Prepare to fail
	// by making Assets/ unreadable so copyDir returns an error.
	projectDir := t.TempDir()
	for _, d := range []string{"Assets", "ProjectSettings", "Packages", "Temp"} {
		_ = os.MkdirAll(filepath.Join(projectDir, d), 0755)
	}
	_ = os.WriteFile(filepath.Join(projectDir, "Temp", "UnityLockfile"), []byte{}, 0644)
	// Make Assets/ unreadable so copyDir fails inside Prepare.
	assetsDir := filepath.Join(projectDir, "Assets")
	if err := os.Chmod(assetsDir, 0000); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { os.Chmod(assetsDir, 0755) })

	svc := &runsvc.Service{
		Runner:    runnerFunc(func(_ context.Context, _ []string, _, _ io.Writer) (int, error) { return 0, nil }),
		Store:     history.NewStore(filepath.Join(projectDir, ".fastplay", "results")),
		Artifacts: artifacts.NewStore(filepath.Join(projectDir, ".fastplay", "runs")),
	}

	cfg := &config.Config{
		UnityPath:    "/fake/unity",
		ProjectPath:  projectDir,
		TestPlatform: "edit_mode",
		Timeout:      config.Timeouts{TotalMs: 5000},
	}
	_, err := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if err == nil {
		t.Error("expected infrastructure error when shadow workspace cannot be created")
	}
}

func TestService_SaveFailure_ReturnsWarning(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData}

	// Point result dir at a read-only path to force save error
	cfg.ResultDir = "/dev/null/impossible"

	svc := &runsvc.Service{
		Runner:    fake,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(filepath.Join(dir, ".fastplay", "runs")),
		Clock:     func() time.Time { return time.Now() },
	}

	resp, _ := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if resp.ExitCode != 0 {
		t.Errorf("save failure must not change exit code, got %d", resp.ExitCode)
	}
	if len(resp.Warnings) == 0 {
		t.Error("expected at least one warning when save fails")
	}
}
