package shadow_test

import (
	"context"
	"errors"
	"fmt"
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
	ws, err := shadow.Prepare(context.Background(), src)
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
	ws, err := shadow.Prepare(context.Background(), src)
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
	ws, err := shadow.Prepare(context.Background(), src)
	if err != nil {
		t.Fatalf("first Prepare failed: %v", err)
	}
	// Write a sentinel to Library/ to verify it is preserved
	sentinel := filepath.Join(ws.ShadowPath, "Library", "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("preserved"), 0644); err != nil {
		t.Fatalf("could not write sentinel: %v", err)
	}
	// Second prepare should succeed and preserve Library/
	if _, err := shadow.Prepare(context.Background(), src); err != nil {
		t.Fatalf("second Prepare failed: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Error("Library/ sentinel was deleted by second Prepare — Library cache must be preserved")
	}
}

func TestPrepare_ReflectsChangedSources(t *testing.T) {
	src := makeProject(t)
	if _, err := shadow.Prepare(context.Background(), src); err != nil {
		t.Fatalf("first Prepare failed: %v", err)
	}
	_ = os.WriteFile(filepath.Join(src, "Assets", "Scripts", "Player.cs"), []byte("// updated"), 0644)
	ws, err := shadow.Prepare(context.Background(), src)
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
	ws, err := shadow.Prepare(context.Background(), src)
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	libFile := filepath.Join(ws.ShadowPath, "Library", "cached.data")
	_ = os.WriteFile(libFile, []byte("stale"), 0644)

	if _, err := shadow.Reset(context.Background(), src); err != nil {
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
// remapAbsPath must match case-insensitively after slash normalisation.
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
	// The result uses forward slashes.
	want := `C:/MyProject/Assets/Tests/Foo.cs`
	if got != want {
		t.Errorf("RemapPaths mixed-case forward-slash: got %q, want %q", got, want)
	}
}

// TestRemapPaths_SiblingDirNotRemapped verifies that a path whose directory
// name merely starts with ".fastplay-shadow" (e.g. ".fastplay-shadowX") is not
// incorrectly treated as a shadow path due to a loose prefix match.
func TestRemapPaths_SiblingDirNotRemapped(t *testing.T) {
	ws := &shadow.Workspace{
		SourcePath: `C:\MyProject`,
		ShadowPath: `C:\MyProject\.fastplay-shadow`,
	}
	// Path under a sibling directory ".fastplay-shadowX" — must not be remapped.
	siblingPath := `C:/MyProject/.fastplay-shadowX/Assets/Tests/Foo.cs`
	result := &history.RunResult{
		Tests: []parser.TestCase{{AbsolutePath: siblingPath}},
	}
	ws.RemapPaths(result)
	got := result.Tests[0].AbsolutePath
	// Normalised to forward slashes but prefix must not be swapped.
	want := `C:/MyProject/.fastplay-shadowX/Assets/Tests/Foo.cs`
	if got != want {
		t.Errorf("RemapPaths sibling dir: got %q, want %q", got, want)
	}
}

func TestRemapPaths_MessageFieldReplaced(t *testing.T) {
	src := t.TempDir()
	ws := &shadow.Workspace{
		SourcePath: src,
		ShadowPath: filepath.Join(src, ".fastplay-shadow"),
	}
	shadowMsg := "error in file " + filepath.Join(ws.ShadowPath, "Assets", "Scripts", "Foo.cs") + " at line 5"
	result := &history.RunResult{
		Tests: []parser.TestCase{
			{
				AbsolutePath: filepath.Join(ws.ShadowPath, "Assets", "Tests", "Bar.cs"),
				Message:      shadowMsg,
			},
		},
		Errors: []history.CompileError{
			{
				AbsolutePath: filepath.Join(ws.ShadowPath, "Assets", "Scripts", "Foo.cs"),
				Message:      shadowMsg,
			},
		},
	}
	ws.RemapPaths(result)

	wantMsg := "error in file " + filepath.Join(src, "Assets", "Scripts", "Foo.cs") + " at line 5"
	if result.Tests[0].Message != wantMsg {
		t.Errorf("test Message: got %q, want %q", result.Tests[0].Message, wantMsg)
	}
	if result.Errors[0].Message != wantMsg {
		t.Errorf("error Message: got %q, want %q", result.Errors[0].Message, wantMsg)
	}
}

func TestRemapPaths_MessageNoShadowPath_Unchanged(t *testing.T) {
	src := t.TempDir()
	ws := &shadow.Workspace{SourcePath: src, ShadowPath: filepath.Join(src, ".fastplay-shadow")}
	original := "CS0246: The type or namespace name 'Foo' could not be found"
	result := &history.RunResult{
		Errors: []history.CompileError{{Message: original}},
	}
	ws.RemapPaths(result)
	if result.Errors[0].Message != original {
		t.Errorf("message unexpectedly modified: got %q", result.Errors[0].Message)
	}
}

func TestCopyDir_PreservesExecutableBit(t *testing.T) {
	projectDir := makeProject(t)
	// Write a file with the executable bit set.
	exePath := filepath.Join(projectDir, "Assets", "Plugins", "native.so")
	if err := os.MkdirAll(filepath.Dir(exePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exePath, []byte("ELF"), 0755); err != nil {
		t.Fatal(err)
	}

	w, err := shadow.Prepare(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	info, err := os.Stat(filepath.Join(w.ShadowPath, "Assets", "Plugins", "native.so"))
	if err != nil {
		t.Fatalf("shadow file missing: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("executable bit lost in shadow copy: mode %v", info.Mode())
	}
}

func TestCopyDir_CopiesFileContents(t *testing.T) {
	// Validates that copyDir (which calls copyFile internally) produces
	// shadow files with identical content to the source.
	projectDir := makeProject(t)
	content := []byte("// source content")
	_ = os.WriteFile(filepath.Join(projectDir, "Assets", "Script.cs"), content, 0644)

	w, err := shadow.Prepare(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(w.ShadowPath, "Assets", "Script.cs"))
	if err != nil {
		t.Fatalf("shadow file missing: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestPrepare_RespectsContextCancellation(t *testing.T) {
	src := makeProject(t)
	// Fill Assets with enough files to make WalkDir non-trivial.
	for i := 0; i < 20; i++ {
		name := filepath.Join(src, "Assets", "Scripts", fmt.Sprintf("File%d.cs", i))
		_ = os.WriteFile(name, []byte("// file"), 0644)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := shadow.Prepare(ctx, src)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}
