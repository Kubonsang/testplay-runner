package unity_test

import (
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/unity"
)

func TestBuildRunArgs_AlwaysIncludesRunTestsAndPlatform(t *testing.T) {
	args := unity.BuildRunArgs("/proj", &unity.RunOptions{})
	if !contains(args, "-runTests") {
		t.Error("expected -runTests in args")
	}
	if !contains(args, "-testPlatform") {
		t.Error("expected -testPlatform in args")
	}
	if !contains(args, "-projectPath") {
		t.Error("expected -projectPath in args")
	}
}

func TestBuildRunArgs_WithFilter(t *testing.T) {
	args := unity.BuildRunArgs("/proj", &unity.RunOptions{Filter: "MyTest.Foo"})
	idx := indexOf(args, "-testFilter")
	if idx == -1 {
		t.Fatal("expected -testFilter in args")
	}
	if idx+1 >= len(args) || args[idx+1] != "MyTest.Foo" {
		t.Error("-testFilter value not set correctly")
	}
}

func TestBuildRunArgs_WithCategory(t *testing.T) {
	args := unity.BuildRunArgs("/proj", &unity.RunOptions{Category: "Performance"})
	idx := indexOf(args, "-testCategory")
	if idx == -1 {
		t.Fatal("expected -testCategory in args")
	}
	if idx+1 >= len(args) || args[idx+1] != "Performance" {
		t.Error("-testCategory value not set correctly")
	}
}

func TestBuildRunArgs_ResultsFileInArgs(t *testing.T) {
	args := unity.BuildRunArgs("/proj", &unity.RunOptions{ResultsFilePath: "/tmp/r.xml"})
	if !contains(args, "/tmp/r.xml") {
		t.Error("expected results file path in args")
	}
}

func TestBuildRunArgs_NoFilterOrCategory_NoExtraArgs(t *testing.T) {
	args := unity.BuildRunArgs("/proj", &unity.RunOptions{})
	for _, a := range args {
		if a == "-testFilter" {
			t.Error("unexpected -testFilter in args when no filter set")
		}
		if a == "-testCategory" {
			t.Error("unexpected -testCategory in args when no category set")
		}
	}
}

// helpers for this test file
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

func indexOf(slice []string, val string) int {
	for i, s := range slice {
		if s == val {
			return i
		}
	}
	return -1
}

func TestBuildRunArgs_PlayMode_UsesPlayModeArg(t *testing.T) {
	args := unity.BuildRunArgs("/proj", &unity.RunOptions{TestPlatform: "play_mode"})
	idx := indexOf(args, "-testPlatform")
	if idx == -1 || idx+1 >= len(args) {
		t.Fatal("-testPlatform arg missing")
	}
	if args[idx+1] != "PlayMode" {
		t.Errorf("expected PlayMode, got %q", args[idx+1])
	}
}

func TestBuildRunArgs_EditMode_UsesEditModeArg(t *testing.T) {
	args := unity.BuildRunArgs("/proj", &unity.RunOptions{TestPlatform: "edit_mode"})
	idx := indexOf(args, "-testPlatform")
	if idx == -1 || idx+1 >= len(args) {
		t.Fatal("-testPlatform arg missing")
	}
	if args[idx+1] != "EditMode" {
		t.Errorf("expected EditMode, got %q", args[idx+1])
	}
}

// TestBuildCompileArgs_NoRunTests verifies the compile-only arg set:
// must include -batchmode, -nographics, -projectPath, -quit
// and must NOT include -runTests or -testPlatform.
func TestBuildCompileArgs_NoRunTests(t *testing.T) {
	args := unity.BuildCompileArgs("/proj")
	if contains(args, "-runTests") {
		t.Error("-runTests must not appear in compile-only args")
	}
	if contains(args, "-testPlatform") {
		t.Error("-testPlatform must not appear in compile-only args")
	}
	if !contains(args, "-batchmode") {
		t.Error("expected -batchmode in compile args")
	}
	if !contains(args, "-nographics") {
		t.Error("expected -nographics in compile args")
	}
	if !contains(args, "-quit") {
		t.Error("expected -quit in compile args")
	}
	if !contains(args, "/proj") {
		t.Error("expected project path in compile args")
	}
}

func TestBuildRunArgs_EmptyTestPlatform_DefaultsToEditMode(t *testing.T) {
	// Callers that don't set TestPlatform must still get EditMode (backward compat)
	args := unity.BuildRunArgs("/proj", &unity.RunOptions{})
	idx := indexOf(args, "-testPlatform")
	if idx == -1 || idx+1 >= len(args) {
		t.Fatal("-testPlatform arg missing")
	}
	if args[idx+1] != "EditMode" {
		t.Errorf("expected EditMode default, got %q", args[idx+1])
	}
}
