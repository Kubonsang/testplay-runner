// internal/artifacts/store_test.go
package artifacts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
)

func TestStore_PrepareRunDir_CreatesDirectory(t *testing.T) {
	root := t.TempDir()
	store := artifacts.NewStore(filepath.Join(root, ".testplay", "runs"))

	dir, err := store.PrepareRunDir("20260326-120000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, statErr := os.Stat(dir)
	if statErr != nil || !info.IsDir() {
		t.Errorf("expected directory to exist at %s", dir)
	}
}

func TestStore_PrepareRunDir_PathContainsRunID(t *testing.T) {
	root := t.TempDir()
	store := artifacts.NewStore(filepath.Join(root, ".testplay", "runs"))

	dir, err := store.PrepareRunDir("20260326-120000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(dir) != "20260326-120000" {
		t.Errorf("expected directory name to be run_id, got %q", filepath.Base(dir))
	}
}

func TestStore_SaveSummary_WritesSummaryJSON(t *testing.T) {
	root := t.TempDir()
	store := artifacts.NewStore(filepath.Join(root, ".testplay", "runs"))
	_, err := store.PrepareRunDir("20260326-120000")
	if err != nil {
		t.Fatalf("PrepareRunDir: %v", err)
	}

	payload := map[string]any{"exit_code": 0, "total": 5}
	if err := store.SaveSummary("20260326-120000", payload); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}

	summaryPath := filepath.Join(root, ".testplay", "runs", "20260326-120000", "summary.json")
	data, readErr := os.ReadFile(summaryPath)
	if readErr != nil {
		t.Fatalf("summary.json not found: %v", readErr)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("summary.json invalid JSON: %v", err)
	}
	if got["exit_code"] != float64(0) {
		t.Errorf("expected exit_code 0, got %v", got["exit_code"])
	}
}

func TestStore_ResultsFilePath_ReturnsPathInsideRunDir(t *testing.T) {
	root := t.TempDir()
	store := artifacts.NewStore(filepath.Join(root, ".testplay", "runs"))

	path := store.ResultsFilePath("20260326-120000")
	if filepath.Base(path) != "results.xml" {
		t.Errorf("expected results.xml filename, got %q", filepath.Base(path))
	}
	if filepath.Base(filepath.Dir(path)) != "20260326-120000" {
		t.Errorf("expected results.xml inside run dir, got %q", path)
	}
}

func TestStore_SaveRawLogs_WritesStdoutAndStderr(t *testing.T) {
	root := t.TempDir()
	store := artifacts.NewStore(filepath.Join(root, ".testplay", "runs"))
	if _, err := store.PrepareRunDir("20260326-120000"); err != nil {
		t.Fatalf("PrepareRunDir: %v", err)
	}

	if err := store.SaveRawLogs("20260326-120000", []byte("out data"), []byte("err data")); err != nil {
		t.Fatalf("SaveRawLogs: %v", err)
	}

	stdoutPath := store.StdoutFilePath("20260326-120000")
	stderrPath := store.StderrFilePath("20260326-120000")

	got, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("stdout.log not found: %v", err)
	}
	if string(got) != "out data" {
		t.Errorf("stdout.log content = %q, want %q", got, "out data")
	}

	got, err = os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("stderr.log not found: %v", err)
	}
	if string(got) != "err data" {
		t.Errorf("stderr.log content = %q, want %q", got, "err data")
	}
}

func TestStore_Prune_RemovesOldestDirs(t *testing.T) {
	root := t.TempDir()
	store := artifacts.NewStore(root)

	ids := []string{
		"20260401-100000-aaaaaaaa",
		"20260401-110000-bbbbbbbb",
		"20260401-120000-cccccccc",
		"20260401-130000-dddddddd",
	}
	for _, id := range ids {
		dir, err := store.PrepareRunDir(id)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "summary.json"), []byte("{}"), 0644)
	}

	removed, err := store.Prune(2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}

	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Errorf("remaining dirs = %d, want 2", len(entries))
	}
}

func TestStore_Prune_IgnoresNonRunIDDirs(t *testing.T) {
	root := t.TempDir()
	store := artifacts.NewStore(root)

	// Create 3 run-ID dirs and 1 non-run-ID dir
	runIDs := []string{
		"20260401-100000-aaaaaaaa",
		"20260401-110000-bbbbbbbb",
		"20260401-120000-cccccccc",
	}
	for _, id := range runIDs {
		if _, err := store.PrepareRunDir(id); err != nil {
			t.Fatal(err)
		}
	}
	// Create a non-run-ID directory that must NOT be pruned
	os.MkdirAll(filepath.Join(root, "cache"), 0755)

	removed, err := store.Prune(1)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (only run-ID dirs)", removed)
	}

	// The non-run-ID dir must survive
	entries, _ := os.ReadDir(root)
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name()] = true
	}
	if !names["cache"] {
		t.Error("non-run-ID dir 'cache' was incorrectly pruned")
	}
	if !names["20260401-120000-cccccccc"] {
		t.Error("newest run-ID dir was incorrectly pruned")
	}
}

func TestStore_Prune_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	store := artifacts.NewStore(root)
	removed, err := store.Prune(5)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestStore_SaveManifest_WritesManifestJSON(t *testing.T) {
	root := t.TempDir()
	store := artifacts.NewStore(filepath.Join(root, ".testplay", "runs"))
	if _, err := store.PrepareRunDir("20260326-120000"); err != nil {
		t.Fatalf("PrepareRunDir: %v", err)
	}

	m := artifacts.Manifest{
		SchemaVersion: "1",
		RunID:         "20260326-120000",
		ArtifactRoot:  store.RunDir("20260326-120000"),
		ResultsXML:    store.ResultsFilePath("20260326-120000"),
		StdoutLog:     store.StdoutFilePath("20260326-120000"),
		StderrLog:     store.StderrFilePath("20260326-120000"),
		StartedAt:     "2026-03-26T12:00:00Z",
		FinishedAt:    "2026-03-26T12:01:00Z",
		ExitCode:      0,
	}
	if err := store.SaveManifest("20260326-120000", m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	manifestPath := filepath.Join(root, ".testplay", "runs", "20260326-120000", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("manifest.json not found: %v", err)
	}
	var got artifacts.Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("manifest.json invalid JSON: %v", err)
	}
	if got.RunID != "20260326-120000" {
		t.Errorf("manifest run_id = %q, want %q", got.RunID, "20260326-120000")
	}
	if got.ExitCode != 0 {
		t.Errorf("manifest exit_code = %d, want 0", got.ExitCode)
	}
	if got.StdoutLog == "" {
		t.Error("manifest stdout_log must not be empty")
	}
}
