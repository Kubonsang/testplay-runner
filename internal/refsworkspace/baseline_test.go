package refsworkspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryBaselineStoreDirectBuildVerifyActiveUseAndClear(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("a")
	buildCount := 0
	baseline, previous, metrics, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		buildCount++
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("x", 8193)), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if previous != BaselineMissing || buildCount != 1 || baseline.Metadata.Library.FileCount != 1 || metrics.BaselineLogicalBytes != 8193 {
		t.Fatalf("previous=%s builds=%d baseline=%+v metrics=%+v", previous, buildCount, baseline, metrics)
	}
	reused, previous, _, err := store.Ensure(context.Background(), key, func(context.Context, string) error {
		buildCount++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if previous != BaselineValid || reused.Path != baseline.Path || buildCount != 1 {
		t.Fatalf("baseline was not reused: previous=%s builds=%d", previous, buildCount)
	}
	release, err := store.AcquireUse(key, "lease-0001")
	if err != nil {
		t.Fatal(err)
	}
	if code := ErrorCode(store.Clear(context.Background(), key)); code != CodeBaselineInUse {
		t.Fatalf("clear code=%s", code)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal("release must be idempotent:", err)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	resolution, _, err := store.Resolve(context.Background(), key)
	if err != nil || resolution.State != BaselineMissing {
		t.Fatalf("resolution=%+v err=%v", resolution, err)
	}
}

func TestLibraryBaselineStoreDetectsAndQuarantinesCorruption(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("b")
	baseline, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("z", 5000)), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(baseline.LibraryPath, "artifact.bin")
	if err := os.Chmod(artifact, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	resolution, _, err := store.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.State != BaselineCorrupt {
		t.Fatalf("resolution=%+v", resolution)
	}
	replacement, previous, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("n", 6000)), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if previous != BaselineCorrupt || replacement.Metadata.Library.LogicalBytes != 6000 {
		t.Fatalf("previous=%s replacement=%+v", previous, replacement)
	}
	entries, err := os.ReadDir(paths.Quarantine)
	if err != nil || len(entries) != 1 {
		t.Fatalf("quarantine entries=%d err=%v", len(entries), err)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		_ = makeWritableTree(filepath.Join(paths.Quarantine, entry.Name()))
	}
}

func TestBaselineCreationLockIsNoWait(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("c")
	lock, err := store.acquireCreationLock(key)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	if _, _, _, err := store.Ensure(context.Background(), key, func(context.Context, string) error { return nil }); ErrorCode(err) != CodeLeaseConflict {
		t.Fatalf("err=%v", err)
	}
}

func testPoolPaths(t *testing.T) Paths {
	t.Helper()
	root := filepath.Join(t.TempDir(), "storage")
	_, paths, err := NewPaths(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.PoolRoot, paths.Baselines, paths.Workers, paths.Leases, paths.Quarantine} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func testCompatibilityKey(seed string) CompatibilityKey {
	return CompatibilityKey{SchemaVersion: BaselineSchemaVersion, Digest: strings.Repeat(seed, 64)}
}
