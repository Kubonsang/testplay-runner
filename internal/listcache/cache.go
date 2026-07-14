// Package listcache persists the complete test list from a run's NUnit XML output.
// After each successful run (exit 0 or 3), the runner writes the full test names
// to .testplay/cache/list.json. The list command reads this cache first and returns
// complete: true when available, falling back to a static scan with complete: false.
package listcache

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
	"github.com/Kubonsang/testplay-runner/internal/parser"
)

const SchemaVersion = "2"

var (
	ErrInvalidCache     = errors.New("invalid list cache")
	ErrStaleCache       = errors.New("stale list cache")
	ErrIncompleteCache  = errors.New("incomplete list cache")
	ErrPlatformMismatch = errors.New("list cache test platform mismatch")
)

// Metadata records the facts needed before a cached test list may be called
// complete. In particular, a filtered/category run is never a full inventory,
// and edit-mode and play-mode inventories are not interchangeable.
type Metadata struct {
	FullInventory bool
	TestPlatform  string
}

// Cache holds the test names discovered during a run.
type Cache struct {
	SchemaVersion string   `json:"schema_version"`
	CachedRunID   string   `json:"cached_run_id"`
	CachedAt      string   `json:"cached_at"`
	FullInventory bool     `json:"full_inventory"`
	TestPlatform  string   `json:"test_platform"`
	Tests         []string `json:"tests"`
}

// CachePath returns the path to the list cache file for a project.
func CachePath(projectPath string) string {
	return filepath.Join(projectPath, ".testplay", "cache", "list.json")
}

// Write extracts test names from tests and atomically writes them to the cache.
// Callers must state whether the run was unfiltered and which Unity test
// platform produced it; omitting those facts was the schema-1 cache poisoning
// bug, so there is intentionally no implicit default.
func Write(projectPath, runID string, tests []parser.TestCase, metadata Metadata) error {
	if runID == "" {
		return fmt.Errorf("%w: cached_run_id is required", ErrInvalidCache)
	}
	if !validTestPlatform(metadata.TestPlatform) {
		return fmt.Errorf("%w: test_platform must be \"edit_mode\" or \"play_mode\"", ErrInvalidCache)
	}

	names := make([]string, 0, len(tests))
	for _, tc := range tests {
		if tc.Name != "" {
			names = append(names, tc.Name)
		}
	}

	c := Cache{
		SchemaVersion: SchemaVersion,
		CachedRunID:   runID,
		CachedAt:      time.Now().UTC().Format(time.RFC3339),
		FullInventory: metadata.FullInventory,
		TestPlatform:  metadata.TestPlatform,
		Tests:         names,
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	dest := CachePath(projectPath)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := atomicfile.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Read loads a trustworthy complete-inventory cache from disk. Schema-1 caches
// are deliberately stale: they did not record whether a filter/category was
// active and therefore cannot substantiate complete:true.
func Read(projectPath string) (*Cache, error) {
	data, err := os.ReadFile(CachePath(projectPath))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Cache
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCache, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, fmt.Errorf("%w: trailing data: %v", ErrInvalidCache, err)
	}
	if c.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %q is not %q", ErrStaleCache, c.SchemaVersion, SchemaVersion)
	}
	if c.CachedRunID == "" {
		return nil, fmt.Errorf("%w: cached_run_id is required", ErrInvalidCache)
	}
	if _, err := time.Parse(time.RFC3339, c.CachedAt); err != nil {
		return nil, fmt.Errorf("%w: cached_at must be RFC3339: %v", ErrInvalidCache, err)
	}
	if !c.FullInventory {
		return nil, fmt.Errorf("%w: full_inventory is false", ErrIncompleteCache)
	}
	if !validTestPlatform(c.TestPlatform) {
		return nil, fmt.Errorf("%w: invalid test_platform %q", ErrInvalidCache, c.TestPlatform)
	}
	if c.Tests == nil {
		c.Tests = make([]string, 0)
	}
	return &c, nil
}

// ReadForPlatform additionally proves that the inventory belongs to the test
// platform requested by the current config.
func ReadForPlatform(projectPath, testPlatform string) (*Cache, error) {
	if !validTestPlatform(testPlatform) {
		return nil, fmt.Errorf("%w: requested platform %q", ErrPlatformMismatch, testPlatform)
	}
	c, err := Read(projectPath)
	if err != nil {
		return nil, err
	}
	if c.TestPlatform != testPlatform {
		return nil, fmt.Errorf("%w: cache is %q, requested %q", ErrPlatformMismatch, c.TestPlatform, testPlatform)
	}
	return c, nil
}

func validTestPlatform(platform string) bool {
	return platform == "edit_mode" || platform == "play_mode"
}
