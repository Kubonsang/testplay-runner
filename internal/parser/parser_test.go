package parser_test

import (
	"os"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/parser"
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

func TestParse_WithSkipped(t *testing.T) {
	data, err := os.ReadFile("testdata/with_skipped.xml")
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
	if result.Passed != 1 {
		t.Errorf("got Passed=%d, want 1", result.Passed)
	}
	if result.Failed != 1 {
		t.Errorf("got Failed=%d, want 1", result.Failed)
	}
	if result.Skipped != 1 {
		t.Errorf("got Skipped=%d, want 1", result.Skipped)
	}
	failed := result.FailedTests()
	if len(failed) != 1 {
		t.Errorf("got %d failed tests, want 1", len(failed))
	}
}

func TestParse_WindowsPath(t *testing.T) {
	data, err := os.ReadFile("testdata/windows_failure.xml")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	result, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result.FailedTests()[0]
	if tc.AbsolutePath != `C:\Users\user\MyProject\Assets\Tests\MyTests.cs` {
		t.Errorf("got absolute_path %q", tc.AbsolutePath)
	}
	if tc.Line != 55 {
		t.Errorf("got line %d, want 55", tc.Line)
	}
}

func TestParse_EditorStyleStackTrace(t *testing.T) {
	data, err := os.ReadFile("testdata/editor_style_failure.xml")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	result, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result.FailedTests()[0]
	if tc.AbsolutePath != "/home/user/MyProject/Assets/Tests/MyTests.cs" {
		t.Errorf("got absolute_path %q", tc.AbsolutePath)
	}
	if tc.Line != 77 {
		t.Errorf("got line %d, want 77", tc.Line)
	}
}

func TestParse_NoMatchDegraces(t *testing.T) {
	// A failure with a stack trace that matches no known format should degrade
	// gracefully: AbsolutePath and Line are zero-valued, not an error.
	raw := []byte(`<?xml version="1.0" encoding="utf-8"?>
<test-run id="1" result="Failed" total="1" passed="0" failed="1" skipped="0" duration="0.1">
  <test-suite type="Assembly" name="Foo.dll" result="Failed">
    <test-case id="1" name="A.B" fullname="A.B" result="Failed" duration="0.1">
      <failure>
        <message>oops</message>
        <stack-trace>no file info here at all</stack-trace>
      </failure>
    </test-case>
  </test-suite>
</test-run>`)
	result, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result.FailedTests()[0]
	if tc.AbsolutePath != "" {
		t.Errorf("expected empty absolute_path, got %q", tc.AbsolutePath)
	}
	if tc.Line != 0 {
		t.Errorf("expected line 0, got %d", tc.Line)
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
