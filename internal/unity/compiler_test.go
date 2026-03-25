package unity_test

import (
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/unity"
)

func TestParseCompileErrors_BasicError(t *testing.T) {
	stderr := []byte(`Assets/Scripts/Foo.cs(42,10): error CS0246: The type 'Bar' could not be found`)
	errs := unity.ParseCompileErrors(stderr)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].File != "Assets/Scripts/Foo.cs" {
		t.Errorf("got file %q", errs[0].File)
	}
	if errs[0].Line != 42 {
		t.Errorf("got line %d, want 42", errs[0].Line)
	}
	if errs[0].Message == "" {
		t.Error("message must not be empty")
	}
}

func TestParseCompileErrors_MultipleErrors(t *testing.T) {
	stderr := []byte("Assets/A.cs(1,1): error CS0001: first error\nAssets/B.cs(2,2): error CS0002: second error")
	errs := unity.ParseCompileErrors(stderr)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(errs))
	}
}

func TestParseCompileErrors_Empty(t *testing.T) {
	errs := unity.ParseCompileErrors([]byte(""))
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d", len(errs))
	}
}

func TestParseCompileErrors_AbsolutePath(t *testing.T) {
	stderr := []byte(`Assets/A.cs(10,1): error CS0001: msg`)
	projectPath := "/home/user/MyProject"
	errs := unity.ParseCompileErrorsWithProject(stderr, projectPath)
	expected := "/home/user/MyProject/Assets/A.cs"
	if len(errs) == 0 {
		t.Fatal("expected 1 error")
	}
	if errs[0].AbsolutePath != expected {
		t.Errorf("got %q, want %q", errs[0].AbsolutePath, expected)
	}
}

func TestParseCompileErrors_NonErrorLinesIgnored(t *testing.T) {
	stderr := []byte("Refreshing native plugins compatible for Editor...\nAssets/A.cs(5,1): error CS0001: oops\nDone.")
	errs := unity.ParseCompileErrors(stderr)
	if len(errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(errs))
	}
}
