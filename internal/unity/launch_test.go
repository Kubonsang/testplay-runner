package unity_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/unity"
)

func TestExecute_MissingUnityBinary_Returns1WithHint(t *testing.T) {
	dir := t.TempDir()
	r := &unity.ProcessRunner{UnityPath: filepath.Join(dir, "no-such-unity")}
	spy := &spyWriter{}

	result, code := unity.Execute(context.Background(), r, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
	})
	if code != 1 {
		t.Fatalf("expected exit 1 (dependency error), got %d", code)
	}
	if result.Error == "" {
		t.Error("expected error message describing the launch failure")
	}
	if result.Hint == "" {
		t.Error("expected hint for agent recovery on exit 1")
	}
}

func TestExecute_UnityPathIsDirectory_Returns1WithHint(t *testing.T) {
	dir := t.TempDir()
	appBundle := filepath.Join(dir, "Unity.app")
	if err := os.MkdirAll(appBundle, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &unity.ProcessRunner{UnityPath: appBundle}

	result, code := unity.Execute(context.Background(), r, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: &spyWriter{},
	})
	if code != 1 {
		t.Fatalf("expected exit 1 for directory unity_path (.app bundle), got %d", code)
	}
	if result.Hint == "" {
		t.Error("expected hint pointing at the executable-inside-bundle path")
	}
}

func TestExecute_TwoPhase_MissingUnityBinary_Returns1(t *testing.T) {
	dir := t.TempDir()
	r := &unity.ProcessRunner{UnityPath: filepath.Join(dir, "no-such-unity")}

	result, code := unity.Execute(context.Background(), r, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: &spyWriter{},
		CompileMs:    60000,
		TestMs:       60000,
	})
	if code != 1 {
		t.Fatalf("expected exit 1 (dependency error) in two-phase mode, got %d", code)
	}
	if result.Hint == "" {
		t.Error("expected hint for agent recovery on exit 1")
	}
}
