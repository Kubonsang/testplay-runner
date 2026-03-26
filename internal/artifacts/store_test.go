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
	store := artifacts.NewStore(filepath.Join(root, ".fastplay", "runs"))

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
	store := artifacts.NewStore(filepath.Join(root, ".fastplay", "runs"))

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
	store := artifacts.NewStore(filepath.Join(root, ".fastplay", "runs"))
	_, err := store.PrepareRunDir("20260326-120000")
	if err != nil {
		t.Fatalf("PrepareRunDir: %v", err)
	}

	payload := map[string]any{"exit_code": 0, "total": 5}
	if err := store.SaveSummary("20260326-120000", payload); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}

	summaryPath := filepath.Join(root, ".fastplay", "runs", "20260326-120000", "summary.json")
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
	store := artifacts.NewStore(filepath.Join(root, ".fastplay", "runs"))

	path := store.ResultsFilePath("20260326-120000")
	if filepath.Base(path) != "results.xml" {
		t.Errorf("expected results.xml filename, got %q", filepath.Base(path))
	}
	if filepath.Base(filepath.Dir(path)) != "20260326-120000" {
		t.Errorf("expected results.xml inside run dir, got %q", path)
	}
}
