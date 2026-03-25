//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
)

// integrationRunner is a fake Unity runner for integration tests.
type integrationRunner struct {
	passingXML []byte
}

func (r *integrationRunner) Run(_ context.Context, args []string) ([]byte, []byte, int, error) {
	for i, a := range args {
		if a == "-testResults" && i+1 < len(args) && r.passingXML != nil {
			_ = os.WriteFile(args[i+1], r.passingXML, 0644)
		}
	}
	return nil, nil, 0, nil
}

func TestFullPipeline_CheckListRunResult(t *testing.T) {
	dir := t.TempDir()

	// Create test data: passing XML fixture
	xmlData, err := os.ReadFile("../../internal/parser/testdata/passing.xml")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	// Create a fake Unity project with one test file
	assetsDir := filepath.Join(dir, "Assets", "Tests")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		t.Fatal(err)
	}
	csFile := filepath.Join(assetsDir, "MyTest.cs")
	os.WriteFile(csFile, []byte(`using NUnit.Framework;
public class MyTest {
    [Test]
    public void TestAdd() {}
}
`), 0644)

	resultsDir := filepath.Join(dir, "results")
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   dir,
		ResultDir:     resultsDir,
		Timeout:       config.Timeouts{CompileMs: 120000, TestMs: 30000, TotalMs: 300000},
	}

	// Step 1: check
	var checkBuf bytes.Buffer
	checkCode := runCheck(&checkBuf, checkDeps{
		loadConfig: func(string) (*config.Config, error) { return cfg, nil },
		fileExists: func(string) bool { return true },
		configPath: "fastplay.json",
	})
	if checkCode != 0 {
		t.Errorf("check: expected exit 0, got %d: %s", checkCode, checkBuf.String())
	}
	var checkOut map[string]any
	json.Unmarshal(checkBuf.Bytes(), &checkOut)
	if checkOut["ready"] != true {
		t.Error("check: expected ready:true")
	}

	// Step 2: list
	var listBuf bytes.Buffer
	listCode := runList(&listBuf, listDeps{projectPath: dir})
	if listCode != 0 {
		t.Errorf("list: expected exit 0, got %d", listCode)
	}
	var listOut struct {
		Tests []string `json:"tests"`
	}
	json.Unmarshal(listBuf.Bytes(), &listOut)
	if len(listOut.Tests) == 0 {
		t.Error("list: expected at least one test name")
	}

	// Step 3: run
	store := history.NewStore(resultsDir)
	runner := &integrationRunner{passingXML: xmlData}
	var runBuf bytes.Buffer
	runCode := runRun(&runBuf, runDeps{
		loadConfig:  func(string) (*config.Config, error) { return cfg, nil },
		runner:      runner,
		statusPath:  filepath.Join(dir, "fastplay-status.json"),
		resultStore: store,
		opts:        RunCmdOptions{},
	})
	if runCode != 0 {
		t.Errorf("run: expected exit 0, got %d: %s", runCode, runBuf.String())
	}
	var runOut map[string]any
	json.Unmarshal(runBuf.Bytes(), &runOut)
	runID, _ := runOut["run_id"].(string)
	if runID == "" {
		t.Error("run: expected non-empty run_id")
	}

	// Step 4: result
	var resultBuf bytes.Buffer
	resultCode := runResult(&resultBuf, resultDeps{store: store, last: 0})
	if resultCode != 0 {
		t.Errorf("result: expected exit 0, got %d", resultCode)
	}
	var resultOut struct {
		Runs []struct {
			RunID string           `json:"run_id"`
			Tests []parser.TestCase `json:"tests"`
		} `json:"runs"`
	}
	json.Unmarshal(resultBuf.Bytes(), &resultOut)
	if len(resultOut.Runs) != 1 {
		t.Errorf("result: expected 1 run, got %d", len(resultOut.Runs))
	}
	if resultOut.Runs[0].RunID != runID {
		t.Errorf("result: run_id mismatch: got %q, want %q", resultOut.Runs[0].RunID, runID)
	}
}
