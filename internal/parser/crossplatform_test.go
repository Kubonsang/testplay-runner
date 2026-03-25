package parser

import (
	"strings"
	"testing"
)

func TestExtractFileAndLine_WindowsPathOnUnixHost(t *testing.T) {
	// Even on macOS/Linux, must handle Windows-style paths in XML
	trace := `at MyTest.TestFoo () [0x00000] in C:\Users\dev\project\Assets\MyTest.cs:line 99`
	path, line := extractFileAndLine(trace)
	if !strings.Contains(path, "MyTest.cs") {
		t.Errorf("failed to extract Windows path: got %q", path)
	}
	if line != 99 {
		t.Errorf("got line %d, want 99", line)
	}
}

func TestExtractFileAndLine_WindowsPathWithSpaces(t *testing.T) {
	trace := `at MyTest.TestFoo () [0x00000] in C:\My Project\Assets\My Test.cs:line 5`
	path, line := extractFileAndLine(trace)
	if !strings.Contains(path, "My Test.cs") {
		t.Errorf("expected 'My Test.cs' in path, got %q", path)
	}
	if line != 5 {
		t.Errorf("got line %d, want 5", line)
	}
}

func TestMakeRelative_WindowsStyleAbsPath(t *testing.T) {
	// On Unix, Windows paths are not "under" the project path, so return unchanged
	rel := MakeRelative("/home/user/proj", `C:\Users\dev\project\Assets\Foo.cs`)
	// Should return the original path unchanged (can't relativize Windows path from Unix project)
	if rel == "" {
		t.Error("expected non-empty result")
	}
}
