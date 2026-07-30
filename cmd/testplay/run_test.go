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

func TestRunCmd_InvalidWorkspaceBackend_Exit5(t *testing.T) {
	var buf bytes.Buffer
	code := runRun(&buf, runDeps{
		opts: RunCmdOptions{WorkspaceBackend: "unknown"},
	})
	if code != 5 {
		t.Fatalf("exit = %d, want 5", code)
	}
	var output map[string]any
	if err := json.Unmarshal(buf.Bytes(), &output); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !strings.Contains(fmt.Sprint(output["error"]), "legacy or image") {
		t.Fatalf("unexpected error: %v", output["error"])
	}
}

func TestRunCmd_ImageBackendEmitsAdditiveWorkspaceMetrics(t *testing.T) {
	project := makeCmdImageProject(t)
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	fake := &fakeCmdRunner{resultsXML: xmlData}
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   project,
		ResultDir:     filepath.Join(project, ".testplay", "results"),
		Timeout:       config.Timeouts{TotalMs: 300000},
		TestPlatform:  "edit_mode",
	}

	var buf bytes.Buffer
	code := runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(project, "status.json"),
		resultStore: history.NewStore(cfg.ResultDir),
		opts: RunCmdOptions{
			WorkspaceBackend: runsvc.WorkspaceBackendImage,
		},
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output=%s", code, buf.String())
	}
	var output map[string]any
	if err := json.Unmarshal(buf.Bytes(), &output); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if output["backend"] != "shadow" {
		t.Fatalf("backend = %v, want shadow", output["backend"])
	}
	metrics, ok := output["workspace_metrics"].(map[string]any)
	if !ok {
		t.Fatalf("workspace_metrics missing or invalid: %T", output["workspace_metrics"])
	}
	if metrics["workspaceBackend"] != "image" || metrics["imageStatus"] != "valid" {
		t.Fatalf("workspace metrics = %+v", metrics)
	}
	if metrics["fallbackUsed"] != false {
		t.Fatalf("fallbackUsed = %v, want false", metrics["fallbackUsed"])
	}
	for _, field := range []string{
		"baseImageLogicalBytes",
		"baseImagePhysicalBytes",
		"imageStorePhysicalBytes",
		"workspaceLogicalBytes",
		"workspacePhysicalBytes",
		"observedPeakAdditionalPhysicalBytes",
		"retainedPhysicalBytes",
		"cleanupReclaimedPhysicalBytes",
	} {
		value, ok := metrics[field].(float64)
		if !ok || value <= 0 {
			t.Fatalf("%s = %v, want positive numeric metric", field, metrics[field])
		}
	}
}

func makeCmdImageProject(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	for _, dir := range []string{"Assets", "Packages", "ProjectSettings"} {
		if err := os.MkdirAll(filepath.Join(project, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join("Assets", "Test.cs"):                        "// source",
		filepath.Join("Packages", "manifest.json"):                `{"dependencies":{}}`,
		filepath.Join("Packages", "packages-lock.json"):           `{"dependencies":{}}`,
		filepath.Join("ProjectSettings", "ProjectVersion.txt"):    "m_EditorVersion: 6000.3.8f1\n",
		filepath.Join("ProjectSettings", "ProjectSettings.asset"): "PlayerSettings:\n  scriptingBackend: 0\n",
	}
	for rel, contents := range files {
		if err := os.WriteFile(filepath.Join(project, rel), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return project
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

type scenarioCapturingRunner struct {
	resultsXML []byte
	mu         sync.Mutex
	args       [][]string
}

func (c *scenarioCapturingRunner) Run(_ context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	c.mu.Lock()
	c.args = append(c.args, append([]string(nil), args...))
	c.mu.Unlock()
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

func TestRunScenario_FilterAndCategoryForwardedToInstances(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	runner := &scenarioCapturingRunner{resultsXML: xmlData}
	t.Cleanup(func() {
		_ = os.Remove("testplay-status-host.json")
		_ = os.Remove("testplay-status-client.json")
	})

	projectDir := filepath.Join(dir, "project")
	// Both instances share this project, so scenario mode forces shadow —
	// the workspace copy requires the standard Unity project layout.
	for _, sub := range []string{"Assets", "ProjectSettings"} {
		if err := os.MkdirAll(filepath.Join(projectDir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	resultDir := filepath.Join(projectDir, ".testplay", "results")
	cfgData, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"unity_path":     "/fake/unity",
		"project_path":   projectDir,
		"result_dir":     resultDir,
		"timeout":        map[string]any{"total_ms": 300000},
	})
	cfgPath := filepath.Join(projectDir, "testplay.json")
	if err := os.WriteFile(cfgPath, cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	scenarioContent, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"instances": []map[string]any{
			{"role": "host", "config": cfgPath},
			{"role": "client", "config": cfgPath},
		},
	})
	specPath := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(specPath, scenarioContent, 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, scenarioDeps{
		runner: runner,
		opts:   RunCmdOptions{Filter: "ROOM_TRAVERSAL_PORT", Category: "NGO"},
	})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput: %s", code, buf.String())
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.args) != 2 {
		t.Fatalf("expected two Unity invocations, got %d: %v", len(runner.args), runner.args)
	}
	for _, args := range runner.args {
		if !argsContainPair(args, "-testFilter", "ROOM_TRAVERSAL_PORT") {
			t.Errorf("expected -testFilter in args, got: %v", args)
		}
		if !argsContainPair(args, "-testCategory", "NGO") {
			t.Errorf("expected -testCategory in args, got: %v", args)
		}
	}
}

func argsContainPair(args []string, key, value string) bool {
	for i, arg := range args {
		if arg == key && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
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

	// Create a file so that using it as a directory parent fails on all OSes.
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("x"), 0644)

	var buf bytes.Buffer
	code := runRun(&buf, runDeps{
		loadConfig: func(string) (*config.Config, error) { return cfg, nil },
		runner:     fake,
		statusPath: filepath.Join(dir, "status.json"),
		// Point store at a path inside a file to force a save error.
		resultStore: history.NewStore(filepath.Join(blocker, "impossible")),
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

func TestRunScenario_SerializesPerInstanceResultErrorAndHint(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "scenario.json")
	writeScenarioFile(t, specPath, &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{{Role: "solo", Config: "./solo.json"}},
	})

	fakeRun := func(_ context.Context, _ scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		return runsvc.Response{
			RunID:    "20260326-143055-aabbccdd",
			ExitCode: 1,
			Result: &history.RunResult{
				SchemaVersion: "1",
				ExitCode:      1,
				Error:         "Unity could not be launched",
				Hint:          "verify unity_path in testplay.json",
				Tests:         []parser.TestCase{},
				Errors:        []history.CompileError{},
			},
		}, nil
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, scenarioDeps{run: fakeRun})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d\noutput: %s", code, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	instances, _ := out["instances"].([]any)
	if len(instances) != 1 {
		t.Fatalf("expected one instance, got %v", out["instances"])
	}
	inst := instances[0].(map[string]any)
	if inst["error"] != "Unity could not be launched" {
		t.Errorf("instance error missing: %v", inst)
	}
	if inst["hint"] != "verify unity_path in testplay.json" {
		t.Errorf("instance hint missing: %v", inst)
	}
}

func TestRunScenario_SerializesEffectiveExitCodeForInfrastructureError(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "scenario.json")
	writeScenarioFile(t, specPath, &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{{Role: "solo", Config: "./solo.json"}},
	})

	fakeRun := func(_ context.Context, _ scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		return runsvc.Response{ExitCode: 7}, fmt.Errorf("shadow workspace permission denied")
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, scenarioDeps{run: fakeRun})
	if code != 7 {
		t.Fatalf("expected exit 7, got %d\noutput: %s", code, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	instances, _ := out["instances"].([]any)
	if len(instances) != 1 {
		t.Fatalf("expected one instance, got %v", out["instances"])
	}
	inst := instances[0].(map[string]any)
	if inst["exit_code"] != float64(7) || inst["error"] != "shadow workspace permission denied" {
		t.Fatalf("specific exit code and error must both be serialized: %v", inst)
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

func TestRunScenario_MostSevereExitCodePropagated(t *testing.T) {
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

func TestRunScenario_EnvPassedToInstances(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(specPath, []byte(`{
		"schema_version": "1",
		"instances": [
			{"role": "host",   "config": "./h.json", "env": {"PORT": "7777", "ROLE": "HOST"}},
			{"role": "client", "config": "./c.json", "env": {"PORT": "7778", "ROLE": "CLIENT"}}
		]
	}`), 0644)

	envCapture := make(map[string]map[string]string)
	var mu sync.Mutex
	fakeRun := func(_ context.Context, inst scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		mu.Lock()
		envCapture[inst.Role] = inst.Env
		mu.Unlock()
		return runsvc.Response{
			RunID:    "20260403-100000-aabbccdd",
			ExitCode: 0,
			Result: &history.RunResult{
				SchemaVersion: "1", Tests: []parser.TestCase{}, Errors: []history.CompileError{},
			},
		}, nil
	}

	var buf bytes.Buffer
	runScenario(&buf, specPath, scenarioDeps{run: fakeRun})

	mu.Lock()
	defer mu.Unlock()
	if envCapture["host"]["PORT"] != "7777" {
		t.Errorf("host PORT = %q, want 7777", envCapture["host"]["PORT"])
	}
	if envCapture["client"]["ROLE"] != "CLIENT" {
		t.Errorf("client ROLE = %q, want CLIENT", envCapture["client"]["ROLE"])
	}
}

func TestRunScenario_OuterContextDeadline_CancelsInstances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(specPath, []byte(`{
		"schema_version": "1",
		"instances": [
			{"role": "host",   "config": "./h.json"},
			{"role": "client", "config": "./c.json"}
		]
	}`), 0644)

	// Outer context with a very short deadline — simulates scenario-level timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	fakeRun := func(ctx context.Context, inst scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		// Block until context is cancelled — should be cancelled by outer deadline.
		<-ctx.Done()
		return runsvc.Response{ExitCode: 4, Result: &history.RunResult{
			SchemaVersion: "1", ExitCode: 4, TimeoutType: "total",
			Tests: []parser.TestCase{}, Errors: []history.CompileError{},
		}}, nil
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, scenarioDeps{ctx: ctx, run: fakeRun})

	// Instances should have been cancelled by the outer deadline.
	if code == 0 {
		t.Error("expected non-zero exit code when outer context deadline fires")
	}
}

func TestRunScenario_PrunesAfterCompletion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create a project directory with the testplay.json config (max_runs = 2).
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	resultDir := filepath.Join(projectDir, ".testplay", "results")
	artifactRoot := filepath.Join(projectDir, ".testplay", "runs")

	maxRuns := 2
	cfgData, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"unity_path":     "/fake/unity",
		"project_path":   projectDir,
		"result_dir":     resultDir,
		"timeout":        map[string]any{"total_ms": 300000},
		"retention":      map[string]any{"max_runs": maxRuns},
	})
	cfgPath := filepath.Join(projectDir, "testplay.json")
	if err := os.WriteFile(cfgPath, cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	// Pre-populate 4 result files and 4 artifact dirs.
	if err := os.MkdirAll(resultDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifactRoot, 0755); err != nil {
		t.Fatal(err)
	}
	preExistingIDs := []string{
		"20260401-100000-aaaaaaaa",
		"20260401-100001-bbbbbbbb",
		"20260401-100002-cccccccc",
		"20260401-100003-dddddddd",
	}
	for _, id := range preExistingIDs {
		// result file
		if err := os.WriteFile(filepath.Join(resultDir, id+".json"), []byte(`{}`), 0644); err != nil {
			t.Fatal(err)
		}
		// artifact dir
		if err := os.MkdirAll(filepath.Join(artifactRoot, id), 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Write the scenario file referencing the project config.
	scenarioContent, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"instances": []map[string]any{
			{"role": "host", "config": cfgPath},
		},
	})
	specPath := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(specPath, scenarioContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Inject a fake runner that returns a successful response.
	fakeRun := func(_ context.Context, inst scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		return runsvc.Response{
			RunID:    "20260401-120000-eeeeffff",
			ExitCode: 0,
			Result: &history.RunResult{
				SchemaVersion: "1",
				Tests:         []parser.TestCase{},
				Errors:        []history.CompileError{},
			},
		}, nil
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, scenarioDeps{run: fakeRun})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput: %s", code, buf.String())
	}

	// Verify result files: only 2 should remain (pruned from 4).
	resultEntries, err := os.ReadDir(resultDir)
	if err != nil {
		t.Fatalf("cannot read result dir: %v", err)
	}
	resultCount := 0
	for _, e := range resultEntries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			resultCount++
		}
	}
	if resultCount != maxRuns {
		t.Errorf("expected %d result files after prune, got %d", maxRuns, resultCount)
	}

	// Verify artifact dirs: only 2 should remain (pruned from 4).
	artifactEntries, err := os.ReadDir(artifactRoot)
	if err != nil {
		t.Fatalf("cannot read artifact root: %v", err)
	}
	artifactCount := 0
	for _, e := range artifactEntries {
		if e.IsDir() {
			artifactCount++
		}
	}
	if artifactCount != maxRuns {
		t.Errorf("expected %d artifact dirs after prune, got %d", maxRuns, artifactCount)
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

func TestRunCmd_LaunchFailure_Exit1WithHint(t *testing.T) {
	dir := t.TempDir()
	launchErr := &os.PathError{Op: "fork/exec", Path: "/fake/unity", Err: os.ErrNotExist}
	fake := runnerFunc(func(context.Context, []string, io.Writer, io.Writer) (int, error) {
		return -1, launchErr
	})

	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     filepath.Join(dir, "results"),
		Timeout:       config.Timeouts{TotalMs: 300000},
	}
	var buf bytes.Buffer
	code := runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(dir, "status.json"),
		resultStore: history.NewStore(cfg.ResultDir),
		opts:        RunCmdOptions{},
	})
	if code != 1 {
		t.Fatalf("expected exit 1 (dependency error), got %d\noutput: %s", code, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if out["hint"] == nil || out["hint"] == "" {
		t.Error("exit 1 output must include a hint field (Output Design Rule #5)")
	}
	if out["error"] == nil || out["error"] == "" {
		t.Error("exit 1 output must include an error field describing the launch failure")
	}
}

func TestRunScenario_SharedProject_ForcesShadowForAllInstances(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	runner := &scenarioCapturingRunner{resultsXML: xmlData}
	t.Cleanup(func() {
		_ = os.Remove("testplay-status-host.json")
		_ = os.Remove("testplay-status-client.json")
	})

	projectDir := filepath.Join(dir, "project")
	for _, sub := range []string{"Assets", "ProjectSettings"} {
		if err := os.MkdirAll(filepath.Join(projectDir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cfgData, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"unity_path":     "/fake/unity",
		"project_path":   projectDir,
		"timeout":        map[string]any{"total_ms": 300000},
	})
	cfgPath := filepath.Join(projectDir, "testplay.json")
	if err := os.WriteFile(cfgPath, cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	scenarioContent, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"instances": []map[string]any{
			{"role": "host", "config": cfgPath},
			{"role": "client", "config": cfgPath},
		},
	})
	specPath := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(specPath, scenarioContent, 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, scenarioDeps{runner: runner})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput: %s", code, buf.String())
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	instances, _ := out["instances"].([]any)
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}
	for _, raw := range instances {
		inst := raw.(map[string]any)
		// Two cold batchmode processes on ONE project directory race Unity's
		// 'project already open' lock; sharing a project must force shadow.
		if inst["backend"] != "shadow" {
			t.Errorf("instance %v: expected backend \"shadow\" for shared project, got %v", inst["role"], inst["backend"])
		}
	}
}

func TestRunScenario_GlobalCompareRun_RejectedExit5(t *testing.T) {
	dir := t.TempDir()
	scenarioContent, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"instances": []map[string]any{
			{"role": "host", "config": "testplay.json"},
		},
	})
	specPath := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(specPath, scenarioContent, 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	// One global run ID cannot be a baseline for N per-instance stores —
	// broadcasting it silently compared against nothing.
	code := runScenario(&buf, specPath, scenarioDeps{
		opts: RunCmdOptions{CompareRun: "20260701-120000-aabbccdd"},
	})
	if code != 5 {
		t.Fatalf("global --compare-run with --scenario must be rejected with exit 5, got %d\noutput: %s", code, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if out["error"] == nil {
		t.Error("expected error field explaining per-instance compare_run")
	}
}

func TestRunScenario_InstanceCompareRun_ForwardedToInstance(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadXMLFixture(t, "../../internal/parser/testdata/passing.xml")
	runner := &scenarioCapturingRunner{resultsXML: xmlData}
	t.Cleanup(func() { _ = os.Remove("testplay-status-solo.json") })

	projectDir := filepath.Join(dir, "project")
	// UNKNOWN project identity deliberately forces shadow isolation. Keep this
	// fixture valid even on restricted Windows agents where identity proof is
	// unavailable and the conservative shadow path is exercised.
	for _, unityDir := range []string{"Assets", "ProjectSettings", "Packages"} {
		if err := os.MkdirAll(filepath.Join(projectDir, unityDir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cfgData, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"unity_path":     "/fake/unity",
		"project_path":   projectDir,
		"timeout":        map[string]any{"total_ms": 300000},
	})
	cfgPath := filepath.Join(projectDir, "testplay.json")
	if err := os.WriteFile(cfgPath, cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	scenarioContent, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"instances": []map[string]any{
			{"role": "solo", "config": cfgPath, "compare_run": "20990101-000000-deadbeef"},
		},
	})
	specPath := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(specPath, scenarioContent, 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, scenarioDeps{runner: runner})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput: %s", code, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	instances, _ := out["instances"].([]any)
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	inst := instances[0].(map[string]any)
	warnings, _ := inst["warnings"].([]any)
	foundWarning := false
	for _, w := range warnings {
		if s, ok := w.(string); ok && strings.Contains(s, "compare-run") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("instance compare_run must reach runsvc (expected a compare-run warning for the missing baseline), got warnings: %v", warnings)
	}
}
