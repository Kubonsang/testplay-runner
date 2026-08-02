//go:build e2e

package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

// TestE2E_LibraryImageParity is the real-Unity gate for the experimental
// backend. It compares the stable result contract and source-tree bytes for the
// same fixture while deliberately ignoring run IDs, timings, and temp paths.
func TestE2E_LibraryImageParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E parity test in short mode")
	}
	unity := unityPath(t)
	project := filepath.Join(t.TempDir(), "unity-project")
	if err := shadow.CopyDirParallel(context.Background(), testProjectPath(t), project, 0); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     unity,
		ProjectPath:   project,
		ResultDir:     filepath.Join(project, ".testplay", "results"),
		Timeout:       config.Timeouts{TotalMs: 300000},
		TestPlatform:  "edit_mode",
	}
	legacy := runService(t, cfg, runsvc.Request{
		Config:           cfg,
		WorkspaceBackend: runsvc.WorkspaceBackendLegacy,
	})
	// Legacy links Packages and may let Unity create packages-lock.json in the
	// source. Preserve that established behavior, then prove the image backend
	// causes no additional source mutation.
	beforeImage := snapshotProjectInputs(t, project)
	image := runService(t, cfg, runsvc.Request{
		Config:           cfg,
		WorkspaceBackend: runsvc.WorkspaceBackendImage,
	})

	if legacy.ExitCode != image.ExitCode {
		t.Fatalf("exit mismatch: legacy=%d image=%d", legacy.ExitCode, image.ExitCode)
	}
	if legacy.Result.Total != image.Result.Total ||
		legacy.Result.Passed != image.Result.Passed ||
		legacy.Result.Failed != image.Result.Failed ||
		legacy.Result.Skipped != image.Result.Skipped {
		t.Fatalf("count mismatch: legacy=%+v image=%+v", legacy.Result, image.Result)
	}
	assertSameTests(t, legacy.Result.Tests, image.Result.Tests)
	assertSameErrors(t, legacy.Result.Errors, image.Result.Errors)
	if image.Result.WorkspaceMetrics == nil ||
		image.Result.WorkspaceMetrics.WorkspaceBackend != runsvc.WorkspaceBackendImage ||
		image.Result.WorkspaceMetrics.ImageStatus != "valid" {
		t.Fatalf("image metrics invalid: %+v", image.Result.WorkspaceMetrics)
	}

	afterImage := snapshotProjectInputs(t, project)
	if !reflect.DeepEqual(beforeImage, afterImage) {
		t.Fatal("Assets, Packages, or ProjectSettings changed during the image run")
	}
}

func snapshotProjectInputs(t *testing.T, project string) map[string][]byte {
	t.Helper()
	snapshot := make(map[string][]byte)
	for _, root := range []string{"Assets", "Packages", "ProjectSettings"} {
		base := filepath.Join(project, root)
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(project, path)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[filepath.ToSlash(rel)] = data
			return nil
		})
		if err != nil {
			t.Fatalf("snapshot %s: %v", root, err)
		}
	}
	return snapshot
}
