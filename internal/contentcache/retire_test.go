package contentcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRetirementPreviewProtectsTrackedDataAndKeepsSharedCache(t *testing.T) {
	store, p, lib, key := fixture(t)
	ctx := context.Background()
	if out, err := exec.Command("git", "-C", p, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	h := sha256.New()
	for _, rel := range []string{filepath.Join("ProjectSettings", "ProjectVersion.txt"), filepath.Join("Packages", "manifest.json")} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(p, rel)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, rel), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(rel))
		h.Write([]byte("fixture"))
	}
	cache := filepath.Join(p, ".testplay", "cache")
	if err := os.MkdirAll(filepath.Join(cache, "Library"), 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(cache, "cache.key")
	if err := os.WriteFile(marker, []byte(hex.EncodeToString(h.Sum(nil))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, p, key, lib); err != nil {
		t.Fatal(err)
	}
	preview, err := store.RetireLocal(ctx, p, key, false)
	if err != nil || preview.Executed {
		t.Fatalf("preview failed: %+v %v", preview, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("preview deleted cache")
	}
	if out, err := exec.Command("git", "-C", p, "add", "-f", ".testplay/cache/cache.key").CombinedOutput(); err != nil {
		t.Fatalf("stage marker: %s %v", out, err)
	}
	if _, err := store.RetireLocal(ctx, p, key, true); err == nil {
		t.Fatal("tracked cache was deleted")
	}
	if out, err := exec.Command("git", "-C", p, "rm", "--cached", "-f", ".testplay/cache/cache.key").CombinedOutput(); err != nil {
		t.Fatalf("unstage marker: %s %v", out, err)
	}
	result, err := store.RetireLocal(ctx, p, key, true)
	if err != nil || !result.Executed {
		t.Fatalf("retirement: %+v %v", result, err)
	}
	if _, err := os.Stat(lib); err != nil {
		t.Fatal("source Library deleted")
	}
	usage, err := store.Inspect(ctx, false)
	if err != nil || usage.ManifestCount != 1 {
		t.Fatal("shared cache changed")
	}
}
