// internal/artifacts/store.go
package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Store manages the per-run artifact directory tree.
// Phase A layout:
//
//	<root>/<run_id>/
//	    results.xml
//	    summary.json
type Store struct {
	root string
}

// NewStore creates a Store rooted at the given directory
// (typically .fastplay/runs relative to the project).
func NewStore(root string) *Store {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root // fallback: keep as-is if Abs fails
	}
	return &Store{root: abs}
}

// PrepareRunDir creates <root>/<runID>/ and returns its absolute path.
func (s *Store) PrepareRunDir(runID string) (string, error) {
	dir := filepath.Join(s.root, runID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating artifact dir for run %s: %w", runID, err)
	}
	return dir, nil
}

// ResultsFilePath returns the canonical path for the Unity results XML
// for the given run. Call PrepareRunDir first.
func (s *Store) ResultsFilePath(runID string) string {
	return filepath.Join(s.root, runID, "results.xml")
}

// SaveSummary marshals v as indented JSON and writes it to
// <root>/<runID>/summary.json.
func (s *Store) SaveSummary(runID string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling summary for run %s: %w", runID, err)
	}
	finalPath := filepath.Join(s.root, runID, "summary.json")
	// Write to a temp file in the same directory, then rename atomically.
	// This prevents partial files from blocking future saves for the same runID.
	tmp, err := os.CreateTemp(filepath.Dir(finalPath), "summary-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file for summary (run %s): %w", runID, err)
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		if writeErr != nil {
			return fmt.Errorf("writing summary.json for run %s: %w", runID, writeErr)
		}
		return fmt.Errorf("closing temp file for summary (run %s): %w", runID, closeErr)
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("committing summary.json for run %s: %w", runID, err)
	}
	return nil
}
