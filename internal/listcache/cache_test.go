package listcache_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/listcache"
	"github.com/Kubonsang/testplay-runner/internal/parser"
)

func TestWriteRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	tests := []parser.TestCase{
		{Name: "MyTests.PlayerTests.TestJump"},
		{Name: "MyTests.PlayerTests.TestRun"},
	}

	if err := listcache.Write(dir, "20260409-120000-a1b2c3d4", tests); err != nil {
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
}

func TestWrite_IsAtomic(t *testing.T) {
	dir := t.TempDir()
	tests := []parser.TestCase{{Name: "A.B"}}

	if err := listcache.Write(dir, "run1", tests); err != nil {
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
	if err := listcache.Write(dir, "run1", []parser.TestCase{}); err != nil {
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
	_ = listcache.Write(dir, "run1", []parser.TestCase{{Name: "A.B"}})
	_ = listcache.Write(dir, "run2", []parser.TestCase{{Name: "X.Y"}, {Name: "X.Z"}})

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
