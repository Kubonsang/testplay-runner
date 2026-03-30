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
	if _, err := shadow.Prepare(src); err != nil {
		t.Fatalf("first Prepare failed: %v", err)
	}
	if _, err := shadow.Prepare(src); err != nil {
		t.Fatalf("second Prepare failed: %v", err)
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
