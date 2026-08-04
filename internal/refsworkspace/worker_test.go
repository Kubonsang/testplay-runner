package refsworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

type copyClaimingCloner struct {
	fallback bool
	fail     error
}

func (cloner copyClaimingCloner) CloneTree(ctx context.Context, source, destination string, clusterSize int64) (CloneMetrics, error) {
	if cloner.fail != nil {
		return CloneMetrics{FailedFileCount: 1}, cloner.fail
	}
	if err := shadow.CopyDirParallel(ctx, source, destination, 2); err != nil {
		return CloneMetrics{}, err
	}
	usage, err := shadow.MeasureDirectoryUsage(destination)
	if err != nil {
		return CloneMetrics{}, err
	}
	aligned := usage.LogicalBytes / clusterSize * clusterSize
	tail := usage.LogicalBytes - aligned
	return CloneMetrics{
		CloneTreeMs:             1,
		ClonedFileCount:         1,
		ClonedBytes:             aligned,
		PhysicalCopiedFileCount: boolCount(tail > 0),
		PhysicalCopiedBytes:     tail,
		TailCopiedBytes:         tail,
		FallbackUsed:            cloner.fallback,
	}, nil
}

type symlinkJunctioner struct{}

func (symlinkJunctioner) Create(target, junction string) error { return os.Symlink(target, junction) }
func (symlinkJunctioner) Remove(target, junction string) error {
	resolved, err := filepath.EvalSymlinks(junction)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	if resolved != expected {
		return fmt.Errorf("target changed")
	}
	return os.Remove(junction)
}

type failingJunctioner struct{ err error }

func (junctions failingJunctioner) Create(string, string) error { return junctions.err }
func (junctions failingJunctioner) Remove(string, string) error { return junctions.err }

func TestWorkerAcquireIsolationReleaseAndResidual(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("d")
	baseline, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("b", 8193)), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	baselineBefore, err := HashTree(context.Background(), baseline.LibraryPath)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	manager := NewWorkerManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{})
	request := func(id string) WorkerRequest {
		return WorkerRequest{
			Key:                  key,
			LeaseID:              id,
			JunctionPath:         filepath.Join(workspace, id+"-Library"),
			ClusterSize:          4096,
			SoftBudgetBytes:      1 << 30,
			ExpectedReserveBytes: 1 << 20,
		}
	}
	workerA, metricsA, err := manager.Acquire(context.Background(), request("lease-0001"))
	if err != nil {
		t.Fatal(err)
	}
	workerB, _, err := manager.Acquire(context.Background(), request("lease-0002"))
	if err != nil {
		t.Fatal(err)
	}
	if metricsA.ClonedBytes != 8192 || metricsA.PhysicalCopiedBytes != 1 || metricsA.WorkerReadyLogicalBytes != 8193 {
		t.Fatalf("metrics=%+v", metricsA)
	}
	workerBPath := filepath.Join(workerB.metadata.WorkerPath, "Library", "artifact.bin")
	workerBBefore, err := os.ReadFile(workerBPath)
	if err != nil {
		t.Fatal(err)
	}
	workerAPath := filepath.Join(workerA.metadata.WorkerPath, "Library", "artifact.bin")
	if err := os.WriteFile(workerAPath, []byte("worker-a-private"), 0600); err != nil {
		t.Fatal(err)
	}
	baselineAfter, err := HashTree(context.Background(), baseline.LibraryPath)
	if err != nil {
		t.Fatal(err)
	}
	workerBAfter, err := os.ReadFile(workerBPath)
	if err != nil {
		t.Fatal(err)
	}
	if baselineAfter != baselineBefore || string(workerBAfter) != string(workerBBefore) {
		t.Fatal("worker A mutation reached baseline or worker B")
	}
	if _, err := workerA.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := workerB.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count, err := countDirectories(paths.Workers); err != nil || count != 0 {
		t.Fatalf("worker residual=%d err=%v", count, err)
	}
	if count, err := countEntries(paths.Leases, "active-", ".json"); err != nil || count != 0 {
		t.Fatalf("active residual=%d err=%v", count, err)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerBudgetFailsBeforeClone(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	manager := NewWorkerManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{})
	_, _, err := manager.Acquire(context.Background(), WorkerRequest{
		Key:                    testCompatibilityKey("e"),
		LeaseID:                "lease-0003",
		JunctionPath:           filepath.Join(t.TempDir(), "Library"),
		ClusterSize:            4096,
		SoftBudgetBytes:        100,
		ExpectedReserveBytes:   20,
		CurrentVolumeUsedBytes: 90,
	})
	if ErrorCode(err) != CodeStorageBudgetExceeded {
		t.Fatalf("err=%v", err)
	}
}

func TestWorkerRejectsSilentPhysicalFallback(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("f")
	_, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("x", 8193)), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewWorkerManager(paths, store, copyClaimingCloner{fallback: true}, symlinkJunctioner{})
	_, _, err = manager.Acquire(context.Background(), WorkerRequest{
		Key:                  key,
		LeaseID:              "lease-0004",
		JunctionPath:         filepath.Join(t.TempDir(), "Library"),
		ClusterSize:          4096,
		SoftBudgetBytes:      1 << 30,
		ExpectedReserveBytes: 1 << 20,
	})
	if ErrorCode(err) != CodeBlockCloneUnavailable {
		t.Fatalf("err=%v", err)
	}
	if count, _ := countDirectories(paths.Workers); count != 0 {
		t.Fatalf("worker residual=%d", count)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerJunctionFailureCleansOwnedWorkerAndReferences(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("1")
	_, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("x", 8193)), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewWorkerManager(paths, store, copyClaimingCloner{}, failingJunctioner{err: errors.New("junction failure")})
	_, _, err = manager.Acquire(context.Background(), WorkerRequest{
		Key:                  key,
		LeaseID:              "lease-0005",
		JunctionPath:         filepath.Join(t.TempDir(), "Library"),
		ClusterSize:          4096,
		SoftBudgetBytes:      1 << 30,
		ExpectedReserveBytes: 1 << 20,
	})
	if ErrorCode(err) != CodeJunctionFailed {
		t.Fatalf("err=%v", err)
	}
	if count, _ := countDirectories(paths.Workers); count != 0 {
		t.Fatalf("worker residual=%d", count)
	}
	if count, _ := countEntries(paths.Leases, "active-", ".json"); count != 0 {
		t.Fatalf("active reference residual=%d", count)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerDetectsOrphanLease(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	manager := NewWorkerManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{})
	manager.processAlive = func(int) bool { return false }
	orphan := WorkerMetadata{SchemaVersion: LeaseSchemaVersion, LeaseID: "lease-dead", State: LeaseRunning, PID: 999, OwnershipToken: strings.Repeat("0", 64), ReservedBytes: 10}
	if err := writeJSONAtomic(filepath.Join(paths.Leases, "worker-lease-dead.json"), orphan, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := manager.checkLeases("lease-new1")
	if ErrorCode(err) != CodeOrphanFound {
		t.Fatalf("err=%v", err)
	}
}

func boolCount(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
