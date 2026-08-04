package refsworkspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	release, err := store.AcquireUse(context.Background(), key, "lease-0001")
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

func TestBaselineAcquireUseAndClearAreCoordinated(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("4")
	if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte("baseline"), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	store.coordinationHook = func(operation string) {
		if operation == "acquire-baseline-use" {
			once.Do(func() { close(locked); <-proceed })
		}
	}
	type acquireResult struct {
		release func() error
		err     error
	}
	acquired := make(chan acquireResult, 1)
	go func() {
		release, err := store.AcquireUse(context.Background(), key, "lease-coord1")
		acquired <- acquireResult{release, err}
	}()
	<-locked
	cleared := make(chan error, 1)
	go func() { cleared <- store.Clear(context.Background(), key) }()
	close(proceed)
	result := <-acquired
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := <-cleared; ErrorCode(err) != CodeBaselineInUse {
		t.Fatalf("clear err=%v", err)
	}
	if err := result.release(); err != nil {
		t.Fatal(err)
	}
	store.coordinationHook = nil
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineClearWinsBeforeAcquireUse(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("5")
	if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte("baseline"), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	store.coordinationHook = func(operation string) {
		if operation == "clear-baseline" {
			once.Do(func() { close(locked); <-proceed })
		}
	}
	cleared := make(chan error, 1)
	go func() { cleared <- store.Clear(context.Background(), key) }()
	<-locked
	acquired := make(chan error, 1)
	go func() { _, err := store.AcquireUse(context.Background(), key, "lease-coord2"); acquired <- err }()
	close(proceed)
	if err := <-cleared; err != nil {
		t.Fatal(err)
	}
	if err := <-acquired; ErrorCode(err) != CodeBaselineMissing {
		t.Fatalf("acquire err=%v", err)
	}
	if count, _ := countEntries(paths.Leases, "active-", ".json"); count != 0 {
		t.Fatalf("active marker residual=%d", count)
	}
}

func TestBaselineAcquireUseAndQuarantineAreCoordinated(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("8")
	if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte("baseline"), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	store.coordinationHook = func(operation string) {
		if operation == "acquire-baseline-use" {
			once.Do(func() { close(locked); <-proceed })
		}
	}
	type acquireResult struct {
		release func() error
		err     error
	}
	acquired := make(chan acquireResult, 1)
	go func() {
		release, err := store.AcquireUse(context.Background(), key, "lease-quar1")
		acquired <- acquireResult{release, err}
	}()
	<-locked
	quarantined := make(chan error, 1)
	go func() { _, err := store.Quarantine(context.Background(), key, "race"); quarantined <- err }()
	close(proceed)
	result := <-acquired
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := <-quarantined; ErrorCode(err) != CodeBaselineInUse {
		t.Fatalf("quarantine err=%v", err)
	}
	if err := result.release(); err != nil {
		t.Fatal(err)
	}
	store.coordinationHook = nil
	path, err := store.Quarantine(context.Background(), key, "cleanup")
	if err != nil {
		t.Fatal(err)
	}
	if err := makeWritableTree(path); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineProtectionDamageIsCorruptionEvenWhenContentMatches(t *testing.T) {
	for _, test := range []struct {
		name   string
		damage func(*testing.T, *Baseline)
	}{
		{name: "file writable", damage: func(t *testing.T, baseline *Baseline) {
			if err := os.Chmod(filepath.Join(baseline.LibraryPath, "artifact.bin"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "root writable", damage: func(t *testing.T, baseline *Baseline) {
			if err := damageDirectoryProtectionForTest(baseline.LibraryPath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "child directory writable", damage: func(t *testing.T, baseline *Baseline) {
			if err := damageDirectoryProtectionForTest(filepath.Join(baseline.LibraryPath, "ArtifactDB")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "file ACL", damage: func(t *testing.T, baseline *Baseline) {
			if err := damageFileProtectionForTest(filepath.Join(baseline.LibraryPath, "artifact.bin")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "metadata digest", damage: func(t *testing.T, baseline *Baseline) {
			metadata := baseline.Metadata
			metadata.Protection.TreeDescriptorSHA256 = strings.Repeat("0", 64)
			if err := writeJSONAtomic(filepath.Join(baseline.Path, baselineMetadataFile), metadata, 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "entry count", damage: func(t *testing.T, baseline *Baseline) {
			metadata := baseline.Metadata
			metadata.Protection.DirectoryCount++
			if err := writeJSONAtomic(filepath.Join(baseline.Path, baselineMetadataFile), metadata, 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "policy missing", damage: func(t *testing.T, baseline *Baseline) {
			metadata := baseline.Metadata
			metadata.Protection.FilePolicy = ""
			if err := writeJSONAtomic(filepath.Join(baseline.Path, baselineMetadataFile), metadata, 0600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := testPoolPaths(t)
			store := NewLibraryBaselineStore(paths)
			key := testCompatibilityKey("6")
			baseline, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
				if err := os.Mkdir(filepath.Join(libraryPath, "ArtifactDB"), 0700); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(libraryPath, "ArtifactDB", "child.bin"), []byte("child-content"), 0600); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte("same-content"), 0600)
			})
			if err != nil {
				t.Fatal(err)
			}
			test.damage(t, baseline)
			resolution, _, err := store.Resolve(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			if resolution.State != BaselineCorrupt {
				t.Fatalf("resolution=%+v", resolution)
			}
			_ = makeWritableTree(baseline.LibraryPath)
		})
	}
}

func TestBaselineStatusDistinguishesAvailableInUseAndMutating(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("7")
	if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte("status"), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	status, _, err := store.Status(context.Background(), key)
	if err != nil || status.CoordinationState != BaselineAvailable {
		t.Fatalf("available status=%+v err=%v", status, err)
	}
	release, err := store.AcquireUse(context.Background(), key, "lease-state1")
	if err != nil {
		t.Fatal(err)
	}
	status, _, err = store.Status(context.Background(), key)
	if err != nil || status.CoordinationState != BaselineInUse {
		t.Fatalf("in-use status=%+v err=%v", status, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := createJSONExclusive(store.baselineMutationPath(key), map[string]any{"schemaVersion": LeaseSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	status, _, err = store.Status(context.Background(), key)
	if err != nil || status.State != BaselineMutating {
		t.Fatalf("mutating status=%+v err=%v", status, err)
	}
	if err := os.Remove(store.baselineMutationPath(key)); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
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
