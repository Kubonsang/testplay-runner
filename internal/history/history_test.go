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
	_, err := store.Load("20250101-000000")
	if !errors.Is(err, history.ErrRunNotFound) {
		t.Errorf("got %v, want ErrRunNotFound", err)
	}
}

func TestSave_InvalidRunID(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	err := store.Save("../evil", &history.RunResult{RunID: "../evil", SchemaVersion: "1"})
	if !errors.Is(err, history.ErrInvalidRunID) {
		t.Errorf("got %v, want ErrInvalidRunID", err)
	}
}

func TestLoad_InvalidRunID(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	_, err := store.Load("../evil")
	if !errors.Is(err, history.ErrInvalidRunID) {
		t.Errorf("got %v, want ErrInvalidRunID", err)
	}
}

func TestLoad_ValidRunIDFormat(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	_, err := store.Load("20250325-143000")
	// May get ErrRunNotFound, but NOT ErrInvalidRunID
	if errors.Is(err, history.ErrInvalidRunID) {
		t.Errorf("valid run ID should not return ErrInvalidRunID, got %v", err)
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

func TestStore_SaveAndLoad_NewFormatRunID(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)

	// New format: YYYYMMDD-HHMMSS-xxxxxxxx
	runID := "20250301-102200-a3f8b2c1"
	result := &history.RunResult{
		RunID:         runID,
		SchemaVersion: "1",
		ExitCode:      0,
		Tests:         []parser.TestCase{},
	}

	if err := store.Save(runID, result); err != nil {
		t.Fatalf("Save with new-format runID: %v", err)
	}

	loaded, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load with new-format runID: %v", err)
	}
	if loaded.RunID != runID {
		t.Errorf("expected RunID %q, got %q", runID, loaded.RunID)
	}
}

func TestStore_Prune_KeepsNewest(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)

	ids := []string{
		"20260401-100000-aaaaaaaa",
		"20260401-110000-bbbbbbbb",
		"20260401-120000-cccccccc",
		"20260401-130000-dddddddd",
		"20260401-140000-eeeeeeee",
	}
	for _, id := range ids {
		err := store.Save(id, &history.RunResult{
			SchemaVersion: "1",
			RunID:         id,
			Tests:         []parser.TestCase{},
		})
		if err != nil {
			t.Fatalf("Save(%s): %v", id, err)
		}
	}

	removed, err := store.Prune(3)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}

	remaining, _ := store.List(0)
	if len(remaining) != 3 {
		t.Fatalf("remaining = %d, want 3", len(remaining))
	}
	if remaining[0].RunID != "20260401-140000-eeeeeeee" {
		t.Errorf("newest = %q", remaining[0].RunID)
	}
	if remaining[2].RunID != "20260401-120000-cccccccc" {
		t.Errorf("oldest kept = %q", remaining[2].RunID)
	}

	_, err = store.Load("20260401-100000-aaaaaaaa")
	if !errors.Is(err, history.ErrRunNotFound) {
		t.Errorf("expected ErrRunNotFound for pruned run, got %v", err)
	}
}

func TestStore_Prune_NothingToRemove(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	_ = store.Save("20260401-100000-aaaaaaaa", &history.RunResult{
		SchemaVersion: "1", Tests: []parser.TestCase{},
	})
	removed, err := store.Prune(10)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestStore_Prune_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	store := history.NewStore(dir)
	removed, err := store.Prune(5)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestLoad_InvalidRunID_NewFormatVariants(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := history.NewStore(dir)

	invalid := []string{
		"20250301-102200-A3F8B2C1",  // uppercase hex rejected
		"20250301-102200-a3f8b2c",   // 7 chars (too short)
		"20250301-102200-a3f8b2c1d", // 9 chars (too long)
		"20250301-102200-",           // empty suffix
		"20250301-102200-gggggggg",  // non-hex chars
	}
	for _, id := range invalid {
		_, err := store.Load(id)
		if !errors.Is(err, history.ErrInvalidRunID) {
			t.Errorf("expected ErrInvalidRunID for %q, got %v", id, err)
		}
	}
}
