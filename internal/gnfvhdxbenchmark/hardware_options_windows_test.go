//go:build windows

package gnfvhdxbenchmark

import (
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
