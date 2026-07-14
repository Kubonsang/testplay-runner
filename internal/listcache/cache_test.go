package listcache_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/listcache"
	"github.com/Kubonsang/testplay-runner/internal/parser"
)

var editModeInventory = listcache.Metadata{FullInventory: true, TestPlatform: "edit_mode"}

func TestWriteRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	tests := []parser.TestCase{
		{Name: "MyTests.PlayerTests.TestJump"},
		{Name: "MyTests.PlayerTests.TestRun"},
	}

	if err := listcache.Write(dir, "20260409-120000-a1b2c3d4", tests, editModeInventory); err != nil {
		t.Fatalf("Write: %v", err)
	}

	c, err := listcache.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if c.CachedRunID != "20260409-120000-a1b2c3d4" {
		t.Errorf("CachedRunID = %q, want %q", c.CachedRunID, "20260409-120000-a1b2c3d4")
	}
	if len(c.Tests) != 2 {
		t.Fatalf("len(Tests) = %d, want 2", len(c.Tests))
	}
	if c.Tests[0] != "MyTests.PlayerTests.TestJump" {
		t.Errorf("Tests[0] = %q", c.Tests[0])
	}
	if c.SchemaVersion != listcache.SchemaVersion || !c.FullInventory || c.TestPlatform != "edit_mode" {
		t.Errorf("cache provenance missing: %+v", c)
	}
}

func TestWrite_IsAtomic(t *testing.T) {
	dir := t.TempDir()
	tests := []parser.TestCase{{Name: "A.B"}}

	if err := listcache.Write(dir, "run1", tests, editModeInventory); err != nil {
		t.Fatalf("Write: %v", err)
	}

	cacheDir := filepath.Join(dir, ".testplay", "cache")
	entries, _ := os.ReadDir(cacheDir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file lingered: %s", e.Name())
		}
	}
}

func TestRead_MissingCache_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	if _, err := listcache.Read(dir); err == nil {
		t.Error("expected error for missing cache, got nil")
	}
}

func TestWrite_EmptyTestSlice(t *testing.T) {
	dir := t.TempDir()
	if err := listcache.Write(dir, "run1", []parser.TestCase{}, editModeInventory); err != nil {
		t.Fatalf("Write: %v", err)
	}
	c, err := listcache.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(c.Tests) != 0 {
		t.Errorf("expected 0 tests, got %d", len(c.Tests))
	}
}

func TestWrite_OverwritesPreviousCache(t *testing.T) {
	dir := t.TempDir()
	_ = listcache.Write(dir, "run1", []parser.TestCase{{Name: "A.B"}}, editModeInventory)
	_ = listcache.Write(dir, "run2", []parser.TestCase{{Name: "X.Y"}, {Name: "X.Z"}}, editModeInventory)

	c, err := listcache.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if c.CachedRunID != "run2" {
		t.Errorf("CachedRunID = %q, want run2", c.CachedRunID)
	}
	if len(c.Tests) != 2 {
		t.Errorf("len(Tests) = %d, want 2", len(c.Tests))
	}
}

func TestRead_Schema1CacheIsStale(t *testing.T) {
	dir := t.TempDir()
	path := listcache.CachePath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"schema_version":"1","cached_run_id":"filtered","cached_at":"2026-04-09T12:00:00Z","tests":["Only.One"]}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := listcache.Read(dir); !errors.Is(err, listcache.ErrStaleCache) {
		t.Fatalf("schema-1 cache must be invalidated, got %v", err)
	}
}

func TestRead_IncompleteInventoryIsRejected(t *testing.T) {
	dir := t.TempDir()
	if err := listcache.Write(dir, "filtered", []parser.TestCase{{Name: "Only.One"}}, listcache.Metadata{
		FullInventory: false,
		TestPlatform:  "edit_mode",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := listcache.Read(dir); !errors.Is(err, listcache.ErrIncompleteCache) {
		t.Fatalf("partial inventory must not be trusted, got %v", err)
	}
}

func TestReadForPlatform_RejectsOtherPlatform(t *testing.T) {
	dir := t.TempDir()
	if err := listcache.Write(dir, "run1", []parser.TestCase{{Name: "Play.One"}}, listcache.Metadata{
		FullInventory: true,
		TestPlatform:  "play_mode",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := listcache.ReadForPlatform(dir, "edit_mode"); !errors.Is(err, listcache.ErrPlatformMismatch) {
		t.Fatalf("cross-platform cache must not be trusted, got %v", err)
	}
}
