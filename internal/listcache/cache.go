// Package listcache persists the complete test list from a run's NUnit XML output.
// After each successful run (exit 0 or 3), the runner writes the full test names
// to .testplay/cache/list.json. The list command reads this cache first and returns
// complete: true when available, falling back to a static scan with complete: false.
package listcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/parser"
)

// Cache holds the test names discovered during a run.
type Cache struct {
	SchemaVersion string   `json:"schema_version"`
	CachedRunID   string   `json:"cached_run_id"`
	CachedAt      string   `json:"cached_at"`
	Tests         []string `json:"tests"`
}

// CachePath returns the path to the list cache file for a project.
func CachePath(projectPath string) string {
	return filepath.Join(projectPath, ".testplay", "cache", "list.json")
}

// Write extracts test names from tests and atomically writes them to the cache.
func Write(projectPath, runID string, tests []parser.TestCase) error {
	names := make([]string, 0, len(tests))
	for _, tc := range tests {
		if tc.Name != "" {
			names = append(names, tc.Name)
		}
	}

	c := Cache{
		SchemaVersion: "1",
		CachedRunID:   runID,
		CachedAt:      time.Now().UTC().Format(time.RFC3339),
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
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Read loads the cache from disk. Returns an error if the cache does not exist
// or cannot be parsed.
func Read(projectPath string) (*Cache, error) {
	data, err := os.ReadFile(CachePath(projectPath))
	if err != nil {
		return nil, err
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
