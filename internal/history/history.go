package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/runid"
)

var (
	ErrRunExists    = errors.New("run result already exists")
	ErrRunNotFound  = errors.New("run result not found")
	ErrInvalidRunID = errors.New("invalid run ID format")
)

func validateRunID(id string) error {
	if !runid.IsValid(id) {
		return ErrInvalidRunID
	}
	return nil
}

// RunResult is the persisted result of a single testplay run.
type RunResult struct {
	SchemaVersion string            `json:"schema_version"`
	RunID         string            `json:"run_id"`
	ExitCode      int               `json:"exit_code"`
	TimeoutType   string            `json:"timeout_type,omitempty"`
	Total         int               `json:"total,omitempty"`
	Passed        int               `json:"passed,omitempty"`
	Failed        int               `json:"failed,omitempty"`
	Skipped       int               `json:"skipped,omitempty"`
	Tests         []parser.TestCase `json:"tests"`
	Errors        []CompileError    `json:"errors,omitempty"`
	NewFailures   []parser.TestCase `json:"new_failures"` // null when compare-run not specified
}

// CompileError represents a C# compile error from Unity stderr.
type CompileError struct {
	File         string `json:"file"`
	AbsolutePath string `json:"absolute_path"`
	Line         int    `json:"line"`
	Column       int    `json:"column,omitempty"`
	Message      string `json:"message"`
}

// Store persists RunResult files in a directory.
type Store struct {
	dir string
}

// NewStore creates a Store that reads/writes to dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Save writes result to <dir>/<runID>.json.
// Returns ErrRunExists if the file already exists (never overwrites).
func (s *Store) Save(runID string, result *RunResult) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("creating result dir: %w", err)
	}

	path := filepath.Join(s.dir, runID+".json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%w: %s", ErrRunExists, runID)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// Load reads and parses <dir>/<runID>.json.
// Returns ErrRunNotFound if the file does not exist.
func (s *Store) Load(runID string) (*RunResult, error) {
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	path := filepath.Join(s.dir, runID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
		}
		return nil, err
	}

	var result RunResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Tests == nil {
		result.Tests = []parser.TestCase{}
	}
	return &result, nil
}

// List returns run results sorted newest-first.
// If last > 0, only the last N results are returned.
// If the directory does not exist, returns an empty slice.
func (s *Store) List(last int) ([]*RunResult, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*RunResult{}, nil
		}
		return nil, err
	}

	var ids []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
		}
	}

	// Sort descending (newest first) — run_id format is lexicographically sortable
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))

	if last > 0 && len(ids) > last {
		ids = ids[:last]
	}

	results := make([]*RunResult, 0, len(ids))
	for _, id := range ids {
		r, err := s.Load(id)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

// Prune removes the oldest run result files, keeping the most recent `keep` entries.
// Returns the number of files removed. If the directory has <= keep entries, no files
// are removed. If the directory doesn't exist, returns (0, nil).
func (s *Store) Prune(keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var ids []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			id := strings.TrimSuffix(e.Name(), ".json")
			if runid.IsValid(id) {
				ids = append(ids, id)
			}
		}
	}

	if len(ids) <= keep {
		return 0, nil
	}

	// Sort ascending (oldest first)
	sort.Strings(ids)

	toRemove := ids[:len(ids)-keep]
	removed := 0
	var errs []error
	for _, id := range toRemove {
		path := filepath.Join(s.dir, id+".json")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("pruning run %s: %w", id, err))
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// Compare returns tests that were NOT "Failed" in prev but ARE "Failed" in curr.
// Returns nil (not an empty slice) when prev is nil — signals no --compare-run was specified.
func Compare(prev, curr *RunResult) []parser.TestCase {
	if prev == nil {
		return nil
	}
	if curr == nil {
		return nil
	}

	// Build a set of names that were failing in prev
	prevFailed := make(map[string]bool, len(prev.Tests))
	for _, tc := range prev.Tests {
		if tc.Result == "Failed" {
			prevFailed[tc.Name] = true
		}
	}

	newFails := make([]parser.TestCase, 0)
	for _, tc := range curr.Tests {
		if tc.Result == "Failed" && !prevFailed[tc.Name] {
			newFails = append(newFails, tc)
		}
	}
	return newFails
}
