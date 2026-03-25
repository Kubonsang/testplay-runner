package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
)

func TestResultCmd_ListsHistory(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)

	for _, id := range []string{"20250301-090000", "20250301-100000"} {
		_ = store.Save(id, &history.RunResult{
			RunID:         id,
			SchemaVersion: "1",
			Tests:         []parser.TestCase{},
		})
	}

	var buf bytes.Buffer
	code := runResult(&buf, resultDeps{store: store, last: 0})
	if code != 0 {
		t.Errorf("expected exit 0, got %d", code)
	}

	var out struct {
		Runs []struct {
			RunID string `json:"run_id"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if len(out.Runs) != 2 {
		t.Errorf("expected 2 runs, got %d", len(out.Runs))
	}
}

func TestResultCmd_LastNFlag(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)

	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("2025030%d-100000", i)
		_ = store.Save(id, &history.RunResult{
			RunID:         id,
			SchemaVersion: "1",
			Tests:         []parser.TestCase{},
		})
	}

	var buf bytes.Buffer
	runResult(&buf, resultDeps{store: store, last: 2})

	var out struct {
		Runs []any `json:"runs"`
	}
	json.Unmarshal(buf.Bytes(), &out)
	if len(out.Runs) != 2 {
		t.Errorf("expected 2 with --last 2, got %d", len(out.Runs))
	}
}

func TestResultCmd_EmptyHistory_ReturnsEmptyArray(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)

	var buf bytes.Buffer
	code := runResult(&buf, resultDeps{store: store, last: 0})
	if code != 0 {
		t.Errorf("expected exit 0, got %d", code)
	}

	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	runs, ok := out["runs"]
	if !ok {
		t.Error("runs field missing")
		return
	}
	if runs == nil {
		t.Error("runs must be empty array, not null")
	}
}

func TestResultCmd_SchemaVersionPresent(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	var buf bytes.Buffer
	runResult(&buf, resultDeps{store: store, last: 0})
	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if out["schema_version"] == nil {
		t.Error("schema_version must be present")
	}
}

func TestResultCmd_NoUnityPath_StillWorks(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(filepath.Join(dir, "results"))

	var buf bytes.Buffer
	code := runResult(&buf, resultDeps{store: store, last: 0})
	if code != 0 {
		t.Errorf("expected exit 0, got %d\noutput: %s", code, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	runs, ok := out["runs"]
	if !ok {
		t.Error("expected 'runs' field in output")
	}
	if runsSlice, ok := runs.([]any); ok {
		if len(runsSlice) != 0 {
			t.Errorf("expected empty runs, got %d", len(runsSlice))
		}
	}
}

// suppress unused import warning
var _ = os.DevNull
