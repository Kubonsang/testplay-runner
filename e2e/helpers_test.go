//go:build e2e

package e2e_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/config"
)

// unityPath returns the Unity editor path from UNITY_PATH env var.
// Skips the test if not set.
func unityPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("UNITY_PATH")
	if p == "" {
		t.Skip("UNITY_PATH not set — skipping E2E test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("UNITY_PATH %q does not exist: %v", p, err)
	}
	return p
}

// testProjectPath returns the absolute path to testdata/unity-project/.
func testProjectPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	// e2e/helpers_test.go → repo root → testdata/unity-project
	repoRoot := filepath.Dir(filepath.Dir(thisFile))
	p := filepath.Join(repoRoot, "testdata", "unity-project")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("test project not found at %s: %v", p, err)
	}
	return p
}

// buildConfig creates a config.Config pointing at the E2E Unity project.
func buildConfig(t *testing.T, resultDir string) *config.Config {
	t.Helper()
	return &config.Config{
		SchemaVersion: "1",
		UnityPath:     unityPath(t),
		ProjectPath:   testProjectPath(t),
		ResultDir:     resultDir,
		Timeout:       config.Timeouts{TotalMs: 300000},
		TestPlatform:  "edit_mode",
	}
}
