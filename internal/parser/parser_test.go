package parser_test

import (
	"os"
	"testing"

	"github.com/fastplay/runner/internal/parser"
)

func TestParse_PassingRun(t *testing.T) {
	data, err := os.ReadFile("testdata/passing.xml")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	result, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("got Total=%d, want 3", result.Total)
	}
	if result.Passed != 3 {
		t.Errorf("got Passed=%d, want 3", result.Passed)
	}
	if result.Failed != 0 {
		t.Errorf("got Failed=%d, want 0", result.Failed)
	}
	if len(result.Tests) != 3 {
		t.Errorf("got %d tests, want 3", len(result.Tests))
	}
}

func TestParse_OneFailure(t *testing.T) {
	data, _ := os.ReadFile("testdata/one_failure.xml")
	result, err := parser.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	failed := result.FailedTests()
	if len(failed) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(failed))
	}
	if failed[0].AbsolutePath == "" {
		t.Error("absolute_path must not be empty for failed test")
	}
	if failed[0].Line == 0 {
		t.Error("line must be non-zero for failed test")
	}
}

func TestParse_ExtractsFileAndLine(t *testing.T) {
	data, _ := os.ReadFile("testdata/one_failure.xml")
	result, _ := parser.Parse(data)
	tc := result.FailedTests()[0]
	if tc.AbsolutePath != "/home/user/MyProject/Assets/Tests/MyTests.cs" {
		t.Errorf("got absolute_path %q", tc.AbsolutePath)
	}
	if tc.Line != 42 {
		t.Errorf("got line %d, want 42", tc.Line)
	}
}

func TestParse_EmptySuite(t *testing.T) {
	data, _ := os.ReadFile("testdata/empty_suite.xml")
	result, err := parser.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 tests, got %d", result.Total)
	}
	if result.Tests == nil {
		t.Error("Tests must be empty slice, not nil")
	}
}

func TestMakeRelative_UnderProjectPath(t *testing.T) {
	rel := parser.MakeRelative("/home/user/proj", "/home/user/proj/Assets/Tests/Foo.cs")
	if rel != "Assets/Tests/Foo.cs" {
		t.Errorf("got %q", rel)
	}
}

func TestMakeRelative_OutsideProjectPath(t *testing.T) {
	rel := parser.MakeRelative("/home/user/proj", "/etc/other/File.cs")
	if rel != "/etc/other/File.cs" {
		t.Errorf("got %q, want absolute path unchanged", rel)
	}
}
