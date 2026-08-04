package refsworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

type fakeWorkerStorageMeter struct {
	used      int64
	hostFree  int64
	volumeErr error
	hostErr   error
}

func (meter *fakeWorkerStorageMeter) VolumeUsedBytes(context.Context) (int64, error) {
	return meter.used, meter.volumeErr
}
func (meter *fakeWorkerStorageMeter) HostFreeBytes(context.Context) (int64, error) {
	return meter.hostFree, meter.hostErr
}

func TestWorkerReservationIsAtomicAcrossConcurrentAcquires(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("2")
	if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("r", 8193)), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	meter := &fakeWorkerStorageMeter{used: 80, hostFree: 100 << 30}
	manager := NewWorkerManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{}, meter)
	start := make(chan struct{})
	type outcome struct {
		id    string
		lease *WorkerLease
		err   error
	}
	results := make(chan outcome, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, id := range []string{"lease-atom1", "lease-atom2"} {
		go func(id string) {
			ready.Done()
			<-start
			lease, _, err := manager.Acquire(context.Background(), WorkerRequest{
				Key: key, LeaseID: id, JunctionPath: filepath.Join(workspace, id), ClusterSize: 4096,
				SoftBudgetBytes: 110, ExpectedReserveBytes: 20, MinimumHostFreeBytes: 1,
			})
			results <- outcome{id: id, lease: lease, err: err}
		}(id)
	}
	ready.Wait()
	close(start)
	var winner *WorkerLease
	loserID := ""
	successes, budgetFailures := 0, 0
	for range 2 {
		result := <-results
		if result.err == nil {
			successes++
			winner = result.lease
		} else if ErrorCode(result.err) == CodeStorageBudgetExceeded {
			budgetFailures++
			loserID = result.id
		} else {
			t.Fatalf("unexpected acquire error: %v", result.err)
		}
	}
	if successes != 1 || budgetFailures != 1 {
		t.Fatalf("successes=%d budgetFailures=%d", successes, budgetFailures)
	}
	reservations, err := manager.checkLeases("lease-check")
	if err != nil || reservations != 20 {
		t.Fatalf("reservations=%d err=%v", reservations, err)
	}
	if pathExists(filepath.Join(paths.Workers, loserID)) || pathExists(filepath.Join(paths.Leases, "worker-"+loserID+".json")) || pathExists(filepath.Join(workspace, loserID)) {
		t.Fatalf("failed acquire left residual for %s", loserID)
	}
	if _, err := winner.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count, _ := countEntries(paths.Leases, "worker-", ".json"); count != 0 {
		t.Fatalf("worker lease residual=%d", count)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerHostFreeFloor(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	request := WorkerRequest{Key: testCompatibilityKey("3"), LeaseID: "lease-host1", JunctionPath: filepath.Join(t.TempDir(), "Library"), ClusterSize: 4096, SoftBudgetBytes: 100, ExpectedReserveBytes: 10, MinimumHostFreeBytes: 50}
	manager := NewWorkerManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{}, &fakeWorkerStorageMeter{hostFree: 49})
	if _, _, err := manager.Acquire(context.Background(), request); ErrorCode(err) != CodeHostFreeSpaceFloor {
		t.Fatalf("err=%v", err)
	}
	manager = NewWorkerManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{}, &fakeWorkerStorageMeter{hostFree: 50, hostErr: errors.New("measurement failed")})
	if _, _, err := manager.Acquire(context.Background(), request); ErrorCode(err) != CodeHostFreeSpaceFloor {
		t.Fatalf("err=%v", err)
	}
	manager = NewWorkerManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{}, &fakeWorkerStorageMeter{hostFree: 50})
	if _, _, err := manager.Acquire(context.Background(), request); ErrorCode(err) != CodeBaselineMissing {
		t.Fatalf("exact floor was not accepted: %v", err)
	}
}

type decreasingHostMeter struct {
	mu   sync.Mutex
	host []int64
}

func (meter *decreasingHostMeter) VolumeUsedBytes(context.Context) (int64, error) { return 0, nil }
func (meter *decreasingHostMeter) HostFreeBytes(context.Context) (int64, error) {
	meter.mu.Lock()
	defer meter.mu.Unlock()
	value := meter.host[0]
	if len(meter.host) > 1 {
		meter.host = meter.host[1:]
	}
	return value, nil
}

func TestParallelReservationsRemeasureDecreasingHostFree(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	meter := &decreasingHostMeter{host: []int64{50, 49}}
	manager := NewWorkerManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{}, meter)
	start := make(chan struct{})
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			<-start
			metadata := WorkerMetadata{SchemaVersion: LeaseSchemaVersion, LeaseID: fmt.Sprintf("lease-free%d", index), State: LeaseRequested, PID: manager.pid, OwnershipToken: strings.Repeat(fmt.Sprintf("%x", index+1), 64), ReservedBytes: 1}
			_, err := manager.reserveWorker(context.Background(), WorkerRequest{LeaseID: metadata.LeaseID, SoftBudgetBytes: 100, ExpectedReserveBytes: 1, MinimumHostFreeBytes: 50}, metadata)
			results <- err
		}(index)
	}
	close(start)
	successes, floorFailures := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if ErrorCode(err) == CodeHostFreeSpaceFloor {
			floorFailures++
		} else {
			t.Fatalf("err=%v", err)
		}
	}
	if successes != 1 || floorFailures != 1 {
		t.Fatalf("successes=%d floorFailures=%d", successes, floorFailures)
	}
	entries, _ := os.ReadDir(paths.Leases)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "worker-") {
			if err := os.Remove(filepath.Join(paths.Leases, entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestReservationLockCancellationPreservesLockEvidence(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{})
	lockPath := filepath.Join(paths.Leases, ".reservation.lock")
	if err := os.Mkdir(lockPath, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := manager.Acquire(ctx, WorkerRequest{Key: testCompatibilityKey("f"), LeaseID: "lease-lock1", JunctionPath: filepath.Join(t.TempDir(), "Library"), ClusterSize: 4096, SoftBudgetBytes: 100, ExpectedReserveBytes: 1, MinimumHostFreeBytes: 1})
	if ErrorCode(err) != CodeCancelled {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock evidence removed: %v", err)
	}
	if count, _ := countEntries(paths.Leases, "worker-", ".json"); count != 0 {
		t.Fatalf("lease residual=%d", count)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerReleaseResumesAfterEveryPersistedMilestone(t *testing.T) {
	stages := []string{"releasing", "junction-removed", "worker-quarantined", "worker-deleted", "active-use-released", "released", "before-lease-delete", "lease-deleted"}
	for index, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			paths := testPoolPaths(t)
			store := NewLibraryBaselineStore(paths)
			key := testCompatibilityKey(fmt.Sprintf("%x", index+7))
			if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
				return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("i", 8193)), 0600)
			}); err != nil {
				t.Fatal(err)
			}
			manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{})
			lease, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: fmt.Sprintf("lease-idem%d", index), JunctionPath: filepath.Join(t.TempDir(), "Library"), ClusterSize: 4096, SoftBudgetBytes: 1 << 30, ExpectedReserveBytes: 1 << 20, MinimumHostFreeBytes: 1})
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			manager.releaseHook = func(current string) error {
				if current == stage && !failed {
					failed = true
					return errors.New("injected release failure")
				}
				return nil
			}
			if _, err := lease.Release(context.Background()); ErrorCode(err) != CodeCleanupFailed {
				t.Fatalf("first release err=%v", err)
			}
			if _, err := lease.Release(context.Background()); err != nil {
				t.Fatalf("resumed release: %v", err)
			}
			if count, _ := countEntries(paths.Leases, "worker-", ".json"); count != 0 {
				t.Fatalf("lease residual=%d", count)
			}
			if count, _ := countEntries(paths.Leases, "active-", ".json"); count != 0 {
				t.Fatalf("active residual=%d", count)
			}
			if count, _ := countDirectories(paths.Workers); count != 0 {
				t.Fatalf("worker residual=%d", count)
			}
			if err := store.Clear(context.Background(), key); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkerReleaseRetriesReleasedJournalWriteAndLeaseDelete(t *testing.T) {
	for _, stage := range []string{"released-write", "lease-delete"} {
		t.Run(stage, func(t *testing.T) {
			paths := testPoolPaths(t)
			store := NewLibraryBaselineStore(paths)
			key := testCompatibilityKey("9")
			if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
				return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("r", 8193)), 0600)
			}); err != nil {
				t.Fatal(err)
			}
			manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{})
			lease, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: "lease-write1", JunctionPath: filepath.Join(t.TempDir(), "Library"), ClusterSize: 4096, SoftBudgetBytes: 1 << 30, ExpectedReserveBytes: 1 << 20, MinimumHostFreeBytes: 1})
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			if stage == "released-write" {
				manager.updateLeaseHook = func(metadata *WorkerMetadata) error {
					if metadata.State == LeaseReleased && !failed {
						failed = true
						return errors.New("released write failed")
					}
					return nil
				}
			}
			if stage == "lease-delete" {
				original := manager.removeLease
				manager.removeLease = func(path string) error {
					if !failed {
						failed = true
						return errors.New("lease delete failed")
					}
					return original(path)
				}
			}
			if _, err := lease.Release(context.Background()); err == nil {
				t.Fatal("first release unexpectedly succeeded")
			}
			if _, err := lease.Release(context.Background()); err != nil {
				t.Fatalf("retry failed: %v", err)
			}
			if count, _ := countEntries(paths.Leases, "worker-", ".json"); count != 0 {
				t.Fatalf("lease residual=%d", count)
			}
			if err := store.Clear(context.Background(), key); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func newWorkerTestManager(paths Paths, store *LibraryBaselineStore, cloner TreeCloner, junctions Junctioner) *WorkerManager {
	return NewWorkerManager(paths, store, cloner, junctions, &fakeWorkerStorageMeter{hostFree: 100 << 30})
}

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
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{})
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
	if metricsA.BaselineVerifyFileCount != 1 || metricsA.BaselineVerifyLogicalBytes != 8193 {
		t.Fatalf("baseline verification metrics=%+v", metricsA)
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
	meter := &fakeWorkerStorageMeter{used: 90, hostFree: 100 << 30}
	manager := NewWorkerManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{}, meter)
	_, _, err := manager.Acquire(context.Background(), WorkerRequest{
		Key:                  testCompatibilityKey("e"),
		LeaseID:              "lease-0003",
		JunctionPath:         filepath.Join(t.TempDir(), "Library"),
		ClusterSize:          4096,
		SoftBudgetBytes:      100,
		ExpectedReserveBytes: 20,
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
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{fallback: true}, symlinkJunctioner{})
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
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, failingJunctioner{err: errors.New("junction failure")})
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
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{})
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
