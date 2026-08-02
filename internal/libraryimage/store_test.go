package libraryimage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStore_CreateAndResolveImage(t *testing.T) {
	project := makeKeyProject(t)
	key, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	sourceLibrary := filepath.Join(t.TempDir(), "Library")
	if err := os.MkdirAll(filepath.Join(sourceLibrary, "ScriptAssemblies"), 0755); err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(sourceLibrary, "ScriptAssemblies", "Tests.dll")
	writeFile(t, sourceFile, "base")

	store := NewStore(project)
	image, err := store.Create(context.Background(), key, sourceLibrary)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	resolution, err := store.Resolve(context.Background(), key)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolution.Status != StatusValid {
		t.Fatalf("status = %q, want valid (%s)", resolution.Status, resolution.Reason)
	}
	if resolution.Image.Metadata.FileCount != 1 {
		t.Fatalf("FileCount = %d, want 1", resolution.Image.Metadata.FileCount)
	}

	baseData, err := os.ReadFile(filepath.Join(image.LibraryPath, "ScriptAssemblies", "Tests.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if string(baseData) != "base" {
		t.Fatalf("base image mutated through materialized Library: %q", baseData)
	}
}

func TestStore_VerificationMetricsPreserveBothWarmFullHashes(t *testing.T) {
	project := makeKeyProject(t)
	key, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(t.TempDir(), "Library")
	if err := os.MkdirAll(library, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(library, "ArtifactDB"), strings.Repeat("hash-me", 1024))

	store := NewStore(project)
	if _, err := store.Create(context.Background(), key, library); err != nil {
		t.Fatal(err)
	}
	before := store.VerificationMetrics()

	resolution, err := store.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != StatusValid {
		t.Fatalf("Resolve status = %q, want valid", resolution.Status)
	}
	afterResolve := store.VerificationMetrics()
	if got := afterResolve.FullHashCount - before.FullHashCount; got != 1 {
		t.Fatalf("Resolve full hashes = %d, want 1", got)
	}

	verified, err := store.Verify(context.Background(), resolution.Image)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Status != StatusValid {
		t.Fatalf("Verify status = %q, want valid", verified.Status)
	}
	afterVerify := store.VerificationMetrics()
	if got := afterVerify.FullHashCount - before.FullHashCount; got != 2 {
		t.Fatalf("Resolve + Verify full hashes = %d, want 2", got)
	}
	if afterVerify.MetadataVerify < before.MetadataVerify ||
		afterVerify.FullHash < before.FullHash {
		t.Fatalf("verification durations regressed: before=%+v after=%+v", before, afterVerify)
	}
}

func TestStore_ResolveMissingAndStale(t *testing.T) {
	project := makeKeyProject(t)
	store := NewStore(project)
	firstKey, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := store.Resolve(context.Background(), firstKey)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != StatusMissing {
		t.Fatalf("initial status = %q, want missing", resolution.Status)
	}

	library := filepath.Join(t.TempDir(), "Library")
	if err := os.MkdirAll(library, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(library, "ArtifactDB"), "db")
	if _, err := store.Create(context.Background(), firstKey, library); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(project, "Packages", "manifest.json"),
		`{"dependencies":{"com.unity.test-framework":"2.0.0"}}`)
	secondKey, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	resolution, err = store.Resolve(context.Background(), secondKey)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != StatusStale {
		t.Fatalf("changed-key status = %q, want stale", resolution.Status)
	}
}

func TestStore_IncompleteImageIsCorrupt(t *testing.T) {
	project := makeKeyProject(t)
	key, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(project)
	if err := os.MkdirAll(filepath.Join(store.imageDir(key), "Library"), 0755); err != nil {
		t.Fatal(err)
	}

	resolution, err := store.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != StatusCorrupt {
		t.Fatalf("status = %q, want corrupt", resolution.Status)
	}
	if !strings.Contains(resolution.Reason, "completion marker") {
		t.Fatalf("unexpected reason: %s", resolution.Reason)
	}
}

func TestStore_CorruptImageIsQuarantinedAndRecreated(t *testing.T) {
	project := makeKeyProject(t)
	key, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(t.TempDir(), "Library")
	if err := os.MkdirAll(library, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(library, "ArtifactDB"), "original")
	store := NewStore(project)
	image, err := store.Create(context.Background(), key, library)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(image.LibraryPath, "ArtifactDB"), "corrupt")

	resolution, err := store.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != StatusCorrupt {
		t.Fatalf("status = %q, want corrupt", resolution.Status)
	}

	recreated, err := store.Create(context.Background(), key, library)
	if err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if recreated.Metadata.LibraryDigest != image.Metadata.LibraryDigest {
		t.Fatal("recreated image digest differs from clean source")
	}
	entries, err := os.ReadDir(filepath.Join(store.Root(), "quarantine"))
	if err != nil {
		t.Fatalf("read quarantine: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("quarantine entries = %d, want 1", len(entries))
	}
}

func TestStore_CreateFailureRemovesStagingImage(t *testing.T) {
	project := makeKeyProject(t)
	key, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(t.TempDir(), "Library")
	if err := os.MkdirAll(library, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(library, "large.dat"), strings.Repeat("x", 1024*1024))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := NewStore(project)
	if _, err := store.Create(ctx, key, library); err == nil {
		t.Fatal("Create succeeded with canceled context")
	}
	entries, err := os.ReadDir(filepath.Join(store.Root(), "images"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("staging image was not cleaned: %s", entry.Name())
		}
	}
}

func TestStore_LockConflictDoesNotRemoveLiveLock(t *testing.T) {
	project := makeKeyProject(t)
	key, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(project)
	lock, err := store.acquireLock(key)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	if _, err := store.acquireLock(key); !errors.Is(err, errLockConflict) {
		t.Fatalf("second acquire error = %v, want lock conflict", err)
	}
	if _, err := os.Stat(lock.path); err != nil {
		t.Fatalf("live lock was removed: %v", err)
	}
}

func TestStore_EnsureLockConflictDoesNotRunBuilder(t *testing.T) {
	project := makeKeyProject(t)
	key, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(project)
	lock, err := store.acquireLock(key)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	builderCalled := false
	_, _, err = store.Ensure(context.Background(), key, func() (ImageSource, error) {
		builderCalled = true
		return ImageSource{}, nil
	})
	if !errors.Is(err, errLockConflict) {
		t.Fatalf("Ensure error = %v, want lock conflict", err)
	}
	if builderCalled {
		t.Fatal("builder ran despite an active generation lock")
	}
}

func TestStore_RemovesOnlyVerifiedStaleLock(t *testing.T) {
	project := makeKeyProject(t)
	key, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(project)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	store.pid = 1234
	store.processAlive = func(pid int) bool { return false }

	lockDir := filepath.Join(store.Root(), "locks")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lockDir, key.Digest+".lock")
	stale := lockRecord{
		PID:       9999,
		Token:     "stale-token",
		CreatedAt: now.Add(-staleLockAge - time.Minute),
	}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	lock, err := store.acquireLock(key)
	if err != nil {
		t.Fatalf("acquire after stale lock: %v", err)
	}
	defer lock.release()
	if lock.token == stale.Token {
		t.Fatal("stale lock token was reused")
	}
}

func TestStore_UnsupportedSchema(t *testing.T) {
	project := makeKeyProject(t)
	key, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatal(err)
	}
	key.SchemaVersion = "999"
	store := NewStore(project)
	resolution, err := store.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != StatusUnsupported {
		t.Fatalf("status = %q, want unsupported", resolution.Status)
	}
}
