package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/ipc"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"github.com/Kubonsang/testplay-runner/internal/scenario"
)

// scenario_run_id appears at the top level of the output unconditionally.
func TestRunScenario_OutputContainsScenarioRunID(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(specPath, []byte(`{
		"schema_version": "1",
		"instances": [
			{"role": "host", "config": "./h.json"}
		]
	}`), 0644)

	fakeRun := func(_ context.Context, _ scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		return runsvc.Response{
			RunID:    "20260424-100000-aabbccdd",
			ExitCode: 0,
			Result: &history.RunResult{
				SchemaVersion: "1", Tests: []parser.TestCase{}, Errors: []history.CompileError{},
			},
		}, nil
	}

	var buf bytes.Buffer
	runScenario(&buf, specPath, scenarioDeps{run: fakeRun})

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	id, ok := out["scenario_run_id"].(string)
	if !ok || id == "" {
		t.Errorf("scenario_run_id missing or empty: %v", out["scenario_run_id"])
	}
}

// When ipcBusPath is provided via deps, every instance's env should carry it.
func TestRunScenario_TestplayIpcBusEnvInjected(t *testing.T) {
	dir := t.TempDir()
	busPath := filepath.Join(dir, "bus.ndjson")
	if f, err := os.OpenFile(busPath, os.O_CREATE|os.O_WRONLY, 0644); err != nil {
		t.Fatal(err)
	} else {
		_ = f.Close()
	}

	specPath := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(specPath, []byte(`{
		"schema_version": "1",
		"instances": [
			{"role": "host",   "config": "./h.json"},
			{"role": "client", "config": "./c.json"}
		]
	}`), 0644)

	envCapture := make(map[string]map[string]string)
	var mu sync.Mutex
	fakeRun := func(_ context.Context, inst scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		mu.Lock()
		envCapture[inst.Role] = inst.Env
		mu.Unlock()
		return runsvc.Response{
			RunID:    "20260424-100000-aabbccdd",
			ExitCode: 0,
			Result: &history.RunResult{
				SchemaVersion: "1", Tests: []parser.TestCase{}, Errors: []history.CompileError{},
			},
		}, nil
	}

	var buf bytes.Buffer
	runScenario(&buf, specPath, scenarioDeps{run: fakeRun, ipcBusPath: busPath})

	mu.Lock()
	defer mu.Unlock()
	for _, role := range []string{"host", "client"} {
		got, ok := envCapture[role]["TESTPLAY_IPC_BUS"]
		if !ok {
			t.Errorf("%s: TESTPLAY_IPC_BUS missing from env: %v", role, envCapture[role])
			continue
		}
		if got != busPath {
			t.Errorf("%s: TESTPLAY_IPC_BUS = %q, want %q", role, got, busPath)
		}
	}
}

// User-supplied TESTPLAY_IPC_BUS in scenario JSON env must override.
func TestRunScenario_UserEnvOverridesIpcBus(t *testing.T) {
	dir := t.TempDir()
	busPath := filepath.Join(dir, "bus.ndjson")
	_ = os.WriteFile(busPath, nil, 0644)

	specPath := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(specPath, []byte(`{
		"schema_version": "1",
		"instances": [
			{"role": "host", "config": "./h.json", "env": {"TESTPLAY_IPC_BUS": "/custom/path"}}
		]
	}`), 0644)

	var captured string
	fakeRun := func(_ context.Context, inst scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		captured = inst.Env["TESTPLAY_IPC_BUS"]
		return runsvc.Response{
			RunID: "x", ExitCode: 0,
			Result: &history.RunResult{SchemaVersion: "1", Tests: []parser.TestCase{}, Errors: []history.CompileError{}},
		}, nil
	}

	var buf bytes.Buffer
	runScenario(&buf, specPath, scenarioDeps{run: fakeRun, ipcBusPath: busPath})

	if captured != "/custom/path" {
		t.Errorf("user override lost: got %q, want /custom/path", captured)
	}
}

// IPC bus directories under .testplay/ipc/ should obey the same retention
// policy as run artifacts.
func TestRunScenario_PrunesIpcBusDirs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	projectDir := filepath.Join(dir, "project")
	resultDir := filepath.Join(projectDir, ".testplay", "results")
	ipcRoot := filepath.Join(projectDir, ".testplay", "ipc")
	if err := os.MkdirAll(ipcRoot, 0755); err != nil {
		t.Fatal(err)
	}

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

	// Pre-populate 4 IPC scenario directories.
	preExisting := []string{
		"20260401-100000-aaaaaaaa",
		"20260401-100001-bbbbbbbb",
		"20260401-100002-cccccccc",
		"20260401-100003-dddddddd",
	}
	for _, id := range preExisting {
		if err := os.MkdirAll(filepath.Join(ipcRoot, id), 0755); err != nil {
			t.Fatal(err)
		}
	}

	scenarioContent, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"instances":      []map[string]any{{"role": "host", "config": cfgPath}},
	})
	specPath := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(specPath, scenarioContent, 0644)

	fakeRun := func(_ context.Context, _ scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		return runsvc.Response{
			RunID: "x", ExitCode: 0,
			Result: &history.RunResult{SchemaVersion: "1", Tests: []parser.TestCase{}, Errors: []history.CompileError{}},
		}, nil
	}

	var buf bytes.Buffer
	if code := runScenario(&buf, specPath, scenarioDeps{run: fakeRun}); code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}

	entries, err := os.ReadDir(ipcRoot)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	// 4 pre-existing → pruned to maxRuns(2). This test injects a fake runner
	// so the production IPC bus creation path is skipped — no fresh dir.
	dirCount := 0
	for _, e := range entries {
		if e.IsDir() {
			dirCount++
		}
	}
	if dirCount != maxRuns {
		t.Errorf("ipc dir count = %d, want %d", dirCount, maxRuns)
	}
}

// End-to-end at the cmd layer: bus exchange surfaces in instances[].ipc_messages
// and instances[].ipc_summary in the output JSON.
func TestRunScenario_OutputContainsIpcMessagesAndSummary(t *testing.T) {
	dir := t.TempDir()
	busPath := filepath.Join(dir, "bus.ndjson")
	_ = os.WriteFile(busPath, nil, 0644)

	specPath := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(specPath, []byte(`{
		"schema_version": "1",
		"instances": [
			{"role": "host",   "config": "./h.json"},
			{"role": "client", "config": "./c.json", "depends_on": "host", "ready_timeout_ms": 5000}
		]
	}`), 0644)

	fakeRun := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		busFile := inst.Env["TESTPLAY_IPC_BUS"]
		if inst.Role == "host" {
			w, _ := ipc.NewBusWriter(busFile, "host")
			_, _ = w.Append(ipc.Message{To: "*", Kind: "ready"})
			if readyCh != nil {
				close(readyCh)
			}
			time.Sleep(150 * time.Millisecond)
		} else {
			time.Sleep(150 * time.Millisecond)
		}
		return runsvc.Response{
			RunID: inst.Role, ExitCode: 0,
			Result: &history.RunResult{SchemaVersion: "1", Tests: []parser.TestCase{}, Errors: []history.CompileError{}},
		}, nil
	}

	var buf bytes.Buffer
	runScenario(&buf, specPath, scenarioDeps{run: fakeRun, ipcBusPath: busPath})

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, buf.String())
	}

	insts, ok := out["instances"].([]any)
	if !ok || len(insts) != 2 {
		t.Fatalf("instances missing or wrong shape: %v", out["instances"])
	}

	for _, raw := range insts {
		inst := raw.(map[string]any)
		role := inst["role"].(string)
		msgs, hasMsgs := inst["ipc_messages"].([]any)
		summary, hasSummary := inst["ipc_summary"].(map[string]any)
		if !hasMsgs || !hasSummary {
			t.Errorf("%s missing ipc_messages/ipc_summary: %+v", role, inst)
			continue
		}
		if len(msgs) != 1 {
			t.Errorf("%s: %d messages, want 1", role, len(msgs))
		}
		switch role {
		case "host":
			if got := summary["sent_count"].(float64); got != 1 {
				t.Errorf("host sent_count = %v, want 1", got)
			}
			if got := summary["received_count"].(float64); got != 0 {
				t.Errorf("host received_count = %v, want 0", got)
			}
		case "client":
			if got := summary["received_count"].(float64); got != 1 {
				t.Errorf("client received_count = %v, want 1", got)
			}
			if got := summary["sent_count"].(float64); got != 0 {
				t.Errorf("client sent_count = %v, want 0", got)
			}
		}
	}
}
