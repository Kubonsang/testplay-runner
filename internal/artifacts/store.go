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
	path := filepath.Join(s.root, runID, "summary.json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("writing summary.json for run %s: %w", runID, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing summary.json for run %s: %w", runID, err)
	}
	return nil
}
