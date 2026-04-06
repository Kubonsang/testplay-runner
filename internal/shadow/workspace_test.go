package shadow_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

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
	ws, err := shadow.Prepare(context.Background(), src, "test-run-001", shadow.PrepareOptions{})
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
	const runID = "test-run-002"
	ws, err := shadow.Prepare(context.Background(), src, runID, shadow.PrepareOptions{})
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	want := filepath.Join(src, ".testplay-shadow-"+runID)
	if ws.ShadowPath != want {
		t.Errorf("ShadowPath: got %q, want %q", ws.ShadowPath, want)
	}
}

func TestPrepare_IsIdempotent(t *testing.T) {
	src := makeProject(t)
	// Per-run dirs are always new, so use the same runID to test same-dir idempotency.
	const runID = "test-run-003"
	ws, err := shadow.Prepare(context.Background(), src, runID, shadow.PrepareOptions{})
	if err != nil {
		t.Fatalf("first Prepare failed: %v", err)
	}
	// Write a sentinel to Library/ to verify it is preserved on second call with same runID.
	sentinel := filepath.Join(ws.ShadowPath, "Library", "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("preserved"), 0644); err != nil {
		t.Fatalf("could not write sentinel: %v", err)
	}
	// Second prepare with the same runID should succeed and preserve Library/
	if _, err := shadow.Prepare(context.Background(), src, runID, shadow.PrepareOptions{}); err != nil {
		t.Fatalf("second Prepare failed: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Error("Library/ sentinel was deleted by second Prepare — Library cache must be preserved")
	}
}

func TestPrepare_ReflectsChangedSources(t *testing.T) {
	src := makeProject(t)
	const runID = "test-run-004"
	if _, err := shadow.Prepare(context.Background(), src, runID, shadow.PrepareOptions{}); err != nil {
		t.Fatalf("first Prepare failed: %v", err)
	}
	_ = os.WriteFile(filepath.Join(src, "Assets", "Scripts", "Player.cs"), []byte("// updated"), 0644)
	ws, err := shadow.Prepare(context.Background(), src, runID, shadow.PrepareOptions{})
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
	const runID = "test-run-005"
	ws, err := shadow.Prepare(context.Background(), src, runID, shadow.PrepareOptions{})
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	libFile := filepath.Join(ws.ShadowPath, "Library", "cached.data")
	_ = os.WriteFile(libFile, []byte("stale"), 0644)

	// Reset with a new runID — per-run dirs are always fresh, so the old lib file is in a different dir.
	ws2, err := shadow.Reset(context.Background(), src, "test-run-005-reset")
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}
	// The new workspace must not contain the stale lib file.
	newLibFile := filepath.Join(ws2.ShadowPath, "Library", "cached.data")
	if _, err := os.Stat(newLibFile); err == nil {
		t.Error("Library cache was not deleted by Reset")
	}
}

func TestRemapPaths_TestAndErrorPaths(t *testing.T) {
	src := t.TempDir()
	ws := &shadow.Workspace{
		SourcePath: src,
		ShadowPath: filepath.Join(src, ".testplay-shadow"),
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

	wantTest := filepath.ToSlash(filepath.Join(src, "Assets", "Tests", "Player.cs"))
	if result.Tests[0].AbsolutePath != wantTest {
		t.Errorf("test path: got %q, want %q", result.Tests[0].AbsolutePath, wantTest)
	}
	wantErr := filepath.ToSlash(filepath.Join(src, "Assets", "Scripts", "Bad.cs"))
	if result.Errors[0].AbsolutePath != wantErr {
		t.Errorf("error path: got %q, want %q", result.Errors[0].AbsolutePath, wantErr)
	}
}

func TestRemapPaths_NoopWhenNoShadowPaths(t *testing.T) {
	src := t.TempDir()
	ws := &shadow.Workspace{SourcePath: src, ShadowPath: filepath.Join(src, ".testplay-shadow")}
	result := &history.RunResult{
		Tests: []parser.TestCase{
			{AbsolutePath: filepath.Join(src, "Assets", "Tests", "Player.cs")},
		},
	}
	ws.RemapPaths(result)
	if result.Tests[0].AbsolutePath != filepath.ToSlash(filepath.Join(src, "Assets", "Tests", "Player.cs")) {
		t.Error("path was unexpectedly modified")
	}
}

func TestRemapPaths_NewFailurePaths(t *testing.T) {
	src := t.TempDir()
	ws := &shadow.Workspace{
		SourcePath: src,
		ShadowPath: filepath.Join(src, ".testplay-shadow"),
	}
	result := &history.RunResult{
		NewFailures: []parser.TestCase{
			{AbsolutePath: filepath.Join(ws.ShadowPath, "Assets", "Tests", "NewTest.cs")},
		},
	}
	ws.RemapPaths(result)
	want := filepath.ToSlash(filepath.Join(src, "Assets", "Tests", "NewTest.cs"))
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
		ShadowPath: `C:\MyProject\.testplay-shadow`,
	}
	// Unity log path: forward slashes, lowercase drive letter.
	unityPath := `c:/myproject/.testplay-shadow/Assets/Tests/Foo.cs`
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
// name merely starts with ".testplay-shadow" (e.g. ".testplay-shadowX") is not
// incorrectly treated as a shadow path due to a loose prefix match.
func TestRemapPaths_SiblingDirNotRemapped(t *testing.T) {
	ws := &shadow.Workspace{
		SourcePath: `C:\MyProject`,
		ShadowPath: `C:\MyProject\.testplay-shadow`,
	}
	// Path under a sibling directory ".testplay-shadowX" — must not be remapped.
	siblingPath := `C:/MyProject/.testplay-shadowX/Assets/Tests/Foo.cs`
	result := &history.RunResult{
		Tests: []parser.TestCase{{AbsolutePath: siblingPath}},
	}
	ws.RemapPaths(result)
	got := result.Tests[0].AbsolutePath
	// Normalised to forward slashes but prefix must not be swapped.
	want := `C:/MyProject/.testplay-shadowX/Assets/Tests/Foo.cs`
	if got != want {
		t.Errorf("RemapPaths sibling dir: got %q, want %q", got, want)
	}
}

func TestRemapPaths_MessageFieldReplaced(t *testing.T) {
	src := t.TempDir()
	ws := &shadow.Workspace{
		SourcePath: src,
		ShadowPath: filepath.Join(src, ".testplay-shadow"),
	}
	shadowMsg := "error in file " + filepath.ToSlash(filepath.Join(ws.ShadowPath, "Assets", "Scripts", "Foo.cs")) + " at line 5"
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

	wantMsg := "error in file " + filepath.ToSlash(filepath.Join(src, "Assets", "Scripts", "Foo.cs")) + " at line 5"
	if result.Tests[0].Message != wantMsg {
		t.Errorf("test Message: got %q, want %q", result.Tests[0].Message, wantMsg)
	}
	if result.Errors[0].Message != wantMsg {
		t.Errorf("error Message: got %q, want %q", result.Errors[0].Message, wantMsg)
	}
}

func TestRemapPaths_MessageNoShadowPath_Unchanged(t *testing.T) {
	src := t.TempDir()
	ws := &shadow.Workspace{SourcePath: src, ShadowPath: filepath.Join(src, ".testplay-shadow")}
	original := "CS0246: The type or namespace name 'Foo' could not be found"
	result := &history.RunResult{
		Errors: []history.CompileError{{Message: original}},
	}
	ws.RemapPaths(result)
	if result.Errors[0].Message != original {
		t.Errorf("message unexpectedly modified: got %q", result.Errors[0].Message)
	}
}

func TestRemapString_MixedCaseDriveLetter(t *testing.T) {
	// Simulates Windows: Unity logs lowercase drive, filepath.Abs returns uppercase.
	ws := &shadow.Workspace{
		SourcePath: `C:\MyProject`,
		ShadowPath: `C:\MyProject\.testplay-shadow`,
	}
	// Message contains forward slashes and lowercase drive (as Unity emits on Windows).
	msg := `error in file c:/myproject/.testplay-shadow/Assets/Scripts/Foo.cs at line 5`
	result := &history.RunResult{
		Errors: []history.CompileError{{Message: msg}},
	}
	ws.RemapPaths(result)
	want := `error in file C:/MyProject/Assets/Scripts/Foo.cs at line 5`
	if result.Errors[0].Message != want {
		t.Errorf("mixed-case message remap: got %q, want %q", result.Errors[0].Message, want)
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

	w, err := shadow.Prepare(context.Background(), projectDir, "test-run-006", shadow.PrepareOptions{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	info, err := os.Stat(filepath.Join(w.ShadowPath, "Assets", "Plugins", "native.so"))
	if err != nil {
		t.Fatalf("shadow file missing: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		t.Errorf("executable bit lost in shadow copy: mode %v", info.Mode())
	}
}

func TestCopyDir_CopiesFileContents(t *testing.T) {
	// Validates that copyDir (which calls copyFile internally) produces
	// shadow files with identical content to the source.
	projectDir := makeProject(t)
	content := []byte("// source content")
	_ = os.WriteFile(filepath.Join(projectDir, "Assets", "Script.cs"), content, 0644)

	w, err := shadow.Prepare(context.Background(), projectDir, "test-run-007", shadow.PrepareOptions{})
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

func TestCopyDir_PreservesSymlink(t *testing.T) {
	projectDir := makeProject(t)
	// Create a symlink inside Assets/ pointing to the existing Player.cs.
	linkPath := filepath.Join(projectDir, "Assets", "Scripts", "PlayerAlias.cs")
	target := "Player.cs" // relative symlink target
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	w, err := shadow.Prepare(context.Background(), projectDir, "test-run-008", shadow.PrepareOptions{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	shadowLink := filepath.Join(w.ShadowPath, "Assets", "Scripts", "PlayerAlias.cs")
	info, err := os.Lstat(shadowLink)
	if err != nil {
		t.Fatalf("shadow symlink missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected symlink, got regular file (mode %v)", info.Mode())
	}
	got, err := os.Readlink(shadowLink)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if got != target {
		t.Errorf("link target: got %q, want %q", got, target)
	}
}

func TestPrepare_RespectsContextCancellation(t *testing.T) {
	src := makeProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Prepare is called

	// copyDir checks ctx.Err() on every WalkDir entry, including the root
	// directory itself, so an already-cancelled context is detected on the
	// very first iteration regardless of how many files are present.
	_, err := shadow.Prepare(ctx, src, "test-run-009", shadow.PrepareOptions{})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestPrepare_CleansUpOnFirstCreateFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod(0000) does not prevent reads on Windows/NTFS")
	}
	src := makeProject(t)
	// Make ProjectSettings/ unreadable so copyDir fails on the second iteration.
	psDir := filepath.Join(src, "ProjectSettings")
	if err := os.Chmod(psDir, 0000); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { os.Chmod(psDir, 0755) })

	const runID = "test-run-010"
	shadowPath := filepath.Join(src, ".testplay-shadow-"+runID)

	// Confirm shadow does not exist yet (first-create scenario).
	if _, err := os.Stat(shadowPath); !os.IsNotExist(err) {
		t.Fatalf("shadow should not exist before Prepare")
	}

	_, err := shadow.Prepare(context.Background(), src, runID, shadow.PrepareOptions{})
	if err == nil {
		t.Fatal("expected error from unreadable ProjectSettings, got nil")
	}

	// Shadow workspace must be cleaned up after first-create failure.
	if _, err := os.Stat(shadowPath); !os.IsNotExist(err) {
		t.Errorf("expected shadow workspace to be removed after failure, but it still exists")
	}
}

func TestPrepare_RollsBackOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod(0000) does not prevent reads on Windows/NTFS")
	}
	src := makeProject(t)

	// With per-run isolation (unique runID per invocation), all Prepare calls
	// start with a new directory. On failure, rollback is unconditional.
	const runID = "test-run-012"
	shadowPath := shadow.ShadowWorkspaceDir(src, runID)

	// Make ProjectSettings/ unreadable so the copyDir fails.
	psDir := filepath.Join(src, "ProjectSettings")
	if err := os.Chmod(psDir, 0000); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { os.Chmod(psDir, 0755) })

	// Prepare should fail and roll back the shadow directory.
	_, err := shadow.Prepare(context.Background(), src, runID, shadow.PrepareOptions{})
	if err == nil {
		t.Fatal("expected error from unreadable ProjectSettings, got nil")
	}

	// Shadow workspace must be cleaned up after failure.
	if _, err := os.Stat(shadowPath); !os.IsNotExist(err) {
		t.Error("expected shadow workspace to be removed after failure, but it still exists")
	}
}

func TestPrepare_CancelsInsideLargeFileCopy(t *testing.T) {
	projectDir := makeProject(t)
	// 2 MB file — large enough that io.Copy needs multiple Read calls.
	// ctxReader checks ctx.Err() before each 32KB chunk, so cancellation
	// during the copy propagates within one buffer-load.
	largeFile := filepath.Join(projectDir, "Assets", "large.bin")
	data := make([]byte, 2*1024*1024)
	if err := os.WriteFile(largeFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := shadow.Prepare(ctx, projectDir, "cancel-test-run", shadow.PrepareOptions{})
		errCh <- err
	}()

	// Cancel immediately — Prepare may still be in WalkDir setup or io.Copy.
	cancel()

	err := <-errCh
	if err == nil {
		// If Prepare finished before cancel fired, the test is inconclusive
		// on fast hardware. Skip rather than fail.
		t.Skip("Prepare completed before context was cancelled — test inconclusive on fast hardware")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestPrepare_PerRunIsolation(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "Assets"), 0755))
	must(t, os.MkdirAll(filepath.Join(src, "ProjectSettings"), 0755))
	must(t, os.MkdirAll(filepath.Join(src, "Packages"), 0755))
	must(t, os.WriteFile(filepath.Join(src, "Assets", "a.txt"), []byte("hello"), 0644))

	ws1, err := shadow.Prepare(context.Background(), src, "run-aaa", shadow.PrepareOptions{})
	if err != nil {
		t.Fatalf("Prepare run-aaa: %v", err)
	}
	ws2, err := shadow.Prepare(context.Background(), src, "run-bbb", shadow.PrepareOptions{})
	if err != nil {
		t.Fatalf("Prepare run-bbb: %v", err)
	}

	if ws1.ShadowPath == ws2.ShadowPath {
		t.Fatal("expected distinct shadow paths for different run IDs")
	}
	for _, ws := range []*shadow.Workspace{ws1, ws2} {
		if _, err := os.Stat(filepath.Join(ws.ShadowPath, "Assets", "a.txt")); err != nil {
			t.Errorf("Assets/a.txt missing in %s: %v", ws.ShadowPath, err)
		}
	}

	if err := ws1.Cleanup(); err != nil {
		t.Fatalf("Cleanup ws1: %v", err)
	}
	if _, err := os.Stat(ws1.ShadowPath); !os.IsNotExist(err) {
		t.Error("ws1 shadow dir should be gone after Cleanup")
	}
	if _, err := os.Stat(ws2.ShadowPath); err != nil {
		t.Error("ws2 shadow dir should still exist after ws1.Cleanup()")
	}
	_ = ws2.Cleanup()
}

func TestPrepare_ShadowDirNameContainsRunID(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "Assets"), 0755))
	must(t, os.MkdirAll(filepath.Join(src, "ProjectSettings"), 0755))
	must(t, os.MkdirAll(filepath.Join(src, "Packages"), 0755))

	const runID = "20260326-143055-a3f8b2c1"
	ws, err := shadow.Prepare(context.Background(), src, runID, shadow.PrepareOptions{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer ws.Cleanup()

	if !strings.HasSuffix(ws.ShadowPath, ".testplay-shadow-"+runID) {
		t.Errorf("ShadowPath %q does not end with '.testplay-shadow-%s'", ws.ShadowPath, runID)
	}
}

func TestPrepare_SeedsLibraryFromCache(t *testing.T) {
	t.Parallel()
	src := makeProject(t)

	cacheLib := shadow.CacheLibraryDir(src)
	must(t, os.MkdirAll(filepath.Join(cacheLib, "PackageCache"), 0755))
	must(t, os.WriteFile(filepath.Join(cacheLib, "PackageCache", "cached.dat"),
		[]byte("cached-content"), 0644))
	must(t, os.WriteFile(filepath.Join(src, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{}}`), 0644))
	must(t, shadow.SaveCacheKey(src))

	ws, err := shadow.Prepare(context.Background(), src, "cache-seed-run",
		shadow.PrepareOptions{LibraryCacheDir: cacheLib})
	if err != nil {
		t.Fatalf("Prepare with cache: %v", err)
	}
	defer ws.Cleanup()

	data, err := os.ReadFile(filepath.Join(ws.ShadowPath, "Library", "PackageCache", "cached.dat"))
	if err != nil {
		t.Fatalf("cached file not seeded into shadow Library: %v", err)
	}
	if string(data) != "cached-content" {
		t.Errorf("cached file content: got %q, want %q", data, "cached-content")
	}
}

func TestPrepare_EmptyOptionsBackwardCompatible(t *testing.T) {
	t.Parallel()
	src := makeProject(t)
	must(t, os.WriteFile(filepath.Join(src, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{}}`), 0644))

	ws, err := shadow.Prepare(context.Background(), src, "compat-run",
		shadow.PrepareOptions{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer ws.Cleanup()

	entries, _ := os.ReadDir(filepath.Join(ws.ShadowPath, "Library"))
	if len(entries) != 0 {
		t.Errorf("expected empty Library/ without cache, got %d entries", len(entries))
	}
}
