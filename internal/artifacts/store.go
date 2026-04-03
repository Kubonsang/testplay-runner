// internal/artifacts/store.go
package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// runIDPattern matches run-ID directory names (YYYYMMDD-HHMMSS or YYYYMMDD-HHMMSS-xxxxxxxx).
var runIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(-[0-9a-f]{8})?$`)

// Store manages the per-run artifact directory tree.
// Phase B layout:
//
//	<root>/<run_id>/
//	    results.xml
//	    summary.json
//	    manifest.json
//	    stdout.log
//	    stderr.log
type Store struct {
	root string
}

// Manifest is written to manifest.json for each run.
// All path fields are absolute for unambiguous cross-tool access.
type Manifest struct {
	SchemaVersion string `json:"schema_version"`
	RunID         string `json:"run_id"`
	ArtifactRoot  string `json:"artifact_root"`
	ResultsXML    string `json:"results_xml"`
	StdoutLog     string `json:"stdout_log"`
	StderrLog     string `json:"stderr_log"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at"`
	ExitCode      int    `json:"exit_code"`
}

// NewStore creates a Store rooted at the given directory
// (typically .testplay/runs relative to the project).
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

// RunDir returns the absolute path of the artifact directory for a run.
func (s *Store) RunDir(runID string) string {
	return filepath.Join(s.root, runID)
}

// ResultsFilePath returns the canonical path for the Unity results XML
// for the given run. Call PrepareRunDir first.
func (s *Store) ResultsFilePath(runID string) string {
	return filepath.Join(s.root, runID, "results.xml")
}

// StdoutFilePath returns the canonical path for the stdout log for the given run.
func (s *Store) StdoutFilePath(runID string) string {
	return filepath.Join(s.root, runID, "stdout.log")
}

// StderrFilePath returns the canonical path for the stderr log for the given run.
func (s *Store) StderrFilePath(runID string) string {
	return filepath.Join(s.root, runID, "stderr.log")
}

// OpenRunLogs opens stdout.log and stderr.log in the run artifact directory
// for streaming writes during execution. The caller must close both writers
// when Unity exits. Call PrepareRunDir before calling OpenRunLogs.
func (s *Store) OpenRunLogs(runID string) (stdout, stderr *os.File, err error) {
	stdoutFile, err := os.Create(s.StdoutFilePath(runID))
	if err != nil {
		return nil, nil, fmt.Errorf("creating stdout.log for run %s: %w", runID, err)
	}
	stderrFile, err := os.Create(s.StderrFilePath(runID))
	if err != nil {
		_ = stdoutFile.Close()
		return nil, nil, fmt.Errorf("creating stderr.log for run %s: %w", runID, err)
	}
	return stdoutFile, stderrFile, nil
}

// Deprecated: use OpenRunLogs to stream logs directly to files during execution.
// SaveRawLogs writes stdout and stderr bytes to their respective log files.
// Each write is atomic (temp-file + rename). An empty slice produces an empty file.
func (s *Store) SaveRawLogs(runID string, stdout, stderr []byte) error {
	if err := atomicWrite(s.StdoutFilePath(runID), stdout); err != nil {
		return fmt.Errorf("writing stdout.log for run %s: %w", runID, err)
	}
	if err := atomicWrite(s.StderrFilePath(runID), stderr); err != nil {
		return fmt.Errorf("writing stderr.log for run %s: %w", runID, err)
	}
	return nil
}

// SaveManifest marshals m as indented JSON and writes it to
// <root>/<runID>/manifest.json.
func (s *Store) SaveManifest(runID string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling manifest for run %s: %w", runID, err)
	}
	finalPath := filepath.Join(s.root, runID, "manifest.json")
	if err := atomicWrite(finalPath, data); err != nil {
		return fmt.Errorf("writing manifest.json for run %s: %w", runID, err)
	}
	return nil
}

// atomicWrite writes data to path via a temp file + rename to prevent partial files.
func atomicWrite(finalPath string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(finalPath), "artifact-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// Prune removes the oldest run directories, keeping the most recent `keep`.
// Returns the number of directories removed. Non-existent root returns (0, nil).
func (s *Store) Prune(keep int) (int, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var dirs []string
	for _, e := range entries {
		if e.IsDir() && runIDPattern.MatchString(e.Name()) {
			dirs = append(dirs, e.Name())
		}
	}

	if len(dirs) <= keep {
		return 0, nil
	}

	sort.Strings(dirs)

	toRemove := dirs[:len(dirs)-keep]
	removed := 0
	for _, name := range toRemove {
		dirPath := filepath.Join(s.root, name)
		if err := os.RemoveAll(dirPath); err != nil {
			return removed, fmt.Errorf("pruning artifact dir %s: %w", name, err)
		}
		removed++
	}
	return removed, nil
}

// SaveSummary marshals v as indented JSON and writes it to
// <root>/<runID>/summary.json.
func (s *Store) SaveSummary(runID string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling summary for run %s: %w", runID, err)
	}
	finalPath := filepath.Join(s.root, runID, "summary.json")
	if err := atomicWrite(finalPath, data); err != nil {
		return fmt.Errorf("writing summary.json for run %s: %w", runID, err)
	}
	return nil
}
