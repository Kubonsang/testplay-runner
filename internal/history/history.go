package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Kubonsang/testplay-runner/internal/parser"
)

var (
	ErrRunExists   = errors.New("run result already exists")
	ErrRunNotFound = errors.New("run result not found")
	ErrInvalidRunID = errors.New("invalid run ID format")
)

var runIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}$`)

func validateRunID(runID string) error {
	if !runIDPattern.MatchString(runID) {
		return ErrInvalidRunID
	}
	return nil
}

// RunResult is the persisted result of a single fastplay run.
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

	// Ensure Tests is never null in JSON
	if result.Tests == nil {
		result.Tests = make([]parser.TestCase, 0)
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
		result.Tests = make([]parser.TestCase, 0)
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
