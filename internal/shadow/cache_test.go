package shadow_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

func TestCacheKey_DeterministicFromProjectFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "ProjectSettings"), 0755))
	must(t, os.MkdirAll(filepath.Join(dir, "Packages"), 0755))
	must(t, os.WriteFile(filepath.Join(dir, "ProjectSettings", "ProjectVersion.txt"),
		[]byte("m_EditorVersion: 6000.3.8f1"), 0644))
	must(t, os.WriteFile(filepath.Join(dir, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{}}`), 0644))

	key1, err := shadow.CacheKey(dir)
	if err != nil {
		t.Fatalf("CacheKey: %v", err)
	}
	if len(key1) != 64 {
		t.Fatalf("expected 64-char hex key, got %d chars: %q", len(key1), key1)
	}

	key2, err := shadow.CacheKey(dir)
	if err != nil {
		t.Fatalf("CacheKey second call: %v", err)
	}
	if key1 != key2 {
		t.Errorf("CacheKey not deterministic: %q != %q", key1, key2)
	}
}

func TestCacheKey_ChangesWhenFilesChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "ProjectSettings"), 0755))
	must(t, os.MkdirAll(filepath.Join(dir, "Packages"), 0755))
	must(t, os.WriteFile(filepath.Join(dir, "ProjectSettings", "ProjectVersion.txt"),
		[]byte("m_EditorVersion: 6000.3.8f1"), 0644))
	must(t, os.WriteFile(filepath.Join(dir, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{}}`), 0644))

	key1, err := shadow.CacheKey(dir)
	if err != nil {
		t.Fatalf("CacheKey: %v", err)
	}

	must(t, os.WriteFile(filepath.Join(dir, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{"com.unity.test-framework":"1.4.5"}}`), 0644))

	key2, err := shadow.CacheKey(dir)
	if err != nil {
		t.Fatalf("CacheKey after change: %v", err)
	}
	if key1 == key2 {
		t.Error("CacheKey should change when manifest.json changes")
	}
}

func TestCacheKey_MissingFileReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := shadow.CacheKey(dir)
	if err == nil {
		t.Fatal("expected error when key files are missing")
	}
}

func TestCacheLibraryDir_ReturnsExpectedPath(t *testing.T) {
	t.Parallel()
	dir := "/some/project"
	got := shadow.CacheLibraryDir(dir)
	want := filepath.Join("/some/project", ".testplay", "cache", "Library")
	if got != want {
		t.Errorf("CacheLibraryDir: got %q, want %q", got, want)
	}
}

func TestValidateCache_ReturnsFalseWhenNoCacheExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "ProjectSettings"), 0755))
	must(t, os.MkdirAll(filepath.Join(dir, "Packages"), 0755))
	must(t, os.WriteFile(filepath.Join(dir, "ProjectSettings", "ProjectVersion.txt"),
		[]byte("m_EditorVersion: 6000.3.8f1"), 0644))
	must(t, os.WriteFile(filepath.Join(dir, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{}}`), 0644))

	if shadow.ValidateCache(dir) {
		t.Error("expected ValidateCache to return false when no cache exists")
	}
}

func TestValidateCache_ReturnsTrueAfterSaveCacheKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "ProjectSettings"), 0755))
	must(t, os.MkdirAll(filepath.Join(dir, "Packages"), 0755))
	must(t, os.WriteFile(filepath.Join(dir, "ProjectSettings", "ProjectVersion.txt"),
		[]byte("m_EditorVersion: 6000.3.8f1"), 0644))
	must(t, os.WriteFile(filepath.Join(dir, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{}}`), 0644))

	cacheLib := shadow.CacheLibraryDir(dir)
	must(t, os.MkdirAll(cacheLib, 0755))
	must(t, shadow.SaveCacheKey(dir))

	if !shadow.ValidateCache(dir) {
		t.Error("expected ValidateCache to return true after SaveCacheKey")
	}
}

func TestValidateCache_ReturnsFalseAfterFileChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "ProjectSettings"), 0755))
	must(t, os.MkdirAll(filepath.Join(dir, "Packages"), 0755))
	must(t, os.WriteFile(filepath.Join(dir, "ProjectSettings", "ProjectVersion.txt"),
		[]byte("m_EditorVersion: 6000.3.8f1"), 0644))
	must(t, os.WriteFile(filepath.Join(dir, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{}}`), 0644))

	cacheLib := shadow.CacheLibraryDir(dir)
	must(t, os.MkdirAll(cacheLib, 0755))
	must(t, shadow.SaveCacheKey(dir))

	must(t, os.WriteFile(filepath.Join(dir, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{"com.unity.ugui":"2.0.0"}}`), 0644))

	if shadow.ValidateCache(dir) {
		t.Error("expected ValidateCache to return false after manifest change")
	}
}

func TestUpdateLibraryCache_CopiesToCacheDir(t *testing.T) {
	t.Parallel()
	src := makeProject(t)
	must(t, os.WriteFile(filepath.Join(src, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{}}`), 0644))

	ws, err := shadow.Prepare(context.Background(), src, "update-cache-run",
		shadow.PrepareOptions{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer ws.Cleanup()

	libDir := filepath.Join(ws.ShadowPath, "Library")
	must(t, os.MkdirAll(filepath.Join(libDir, "PackageCache"), 0755))
	must(t, os.WriteFile(filepath.Join(libDir, "PackageCache", "pkg.dat"),
		[]byte("pkg-data"), 0644))
	must(t, os.WriteFile(filepath.Join(libDir, "ArtifactDB"), []byte("db"), 0644))

	if err := ws.UpdateLibraryCache(context.Background()); err != nil {
		t.Fatalf("UpdateLibraryCache: %v", err)
	}

	cacheLib := shadow.CacheLibraryDir(src)
	data, err := os.ReadFile(filepath.Join(cacheLib, "PackageCache", "pkg.dat"))
	if err != nil {
		t.Fatalf("cached file missing: %v", err)
	}
	if string(data) != "pkg-data" {
		t.Errorf("cached content: got %q, want %q", data, "pkg-data")
	}

	if !shadow.ValidateCache(src) {
		t.Error("ValidateCache should return true after UpdateLibraryCache")
	}
}

func TestClearCache_RemovesCacheDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".testplay", "cache")
	must(t, os.MkdirAll(filepath.Join(cacheDir, "Library"), 0755))
	must(t, os.WriteFile(filepath.Join(cacheDir, "cache.key"), []byte("abc"), 0644))

	if err := shadow.ClearCache(dir); err != nil {
		t.Fatalf("ClearCache: %v", err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Error("expected cache dir to be removed after ClearCache")
	}
}
