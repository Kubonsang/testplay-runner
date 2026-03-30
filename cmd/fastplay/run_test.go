package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
)

func mustReadXMLFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}
	return data
}

func TestRunCmd_InvalidConfig_Exit5(t *testing.T) {
	dir := t.TempDir()
	// compile_ms without test_ms → Validate returns ErrConfigInvalid
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		Timeout:       config.Timeouts{CompileMs: 1000},
	}
	var buf bytes.Buffer
	code := runRun(&buf, runDeps{
		loadConfig: func(string) (*config.Config, error) { return cfg, nil },
		opts:       RunCmdOptions{},
	})
	if code != 5 {
		t.Errorf("expected exit 5, got %d", code)
	}
}

func TestRunCmd_AllPass_Exit0(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeCmdRunner{resultsXML: xmlData, exitCode: 0}

	store := history.NewStore(filepath.Join(dir, "results"))
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     filepath.Join(dir, "results"),
		Timeout:       config.Timeouts{CompileMs: 120000, TestMs: 30000, TotalMs: 300000},
	}

	var buf bytes.Buffer
	code := runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(dir, "status.json"),
		resultStore: store,
		opts:        RunCmdOptions{},
	})
	if code != 0 {
		t.Errorf("expected exit 0, got %d\noutput: %s", code, buf.String())
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if out["run_id"] == nil || out["run_id"] == "" {
		t.Error("run_id must be present and non-empty")
	}
	skipped, ok := out["skipped"]
	if !ok {
		t.Error("skipped field must be present in run output")
	} else if skipped != float64(0) {
		t.Errorf("expected skipped=0, got %v", skipped)
	}
}

func TestRunCmd_TestFailure_Exit3(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/one_failure.xml")
	fake := &fakeCmdRunner{resultsXML: xmlData, exitCode: 0}

	store := history.NewStore(filepath.Join(dir, "results"))
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     filepath.Join(dir, "results"),
		Timeout:       config.Timeouts{CompileMs: 120000, TestMs: 30000, TotalMs: 300000},
	}

	var buf bytes.Buffer
	code := runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(dir, "status.json"),
		resultStore: store,
		opts:        RunCmdOptions{},
	})
	if code != 3 {
		t.Errorf("expected exit 3, got %d\noutput: %s", code, buf.String())
	}

	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if out["tests"] == nil {
		t.Error("tests array must be present")
	}
}

func TestRunCmd_NoCompareRun_NewFailuresIsNull(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeCmdRunner{resultsXML: xmlData, exitCode: 0}

	store := history.NewStore(filepath.Join(dir, "results"))
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     filepath.Join(dir, "results"),
		Timeout:       config.Timeouts{CompileMs: 120000, TestMs: 30000, TotalMs: 300000},
	}

	var buf bytes.Buffer
	runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(dir, "status.json"),
		resultStore: store,
		opts:        RunCmdOptions{}, // no CompareRun
	})

	// Verify new_failures is exactly null
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	nf, ok := raw["new_failures"]
	if !ok {
		t.Error("new_failures field must be present in output")
		return
	}
	if string(nf) != "null" {
		t.Errorf("new_failures must be null when --compare-run not specified, got %s", nf)
	}
}

func TestRunCmd_SchemaVersionPresent(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeCmdRunner{resultsXML: xmlData, exitCode: 0}
	store := history.NewStore(filepath.Join(dir, "results"))
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     filepath.Join(dir, "results"),
		Timeout:       config.Timeouts{CompileMs: 120000, TestMs: 30000, TotalMs: 300000},
	}
	var buf bytes.Buffer
	runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(dir, "status.json"),
		resultStore: store,
		opts:        RunCmdOptions{},
	})
	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if out["schema_version"] == nil {
		t.Error("schema_version must be present in run output")
	}
}

type capturingRunner struct {
	resultsXML []byte
	lastArgs   []string
}

func (c *capturingRunner) Run(_ context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	c.lastArgs = args
	for i, a := range args {
		if a == "-testResults" && i+1 < len(args) && c.resultsXML != nil {
			_ = os.WriteFile(args[i+1], c.resultsXML, 0644)
		}
	}
	return 0, nil
}

func TestRunCmd_FilterForwarded(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	cap := &capturingRunner{resultsXML: xmlData}
	store := history.NewStore(filepath.Join(dir, "results"))
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     filepath.Join(dir, "results"),
		Timeout:       config.Timeouts{CompileMs: 120000, TestMs: 30000, TotalMs: 300000},
	}
	var buf bytes.Buffer
	runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      cap,
		statusPath:  filepath.Join(dir, "status.json"),
		resultStore: store,
		opts:        RunCmdOptions{Filter: "MyTest.Foo"},
	})
	found := false
	for i, a := range cap.lastArgs {
		if a == "-testFilter" && i+1 < len(cap.lastArgs) && cap.lastArgs[i+1] == "MyTest.Foo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected -testFilter MyTest.Foo in args, got: %v", cap.lastArgs)
	}
}

func TestRunCmd_SaveFailure_IncludesWarning(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeCmdRunner{resultsXML: xmlData, exitCode: 0}

	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     filepath.Join(dir, "results"),
		Timeout:       config.Timeouts{CompileMs: 120000, TestMs: 30000, TotalMs: 300000},
	}

	var buf bytes.Buffer
	code := runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(dir, "status.json"),
		// Point store at an impossible path to force a save error.
		resultStore: history.NewStore("/dev/null/impossible"),
		opts:        RunCmdOptions{},
	})

	// Exit code must not change due to save failure
	if code != 0 {
		t.Errorf("expected exit 0 (all tests passed), got %d\noutput: %s", code, buf.String())
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	warnings, ok := out["warnings"]
	if !ok {
		t.Fatalf("expected 'warnings' field in JSON output, got: %s", buf.String())
	}
	warnList, _ := warnings.([]any)
	if len(warnList) == 0 {
		t.Error("warnings field must be a non-empty array")
	}
}

func TestRunCmd_PlayMode_PassesPlayModeToRunner(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeCmdRunner{resultsXML: xmlData, exitCode: 0}

	store := history.NewStore(filepath.Join(dir, "results"))
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     filepath.Join(dir, "results"),
		Timeout:       config.Timeouts{TotalMs: 300000},
		TestPlatform:  "play_mode",
	}

	var buf bytes.Buffer
	runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(dir, "status.json"),
		resultStore: store,
		opts:        RunCmdOptions{},
	})

	idx := -1
	for i, a := range fake.lastArgs {
		if a == "-testPlatform" {
			idx = i
			break
		}
	}
	if idx == -1 || idx+1 >= len(fake.lastArgs) {
		t.Fatal("-testPlatform not found in runner args")
	}
	if fake.lastArgs[idx+1] != "PlayMode" {
		t.Errorf("expected PlayMode, got %q", fake.lastArgs[idx+1])
	}
}

func TestRunCmd_SummaryJSON_WrittenToArtifactDir(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeCmdRunner{resultsXML: xmlData, exitCode: 0}

	resultDir := filepath.Join(dir, ".fastplay", "results")
	store := history.NewStore(resultDir)
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     resultDir,
		Timeout:       config.Timeouts{TotalMs: 300000},
	}

	var buf bytes.Buffer
	runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(dir, "status.json"),
		resultStore: store,
		opts:        RunCmdOptions{},
	})

	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	runID, _ := out["run_id"].(string)
	if runID == "" {
		t.Fatal("run_id not in output")
	}

	// artifactRoot = cfg.ProjectPath + "/.fastplay/runs"
	artifactRoot := filepath.Join(dir, ".fastplay", "runs")
	summaryPath := filepath.Join(artifactRoot, runID, "summary.json")
	if _, err := os.Stat(summaryPath); err != nil {
		t.Errorf("expected summary.json at %s, got error: %v", summaryPath, err)
	}
}

func TestRunCmd_ResetShadowFlagExists(t *testing.T) {
	f := runCmd.Flags().Lookup("reset-shadow")
	if f == nil {
		t.Fatal("--reset-shadow flag not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("default should be false, got %q", f.DefValue)
	}
}

func TestRunCmd_WithCompareRun_PopulatesNewFailures(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	store := history.NewStore(resultsDir)

	// Seed a previous run where TestSub passed
	prevID := "20250301-090000"
	_ = store.Save(prevID, &history.RunResult{
		RunID:         prevID,
		SchemaVersion: "1",
		Tests:         []parser.TestCase{{Name: "MyTests.TestSub", Result: "Passed"}},
	})

	// Current run has TestSub failing (one_failure.xml)
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/one_failure.xml")
	fake := &fakeCmdRunner{resultsXML: xmlData, exitCode: 0}

	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     resultsDir,
		Timeout:       config.Timeouts{CompileMs: 120000, TestMs: 30000, TotalMs: 300000},
	}

	var buf bytes.Buffer
	runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(dir, "status.json"),
		resultStore: store,
		opts:        RunCmdOptions{CompareRun: prevID},
	})

	var raw map[string]json.RawMessage
	json.Unmarshal(buf.Bytes(), &raw)
	nf := raw["new_failures"]
	if string(nf) == "null" {
		t.Error("new_failures should be an array when --compare-run specified")
	}
}

func TestRunRun_ConfigError_NoNewFailuresField(t *testing.T) {
	var buf bytes.Buffer
	deps := runDeps{
		loadConfig: func(string) (*config.Config, error) {
			return nil, fmt.Errorf("config not found")
		},
	}
	code := runRun(&buf, deps)
	if code != 5 {
		t.Fatalf("expected exit 5, got %d", code)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := out["new_failures"]; ok {
		t.Error("new_failures must not appear in exit 5 error response")
	}
}

func TestRunRun_InfraError_NoNewFailuresField(t *testing.T) {
	var buf bytes.Buffer
	projectDir := t.TempDir()

	// Block artifact directory creation by placing a regular file where
	// the artifact root directory would be. os.MkdirAll will fail with
	// ENOTDIR, causing Service.Run to return an infra error → exit 1.
	artifactRoot := filepath.Join(projectDir, ".fastplay", "runs")
	_ = os.MkdirAll(filepath.Dir(artifactRoot), 0755)
	_ = os.WriteFile(artifactRoot, []byte("poison"), 0644)

	deps := runDeps{
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{
				UnityPath:   "/fake/unity",
				ProjectPath: projectDir,
				Timeout:     config.Timeouts{TotalMs: 5000},
			}, nil
		},
		// Runner is provided explicitly; it must not be called because the
		// infra error occurs before Unity is invoked.
		runner: runnerFunc(func(_ context.Context, _ []string, _, _ io.Writer) (int, error) {
			t.Error("runner must not be called when artifact dir creation fails")
			return 0, nil
		}),
	}
	code := runRun(&buf, deps)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := out["new_failures"]; ok {
		t.Error("new_failures must not appear in exit 1 error response")
	}
}
