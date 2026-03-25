package history_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
)

func TestSave_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	runID := "20250301-102200"
	result := &history.RunResult{RunID: runID, SchemaVersion: "1"}

	if err := store.Save(runID, result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(dir, runID+".json")
	if _, err := os.Stat(expected); os.IsNotExist(err) {
		t.Errorf("result file not found at %s", expected)
	}
}

func TestSave_NeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	runID := "20250301-102200"

	r1 := &history.RunResult{RunID: runID, SchemaVersion: "1", ExitCode: 0}
	r2 := &history.RunResult{RunID: runID, SchemaVersion: "1", ExitCode: 3}

	_ = store.Save(runID, r1)
	err := store.Save(runID, r2)
	if !errors.Is(err, history.ErrRunExists) {
		t.Errorf("expected ErrRunExists, got %v", err)
	}

	loaded, _ := store.Load(runID)
	if loaded.ExitCode != 0 {
		t.Error("original result should be preserved, got overwritten")
	}
}

func TestLoad_NotFound(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	_, err := store.Load("nonexistent")
	if !errors.Is(err, history.ErrRunNotFound) {
		t.Errorf("got %v, want ErrRunNotFound", err)
	}
}

func TestLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	runID := "20250301-110000"
	original := &history.RunResult{
		RunID:         runID,
		SchemaVersion: "1",
		ExitCode:      3,
		Tests: []parser.TestCase{
			{Name: "MyTest.Foo", Result: "Failed"},
		},
	}
	_ = store.Save(runID, original)
	loaded, err := store.Load(runID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ExitCode != 3 {
		t.Errorf("got ExitCode %d, want 3", loaded.ExitCode)
	}
	if len(loaded.Tests) != 1 {
		t.Errorf("got %d tests, want 1", len(loaded.Tests))
	}
}

func TestList_ReturnsChronologicalOrder(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	ids := []string{"20250301-090000", "20250301-100000", "20250301-110000"}
	for _, id := range ids {
		_ = store.Save(id, &history.RunResult{RunID: id, SchemaVersion: "1"})
	}

	list, err := store.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3, got %d", len(list))
	}
	// Most recent first
	if list[0].RunID != "20250301-110000" {
		t.Errorf("expected most recent first, got %q", list[0].RunID)
	}
}

func TestList_LastNLimit(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("2025030%d-100000", i)
		_ = store.Save(id, &history.RunResult{RunID: id, SchemaVersion: "1"})
	}

	list, err := store.List(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Errorf("expected 3 with last=3, got %d", len(list))
	}
}

func TestList_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	list, err := store.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestCompare_NewFailures(t *testing.T) {
	prev := &history.RunResult{
		Tests: []parser.TestCase{
			{Name: "A.Pass", Result: "Passed"},
			{Name: "A.Fail", Result: "Failed"},
		},
	}
	curr := &history.RunResult{
		Tests: []parser.TestCase{
			{Name: "A.Pass", Result: "Failed"}, // newly failing
			{Name: "A.Fail", Result: "Failed"}, // still failing — not new
		},
	}
	newFails := history.Compare(prev, curr)
	if len(newFails) != 1 {
		t.Fatalf("expected 1 new failure, got %d", len(newFails))
	}
	if newFails[0].Name != "A.Pass" {
		t.Errorf("wrong test name: %q", newFails[0].Name)
	}
}

func TestCompare_NoNewFailures(t *testing.T) {
	prev := &history.RunResult{Tests: []parser.TestCase{{Name: "A", Result: "Passed"}}}
	curr := &history.RunResult{Tests: []parser.TestCase{{Name: "A", Result: "Passed"}}}
	newFails := history.Compare(prev, curr)
	if len(newFails) != 0 {
		t.Errorf("expected no new failures, got %d", len(newFails))
	}
}

func TestCompare_NilPrev_ReturnsNil(t *testing.T) {
	curr := &history.RunResult{Tests: []parser.TestCase{{Name: "A", Result: "Failed"}}}
	newFails := history.Compare(nil, curr)
	if newFails != nil {
		t.Errorf("expected nil when no compare-run, got %v", newFails)
	}
}
