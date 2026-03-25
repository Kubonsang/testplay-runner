package unity_test

import (
	"testing"

	"github.com/fastplay/runner/internal/unity"
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
