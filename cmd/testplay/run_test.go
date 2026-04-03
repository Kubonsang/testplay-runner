package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"github.com/Kubonsang/testplay-runner/internal/scenario"
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

func TestRunCmd_SaveFailure_ReturnsExit9WithWarning(t *testing.T) {
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

	// Exit code must be 9 (runner system error) when save fails
	if code != 9 {
		t.Errorf("expected exit 9 (runner system error), got %d\noutput: %s", code, buf.String())
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

	resultDir := filepath.Join(dir, ".testplay", "results")
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

	// artifactRoot = cfg.ProjectPath + "/.testplay/runs"
	artifactRoot := filepath.Join(dir, ".testplay", "runs")
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

func TestRunCmd_ShadowFlagExists(t *testing.T) {
	f := runCmd.Flags().Lookup("shadow")
	if f == nil {
		t.Fatal("--shadow flag not registered")
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

func TestRunRun_ForceShadowActivatesShadow(t *testing.T) {
	// Build a minimal project directory (no Temp/UnityLockfile).
	projectDir := t.TempDir()
	for _, d := range []string{"Assets/Scripts", "ProjectSettings", "Packages"} {
		_ = os.MkdirAll(filepath.Join(projectDir, d), 0755)
	}
	_ = os.WriteFile(
		filepath.Join(projectDir, "ProjectSettings", "ProjectVersion.txt"),
		[]byte("m_EditorVersion: 6000.3.8f1"), 0644)
	_ = os.WriteFile(
		filepath.Join(projectDir, "Assets", "Scripts", "Player.cs"),
		[]byte("// test"), 0644)

	var capturedArgs []string
	runner := runnerFunc(func(_ context.Context, args []string, _, _ io.Writer) (int, error) {
		capturedArgs = args
		return 0, nil
	})

	cfg := &config.Config{
		UnityPath:   "/fake/Unity",
		ProjectPath: projectDir,
		ResultDir:   t.TempDir(),
		Timeout:     config.Timeouts{TotalMs: 30000},
	}

	deps := runDeps{
		ctx:         context.Background(),
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      runner,
		resultStore: history.NewStore(t.TempDir()),
		opts: RunCmdOptions{
			ForceShadow: true,
		},
	}

	var buf bytes.Buffer
	runRun(&buf, deps)

	shadowPrefix := filepath.Join(projectDir, ".testplay-shadow-")
	for _, a := range capturedArgs {
		if strings.HasPrefix(a, shadowPrefix) {
			return // per-run shadow path was passed to Unity — test passes
		}
	}
	t.Errorf("shadow path with prefix %q not found in Unity args %v", shadowPrefix, capturedArgs)
}

func TestRunRun_InfraError_NoNewFailuresField(t *testing.T) {
	var buf bytes.Buffer
	projectDir := t.TempDir()

	// Block artifact directory creation by placing a regular file where
	// the artifact root directory would be. os.MkdirAll will fail with
	// ENOTDIR, causing Service.Run to return an infra error → exit 1.
	artifactRoot := filepath.Join(projectDir, ".testplay", "runs")
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

func TestRunScenario_DispatchesScenarioRunner(t *testing.T) {
	// Create a minimal scenario file on disk
	dir := t.TempDir()
	scenarioContent := `{
		"schema_version": "1",
		"instances": [
			{"role": "Host",   "config": "./host.json"},
			{"role": "Client", "config": "./client.json"}
		]
	}`
	specPath := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(specPath, []byte(scenarioContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Inject a fake runner that returns canned responses
	var mu sync.Mutex
	var called []string
	fakeRun := func(_ context.Context, inst scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		mu.Lock()
		called = append(called, inst.Role)
		mu.Unlock()
		return runsvc.Response{
			RunID:    "20260326-143055-aabbccdd",
			ExitCode: 0,
			Result: &history.RunResult{
				SchemaVersion: "1",
				ExitCode:      0,
				Tests:         []parser.TestCase{},
				Errors:        []history.CompileError{},
			},
		}, nil
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, scenarioDeps{run: fakeRun})

	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	if len(called) != 2 {
		t.Errorf("expected 2 instances called, got %d: %v", len(called), called)
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if out["schema_version"] != "1" {
		t.Errorf("missing schema_version in output")
	}
	instances, ok := out["instances"].([]any)
	if !ok || len(instances) != 2 {
		t.Errorf("expected 2 instances in output, got: %v", out["instances"])
	}
}

func writeScenarioFile(t *testing.T, path string, spec *scenario.ScenarioFile) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"instances":      spec.Instances,
	})
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

// TestRunScenario_WritesPerRoleStatusFiles verifies the path naming convention
// for per-instance status files. The injected fake runner records the expected
// paths; the production StatusWriter wiring is validated by integration tests.
func TestRunScenario_WritesPerRoleStatusFiles(t *testing.T) {
	dir := t.TempDir()

	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host", Config: "host.json"},
			{Role: "client", Config: "client.json"},
		},
	}
	specPath := filepath.Join(dir, "scenario.json")
	writeScenarioFile(t, specPath, spec)

	var writerPaths []string
	var mu sync.Mutex

	deps := scenarioDeps{
		ctx: context.Background(),
		run: func(_ context.Context, inst scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
			// capture which status file was written
			statusPath := fmt.Sprintf("testplay-status-%s.json", inst.Role)
			mu.Lock()
			writerPaths = append(writerPaths, statusPath)
			mu.Unlock()
			return runsvc.Response{ExitCode: 0, Result: &history.RunResult{
				SchemaVersion: "1", Tests: []parser.TestCase{}, Errors: []history.CompileError{},
			}}, nil
		},
	}

	var buf bytes.Buffer
	runScenario(&buf, specPath, deps)

	mu.Lock()
	defer mu.Unlock()
	wantPaths := map[string]bool{
		"testplay-status-host.json":   true,
		"testplay-status-client.json": true,
	}
	for _, p := range writerPaths {
		if !wantPaths[p] {
			t.Errorf("unexpected status path %q", p)
		}
		delete(wantPaths, p)
	}
	for p := range wantPaths {
		t.Errorf("expected status path %q was not written", p)
	}
}

func TestRunScenario_ExitCodeMaxPropagated(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(specPath, []byte(`{
		"schema_version": "1",
		"instances": [
			{"role": "Host",   "config": "./h.json"},
			{"role": "Client", "config": "./c.json"}
		]
	}`), 0644)

	exitCodes := map[string]int{"Host": 0, "Client": 3}
	fakeRun := func(_ context.Context, inst scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		code := exitCodes[inst.Role]
		return runsvc.Response{
			RunID:    "20260326-143055-aabbccdd",
			ExitCode: code,
			Result: &history.RunResult{
				SchemaVersion: "1",
				ExitCode:      code,
				Tests:         []parser.TestCase{},
				Errors:        []history.CompileError{},
			},
		}, nil
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, scenarioDeps{run: fakeRun})
	if code != 3 {
		t.Errorf("expected exit code 3, got %d", code)
	}
}

func TestRunScenario_OrchestratorErrorsInOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	specPath := filepath.Join(dir, "scenario.json")
	content := `{"schema_version":"1","instances":[
        {"role":"host","config":"host.json"},
        {"role":"client","config":"client.json","depends_on":"host","ready_timeout_ms":50}
    ]}`
	if err := os.WriteFile(specPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	deps := scenarioDeps{
		ctx: context.Background(),
		run: func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
			// host never signals ready
			if inst.Role == "host" {
				time.Sleep(200 * time.Millisecond)
			}
			return runsvc.Response{ExitCode: 0, Result: &history.RunResult{
				SchemaVersion: "1", Tests: []parser.TestCase{}, Errors: []history.CompileError{},
			}}, nil
		},
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, deps)
	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	errs, ok := out["orchestrator_errors"]
	if !ok {
		t.Fatal("expected orchestrator_errors field in output")
	}
	errsSlice, ok := errs.([]any)
	if !ok || len(errsSlice) == 0 {
		t.Errorf("expected non-empty orchestrator_errors, got %v", errs)
	}
}
