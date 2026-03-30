package shadow_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

func makeProject(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	for _, d := range []string{"Assets/Scripts", "ProjectSettings", "Packages"} {
		_ = os.MkdirAll(filepath.Join(src, d), 0755)
	}
	_ = os.WriteFile(filepath.Join(src, "Assets", "Scripts", "Player.cs"), []byte("// test"), 0644)
	_ = os.WriteFile(filepath.Join(src, "ProjectSettings", "ProjectVersion.txt"), []byte("m_EditorVersion: 6000.3.8f1"), 0644)
	return src
}

func TestPrepare_CreatesShadowStructure(t *testing.T) {
	src := makeProject(t)
	ws, err := shadow.Prepare(src)
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	for _, rel := range []string{
		"Assets/Scripts/Player.cs",
		"ProjectSettings/ProjectVersion.txt",
	} {
		if _, err := os.Stat(filepath.Join(ws.ShadowPath, rel)); err != nil {
			t.Errorf("expected %s in shadow: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(ws.ShadowPath, "Library")); err != nil {
		t.Error("Library/ not created in shadow")
	}
}

func TestPrepare_ShadowPathUnderSource(t *testing.T) {
	src := makeProject(t)
	ws, err := shadow.Prepare(src)
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	want := filepath.Join(src, ".fastplay-shadow")
	if ws.ShadowPath != want {
		t.Errorf("ShadowPath: got %q, want %q", ws.ShadowPath, want)
	}
}

func TestPrepare_IsIdempotent(t *testing.T) {
	src := makeProject(t)
	ws, err := shadow.Prepare(src)
	if err != nil {
		t.Fatalf("first Prepare failed: %v", err)
	}
	// Write a sentinel to Library/ to verify it is preserved
	sentinel := filepath.Join(ws.ShadowPath, "Library", "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("preserved"), 0644); err != nil {
		t.Fatalf("could not write sentinel: %v", err)
	}
	// Second prepare should succeed and preserve Library/
	if _, err := shadow.Prepare(src); err != nil {
		t.Fatalf("second Prepare failed: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Error("Library/ sentinel was deleted by second Prepare — Library cache must be preserved")
	}
}

func TestPrepare_ReflectsChangedSources(t *testing.T) {
	src := makeProject(t)
	if _, err := shadow.Prepare(src); err != nil {
		t.Fatalf("first Prepare failed: %v", err)
	}
	_ = os.WriteFile(filepath.Join(src, "Assets", "Scripts", "Player.cs"), []byte("// updated"), 0644)
	ws, err := shadow.Prepare(src)
	if err != nil {
		t.Fatalf("second Prepare failed: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(ws.ShadowPath, "Assets", "Scripts", "Player.cs"))
	if string(data) != "// updated" {
		t.Error("re-prepare did not sync updated source file")
	}
}

func TestReset_DeletesLibraryCache(t *testing.T) {
	src := makeProject(t)
	ws, err := shadow.Prepare(src)
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	libFile := filepath.Join(ws.ShadowPath, "Library", "cached.data")
	_ = os.WriteFile(libFile, []byte("stale"), 0644)

	if _, err := shadow.Reset(src); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}
	if _, err := os.Stat(libFile); err == nil {
		t.Error("Library cache was not deleted by Reset")
	}
}

func TestRemapPaths_TestAndErrorPaths(t *testing.T) {
	src := t.TempDir()
	ws := &shadow.Workspace{
		SourcePath: src,
		ShadowPath: filepath.Join(src, ".fastplay-shadow"),
	}
	result := &history.RunResult{
		Tests: []parser.TestCase{
			{AbsolutePath: filepath.Join(ws.ShadowPath, "Assets", "Tests", "Player.cs")},
		},
		Errors: []history.CompileError{
			{AbsolutePath: filepath.Join(ws.ShadowPath, "Assets", "Scripts", "Bad.cs")},
		},
	}
	ws.RemapPaths(result)

	wantTest := filepath.Join(src, "Assets", "Tests", "Player.cs")
	if result.Tests[0].AbsolutePath != wantTest {
		t.Errorf("test path: got %q, want %q", result.Tests[0].AbsolutePath, wantTest)
	}
	wantErr := filepath.Join(src, "Assets", "Scripts", "Bad.cs")
	if result.Errors[0].AbsolutePath != wantErr {
		t.Errorf("error path: got %q, want %q", result.Errors[0].AbsolutePath, wantErr)
	}
}

func TestRemapPaths_NoopWhenNoShadowPaths(t *testing.T) {
	src := t.TempDir()
	ws := &shadow.Workspace{SourcePath: src, ShadowPath: filepath.Join(src, ".fastplay-shadow")}
	result := &history.RunResult{
		Tests: []parser.TestCase{
			{AbsolutePath: filepath.Join(src, "Assets", "Tests", "Player.cs")},
		},
	}
	ws.RemapPaths(result)
	if result.Tests[0].AbsolutePath != filepath.Join(src, "Assets", "Tests", "Player.cs") {
		t.Error("path was unexpectedly modified")
	}
}

func TestRemapPaths_NewFailurePaths(t *testing.T) {
	src := t.TempDir()
	ws := &shadow.Workspace{
		SourcePath: src,
		ShadowPath: filepath.Join(src, ".fastplay-shadow"),
	}
	result := &history.RunResult{
		NewFailures: []parser.TestCase{
			{AbsolutePath: filepath.Join(ws.ShadowPath, "Assets", "Tests", "NewTest.cs")},
		},
	}
	ws.RemapPaths(result)
	want := filepath.Join(src, "Assets", "Tests", "NewTest.cs")
	if result.NewFailures[0].AbsolutePath != want {
		t.Errorf("new_failures path: got %q, want %q", result.NewFailures[0].AbsolutePath, want)
	}
}

// TestRemapPaths_ForwardSlashMixedCase simulates the Windows scenario where
// Unity logs emit forward slashes and a lowercase drive letter while
// Workspace.ShadowPath uses backslashes and an uppercase drive letter.
// remapAbsPath must match case-insensitively after ToSlash normalisation.
func TestRemapPaths_ForwardSlashMixedCase(t *testing.T) {
	ws := &shadow.Workspace{
		// Simulate filepath.Abs output on Windows: backslashes, uppercase drive.
		SourcePath: `C:\MyProject`,
		ShadowPath: `C:\MyProject\.fastplay-shadow`,
	}
	// Unity log path: forward slashes, lowercase drive letter.
	unityPath := `c:/myproject/.fastplay-shadow/Assets/Tests/Foo.cs`
	result := &history.RunResult{
		Tests: []parser.TestCase{{AbsolutePath: unityPath}},
	}
	ws.RemapPaths(result)
	got := result.Tests[0].AbsolutePath
	// After remap the shadow prefix must be replaced with the source prefix.
	// The result uses forward slashes (ToSlash of SourcePath).
	want := `C:/MyProject/Assets/Tests/Foo.cs`
	if got != want {
		t.Errorf("RemapPaths mixed-case forward-slash: got %q, want %q", got, want)
	}
}
