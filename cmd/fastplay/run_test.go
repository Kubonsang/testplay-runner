package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func (c *capturingRunner) Run(_ context.Context, args []string) ([]byte, []byte, int, error) {
	c.lastArgs = args
	for i, a := range args {
		if a == "-testResults" && i+1 < len(args) && c.resultsXML != nil {
			_ = os.WriteFile(args[i+1], c.resultsXML, 0644)
		}
	}
	return nil, nil, 0, nil
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

	saveErr := errors.New("disk full")
	failingSave := func(runID string, result *history.RunResult) error {
		return saveErr
	}

	var buf bytes.Buffer
	code := runRun(&buf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      fake,
		statusPath:  filepath.Join(dir, "status.json"),
		resultStore: history.NewStore(filepath.Join(dir, "results")),
		saveFunc:    failingSave,
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
	warning, ok := out["warning"]
	if !ok {
		t.Fatalf("expected 'warning' field in JSON output, got: %s", buf.String())
	}
	warnStr, _ := warning.(string)
	if warnStr == "" {
		t.Error("warning field must be a non-empty string")
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
