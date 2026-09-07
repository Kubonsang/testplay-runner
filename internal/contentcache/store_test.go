package contentcache

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (Store, string, string, string) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	library := filepath.Join(project, "Library")
	if err := os.MkdirAll(filepath.Join(library, "PackageCache", "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "PackageCache", "file"), []byte("shared package bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	return New(filepath.Join(root, "store")), project, library, strings.Repeat("a", 64)
}
func TestDedupRestoreAndClearIsolation(t *testing.T) {
	ctx := context.Background()
	store, p, lib, key := fixture(t)
	p2 := filepath.Join(filepath.Dir(p), "second")
	if err := os.Mkdir(p2, 0700); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{p, p2} {
		if err := store.Publish(ctx, project, key, lib); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := store.Inspect(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ManifestCount != 2 || usage.ReferencedBytes != 2*usage.UniqueBytes {
		t.Fatalf("not deduplicated: %+v", usage)
	}
	target := filepath.Join(p2, "Library")
	if hit, err := store.Restore(ctx, p2, key, target); err != nil || !hit {
		t.Fatalf("restore: %v %v", hit, err)
	}
	file := filepath.Join(target, "PackageCache", "file")
	if err := os.WriteFile(file, []byte("independent edit"), 0600); err != nil {
		t.Fatal(err)
	}
	id, _ := Identity(p)
	m, err := store.readManifest(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entries {
		if e.Directory {
			continue
		}
		data, err := os.ReadFile(filepath.Join(store.Root, "blobs", e.Hash))
		if err != nil || string(data) != "shared package bytes" {
			t.Fatalf("blob changed: %s %v", data, err)
		}
	}
	if err := store.Clear(ctx, p); err != nil {
		t.Fatal(err)
	}
	usage, err = store.Inspect(ctx, true)
	if err != nil || usage.PrunedBytes != 0 {
		t.Fatalf("deleted another project: %+v %v", usage, err)
	}
	if err := store.Clear(ctx, p2); err != nil {
		t.Fatal(err)
	}
	usage, err = store.Inspect(ctx, true)
	if err != nil || usage.PrunedBytes == 0 {
		t.Fatalf("did not reclaim: %+v %v", usage, err)
	}
}
func TestFailurePreservesPublishedManifest(t *testing.T) {
	store, p, lib, key := fixture(t)
	ctx := context.Background()
	if err := store.Publish(ctx, p, key, lib); err != nil {
		t.Fatal(err)
	}
	id, _ := Identity(p)
	path, _ := store.manifestPath(id)
	before, _ := os.ReadFile(path)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Publish(cancelled, p, key, lib); err == nil {
		t.Fatal("cancelled publish succeeded")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("previous manifest lost")
	}
	hit, err := store.Restore(ctx, p, strings.Repeat("b", 64), filepath.Join(p, "miss"))
	if err != nil || hit {
		t.Fatalf("key mismatch reused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p, "miss")); !os.IsNotExist(err) {
		t.Fatal("cache miss created destination")
	}
}
func TestCorruptBlobAndTraversalFailClosed(t *testing.T) {
	store, p, lib, key := fixture(t)
	ctx := context.Background()
	if err := store.Publish(ctx, p, key, lib); err != nil {
		t.Fatal(err)
	}
	id, _ := Identity(p)
	m, _ := store.readManifest(id)
	for _, e := range m.Entries {
		if e.Directory {
			continue
		}
		if err := os.WriteFile(filepath.Join(store.Root, "blobs", e.Hash), []byte("broken package bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Restore(ctx, p, key, filepath.Join(p, "broken")); err == nil {
		t.Fatal("corruption ignored")
	}
	if err := store.Publish(ctx, p, key, lib); err == nil {
		t.Fatal("corrupt blob reused on publish")
	}
	m.Entries[0].Path = "../escape"
	data, _ := json.Marshal(m)
	path, _ := store.manifestPath(id)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Inspect(ctx, true); err == nil {
		t.Fatal("unsafe manifest allowed GC")
	}
}
func TestReaderPinsBlobsAndWriterCancellation(t *testing.T) {
	store, p, lib, key := fixture(t)
	ctx := context.Background()
	if err := store.Publish(ctx, p, key, lib); err != nil {
		t.Fatal(err)
	}
	unlock, err := store.lock(ctx, false, false)
	if err != nil {
		t.Fatal(err)
	}
	timeout, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	defer cancel()
	if err := store.Clear(timeout, p); err == nil {
		t.Fatal("writer ignored active reader")
	}
	unlock()
	if err := store.Clear(ctx, p); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentPublishRestoreAndGC(t *testing.T) {
	store, p, lib, key := fixture(t)
	ctx := context.Background()
	if err := store.Publish(ctx, p, key, lib); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Publish(ctx, p, key, lib); err != nil {
				t.Error(err)
			}
			if _, err := store.Inspect(ctx, true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if hit, err := store.Restore(ctx, p, key, filepath.Join(p, "restored")); err != nil || !hit {
		t.Fatalf("lost committed data: %v", err)
	}
}
func TestMissingUsageDoesNotCreateStoreAndRejectsContainment(t *testing.T) {
	store, p, _, _ := fixture(t)
	if _, err := store.Inspect(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.Root); !os.IsNotExist(err) {
		t.Fatal("read-only usage created store")
	}
	if err := New(filepath.Join(p, "cache")).ValidateLocation(p); err == nil {
		t.Fatal("nested store accepted")
	}
}
