package parser

import "testing"

func TestExtractFileAndLine_UnixPath(t *testing.T) {
	trace := "at MyTests.TestSub () [0x00000] in /home/user/project/MyTests.cs:line 42"
	path, line := extractFileAndLine(trace)
	if path != "/home/user/project/MyTests.cs" {
		t.Errorf("got path %q", path)
	}
	if line != 42 {
		t.Errorf("got line %d, want 42", line)
	}
}

func TestExtractFileAndLine_WindowsPath(t *testing.T) {
	trace := `at MyTests.TestSub () [0x00000] in C:\Users\user\MyTests.cs:line 15`
	path, line := extractFileAndLine(trace)
	if path != `C:\Users\user\MyTests.cs` {
		t.Errorf("got path %q", path)
	}
	if line != 15 {
		t.Errorf("got line %d, want 15", line)
	}
}

func TestExtractFileAndLine_NoMatch(t *testing.T) {
	path, line := extractFileAndLine("some other stack trace without file info")
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
	if line != 0 {
		t.Errorf("expected line 0, got %d", line)
	}
}

func TestExtractFileAndLine_MultilineTrace(t *testing.T) {
	trace := "at A () [0x00000] in /a.cs:line 1\nat B () [0x00000] in /b.cs:line 2"
	path, line := extractFileAndLine(trace)
	// Should return the first (innermost) frame
	if path != "/a.cs" {
		t.Errorf("expected first frame /a.cs, got %q", path)
	}
	if line != 1 {
		t.Errorf("expected line 1, got %d", line)
	}
}
