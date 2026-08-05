package refsworkspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPhase2ChangedEntriesDetectsWorkerOnlyWrites(t *testing.T) {
	before := map[string]phase2Entry{"a": {Size: 1, ModifiedAt: 1, Digest: "one"}}
	after := map[string]phase2Entry{
		"a": {Size: 2, ModifiedAt: 2, Digest: "two"},
		"b": {Size: 1, ModifiedAt: 2, Digest: "new"},
	}
	changed := phase2ChangedEntries(before, after)
	if len(changed) != 2 || changed[0] != "a" || changed[1] != "b" {
		t.Fatalf("changed=%v", changed)
	}
}

func TestPhase2EntrySnapshotDetectsSourceMutation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "LibraryState.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := snapshotPhase2Entries(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after-worker-write"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := snapshotPhase2Entries(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if changed := phase2ChangedEntries(before, after); len(changed) != 1 || changed[0] != "LibraryState.txt" {
		t.Fatalf("changed=%v", changed)
	}
}

func TestValidateUnityPhase2ConfigRejectsArtifactInsidePool(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pool")
	fixture := filepath.Join(t.TempDir(), "fixture")
	for _, directory := range []string{filepath.Join(fixture, "Assets"), filepath.Join(fixture, "Packages"), filepath.Join(fixture, "ProjectSettings")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{
		filepath.Join(fixture, "Packages", "manifest.json"):             `{}`,
		filepath.Join(fixture, "Packages", "packages-lock.json"):        `{}`,
		filepath.Join(fixture, "ProjectSettings", "ProjectVersion.txt"): "m_EditorVersion: 6000.3.8f1\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	editor := filepath.Join(t.TempDir(), "Unity.exe")
	if err := os.WriteFile(editor, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := validateUnityPhase2Config(UnityPhase2Config{
		Pool:            Config{Root: root},
		UnityEditorPath: editor,
		FixturePath:     fixture,
		ArtifactRoot:    filepath.Join(root, "artifacts"),
		TestTimeout:     time.Minute,
	})
	if ErrorCode(err) != CodeInvalidConfiguration {
		t.Fatalf("err=%v", err)
	}
}

func TestCalculateUnityPhase2StorageDeltas(t *testing.T) {
	refsBaseline, refsAcquire, refsUnity, refsRelease := int64(400), int64(500), int64(900), int64(600)
	snapshots := []UnityPhase2StorageSnapshot{
		{Name: "before-baseline", VHDX: &FileUsage{AllocatedBytes: 100}},
		{Name: "after-baseline", Baseline: &directoryUsage{LogicalBytes: 1000, AllocatedBytes: 800}, RefsUsedBytes: &refsBaseline},
		{Name: "after-worker-acquire", Worker: &directoryUsage{LogicalBytes: 1000, AllocatedBytes: 100}, RefsUsedBytes: &refsAcquire},
		{Name: "after-unity", Worker: &directoryUsage{LogicalBytes: 1200, AllocatedBytes: 300}, RefsUsedBytes: &refsUnity},
		{Name: "after-worker-release", VHDX: &FileUsage{AllocatedBytes: 500}, RefsUsedBytes: &refsRelease},
	}
	deltas := calculateUnityPhase2StorageDeltas(snapshots)
	if *deltas.WorkerLogicalAmplificationBytes != 0 || *deltas.WorkerPhysicalAllocationDelta != 100 || *deltas.UnityLogicalWriteDelta != 200 || *deltas.UnityPhysicalWriteDelta != 200 || *deltas.RefsReclaimedAfterRelease != 300 || *deltas.VHDXAllocatedGrowthThroughRun != 400 {
		t.Fatalf("deltas=%+v", deltas)
	}
}
