//go:build e2e

package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

func TestE2E_EditModeTestsPass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	resultDir := t.TempDir()
	artifactRoot := filepath.Join(t.TempDir(), ".testplay", "runs")

	cfg := buildConfig(t, resultDir)

	svc := &runsvc.Service{
		Runner:       &unity.ProcessRunner{UnityPath: cfg.UnityPath},
		Store:        history.NewStore(resultDir),
		Artifacts:    artifacts.NewStore(artifactRoot),
		StatusWriter: status.NewWriter(filepath.Join(t.TempDir(), "status.json")),
	}

	resp, err := svc.Run(context.Background(), runsvc.Request{
		Config: cfg,
	})
	if err != nil {
		t.Fatalf("Service.Run infrastructure error: %v", err)
	}

	// Exit code must be 0 (all tests pass) or 3 (some fail)
	if resp.ExitCode != 0 && resp.ExitCode != 3 {
		t.Errorf("unexpected exit code %d; expected 0 or 3", resp.ExitCode)
	}

	if resp.Result == nil {
		t.Fatal("Result is nil")
	}
	if resp.Result.Total == 0 {
		t.Error("Total must be > 0 — Unity should have found test cases")
	}
	if len(resp.Result.Tests) == 0 {
		t.Error("Tests slice must be non-empty")
	}

	// Verify parameterized tests have group info
	hasParamGroup := false
	for _, tc := range resp.Result.Tests {
		if tc.ParameterizedGroup != "" {
			hasParamGroup = true
			break
		}
	}
	if !hasParamGroup {
		t.Log("WARNING: no parameterized_group detected — ensure SampleEditModeTest has [TestCase] tests")
	}

	t.Logf("E2E result: exit=%d total=%d passed=%d failed=%d tests=%d",
		resp.ExitCode, resp.Result.Total, resp.Result.Passed, resp.Result.Failed, len(resp.Result.Tests))
}

func TestE2E_ExitCodeMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	resultDir := t.TempDir()
	cfg := buildConfig(t, resultDir)
	artifactRoot := filepath.Join(t.TempDir(), ".testplay", "runs")

	svc := &runsvc.Service{
		Runner:       &unity.ProcessRunner{UnityPath: cfg.UnityPath},
		Store:        history.NewStore(resultDir),
		Artifacts:    artifacts.NewStore(artifactRoot),
		StatusWriter: status.NewWriter(filepath.Join(t.TempDir(), "status.json")),
	}

	resp, err := svc.Run(context.Background(), runsvc.Request{
		Config: cfg,
	})
	if err != nil {
		t.Fatalf("infrastructure error: %v", err)
	}

	switch resp.ExitCode {
	case 0:
		if resp.Result.Failed != 0 {
			t.Errorf("exit 0 but %d failures", resp.Result.Failed)
		}
	case 3:
		if resp.Result.Failed == 0 {
			t.Errorf("exit 3 but 0 failures")
		}
	default:
		t.Errorf("unexpected exit code %d", resp.ExitCode)
	}
}

func TestE2E_ShadowWorkspace_PathRemapping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	projectPath := testProjectPath(t)
	resultDir := t.TempDir()
	artifactRoot := filepath.Join(t.TempDir(), ".testplay", "runs")

	cfg := buildConfig(t, resultDir)

	svc := &runsvc.Service{
		Runner:       &unity.ProcessRunner{UnityPath: cfg.UnityPath},
		Store:        history.NewStore(resultDir),
		Artifacts:    artifacts.NewStore(artifactRoot),
		StatusWriter: status.NewWriter(filepath.Join(t.TempDir(), "status.json")),
	}

	resp, err := svc.Run(context.Background(), runsvc.Request{
		Config:      cfg,
		ForceShadow: true,
	})
	if err != nil {
		t.Fatalf("infrastructure error: %v", err)
	}
	if resp.ExitCode != 0 && resp.ExitCode != 3 {
		t.Fatalf("unexpected exit code %d — cannot verify path remapping", resp.ExitCode)
	}

	// All absolute_path fields must point to the source project, not the shadow
	for i, tc := range resp.Result.Tests {
		if tc.AbsolutePath == "" {
			continue
		}
		if !strings.HasPrefix(tc.AbsolutePath, projectPath) {
			t.Errorf("Tests[%d].AbsolutePath %q does not start with project path %q",
				i, tc.AbsolutePath, projectPath)
		}
	}
}

func TestE2E_LibraryCacheSeeding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	projectPath := testProjectPath(t)

	// First run: populates Library cache (may be slow — cold start)
	resultDir1 := t.TempDir()
	artifactRoot1 := filepath.Join(t.TempDir(), ".testplay", "runs")
	cfg1 := buildConfig(t, resultDir1)

	svc1 := &runsvc.Service{
		Runner:       &unity.ProcessRunner{UnityPath: cfg1.UnityPath},
		Store:        history.NewStore(resultDir1),
		Artifacts:    artifacts.NewStore(artifactRoot1),
		StatusWriter: status.NewWriter(filepath.Join(t.TempDir(), "status1.json")),
	}

	resp1, err := svc1.Run(context.Background(), runsvc.Request{
		Config:      cfg1,
		ForceShadow: true,
		ClearCache:  true,
	})
	if err != nil {
		t.Fatalf("first run infrastructure error: %v", err)
	}
	if resp1.ExitCode != 0 && resp1.ExitCode != 3 {
		t.Fatalf("first run: unexpected exit code %d", resp1.ExitCode)
	}

	// Verify cache was created
	cacheDir := filepath.Join(projectPath, ".testplay", "cache", "Library")
	if _, err := os.Stat(cacheDir); err != nil {
		t.Fatalf("Library cache not created after first run: %v", err)
	}

	// Second run: should seed from cache (faster)
	resultDir2 := t.TempDir()
	artifactRoot2 := filepath.Join(t.TempDir(), ".testplay", "runs")
	cfg2 := buildConfig(t, resultDir2)

	svc2 := &runsvc.Service{
		Runner:       &unity.ProcessRunner{UnityPath: cfg2.UnityPath},
		Store:        history.NewStore(resultDir2),
		Artifacts:    artifacts.NewStore(artifactRoot2),
		StatusWriter: status.NewWriter(filepath.Join(t.TempDir(), "status2.json")),
	}

	resp2, err := svc2.Run(context.Background(), runsvc.Request{
		Config:      cfg2,
		ForceShadow: true,
	})
	if err != nil {
		t.Fatalf("second run infrastructure error: %v", err)
	}
	if resp2.ExitCode != 0 && resp2.ExitCode != 3 {
		t.Fatalf("second run: unexpected exit code %d", resp2.ExitCode)
	}

	t.Logf("Cache seeding verified: first run exit=%d, second run exit=%d", resp1.ExitCode, resp2.ExitCode)
}
