//go:build windows

package gnfvhdxbenchmark

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyPrepareOptionsCopyPackagesForSourceIsolation(t *testing.T) {
	root := t.TempDir()
	opts := legacyPrepareOptions(
		filepath.Join(root, "source-project"),
		filepath.Join(root, "legacy-cache"),
	)
	if !opts.CopyPackages {
		t.Fatal("Legacy benchmark must physically copy Packages so Unity cannot mutate source-project through a link")
	}
}

func TestEnsureChildDirectoryCreatesOnlyParent(t *testing.T) {
	childPath := filepath.Join(t.TempDir(), "storage", "children", "run.vhdx")
	if err := ensureChildDirectory(childPath); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(childPath))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("Child parent must be a directory")
	}
	if _, err = os.Lstat(childPath); !os.IsNotExist(err) {
		t.Fatalf("Harness must not create the Child VHDX; Lstat error=%v", err)
	}
}
