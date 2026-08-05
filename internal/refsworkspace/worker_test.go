package refsworkspace

import (
	"context"
	"encoding/json"
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

func (cloner copyClaimingCloner) CloneTree(ctx context.Context, request CloneRequest) (CloneMetrics, error) {
	if cloner.fail != nil {
		return CloneMetrics{FailedFileCount: 1}, cloner.fail
	}
	if request.TrustedRoot == "" || !PathWithin(request.TrustedRoot, request.Source) || !PathWithin(request.TrustedRoot, request.Destination) {
		return CloneMetrics{FailedFileCount: 1}, fmt.Errorf("clone request escaped trusted root: %+v", request)
	}
	source, destination, clusterSize := request.Source, request.Destination, request.ClusterSize
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
		CloneTreeMs:                     1,
		ClonedFileCount:                 1,
		ClonedBytes:                     aligned,
		PhysicalCopiedFileCount:         boolCount(tail > 0),
		PhysicalCopiedBytes:             tail,
		TailCopiedBytes:                 tail,
		FallbackUsed:                    cloner.fallback,
		RegularBlockCloneIOCTLAttempted: true,
		SparseBlockCloneIOCTLAttempted:  true,
		SparseClonedBytes:               aligned,
	}, nil
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
	policy := testWorkerPolicy()
	policy.MaximumBytes, policy.SoftBudgetBytes, policy.WorkerReserveBytes = 130, 110, 20
	manager := newWorkerManager(paths, store, copyClaimingCloner{}, testJunctioner{}, policy, meter)
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
			lease, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: id, JunctionPath: filepath.Join(workspace, id)})
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
	request := WorkerRequest{Key: testCompatibilityKey("3"), LeaseID: "lease-host1", JunctionPath: filepath.Join(t.TempDir(), "Library")}
	policy := testWorkerPolicy()
	policy.MaximumBytes, policy.SoftBudgetBytes, policy.WorkerReserveBytes, policy.MinimumHostFreeBytes = 110, 100, 10, 50
	manager := newWorkerManager(paths, store, copyClaimingCloner{}, testJunctioner{}, policy, &fakeWorkerStorageMeter{hostFree: 59})
	if _, _, err := manager.Acquire(context.Background(), request); ErrorCode(err) != CodeHostFreeSpaceFloor {
		t.Fatalf("err=%v", err)
	}
	manager = newWorkerManager(paths, store, copyClaimingCloner{}, testJunctioner{}, policy, &fakeWorkerStorageMeter{hostFree: 60, hostErr: errors.New("measurement failed")})
	if _, _, err := manager.Acquire(context.Background(), request); ErrorCode(err) != CodeHostFreeSpaceFloor {
		t.Fatalf("err=%v", err)
	}
	manager = newWorkerManager(paths, store, copyClaimingCloner{}, testJunctioner{}, policy, &fakeWorkerStorageMeter{hostFree: 60})
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
	meter := &decreasingHostMeter{host: []int64{51, 50}}
	policy := testWorkerPolicy()
	policy.MaximumBytes, policy.SoftBudgetBytes, policy.WorkerReserveBytes, policy.MinimumHostFreeBytes = 101, 100, 1, 50
	manager := newWorkerManager(paths, store, copyClaimingCloner{}, testJunctioner{}, policy, meter)
	start := make(chan struct{})
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			<-start
			metadata := WorkerMetadata{SchemaVersion: LeaseSchemaVersion, LeaseID: fmt.Sprintf("lease-free%d", index), State: LeaseRequested, PID: manager.pid, OwnershipToken: strings.Repeat(fmt.Sprintf("%x", index+1), 64), ReservedBytes: 1}
			_, err := manager.reserveWorker(context.Background(), WorkerRequest{LeaseID: metadata.LeaseID}, metadata)
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
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
	lockPath := filepath.Join(paths.Leases, ".reservation.lock")
	if err := os.Mkdir(lockPath, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := manager.Acquire(ctx, WorkerRequest{Key: testCompatibilityKey("f"), LeaseID: "lease-lock1", JunctionPath: filepath.Join(t.TempDir(), "Library")})
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
			manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
			lease, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: fmt.Sprintf("lease-idem%d", index), JunctionPath: filepath.Join(t.TempDir(), "Library")})
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
			manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
			lease, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: "lease-write1", JunctionPath: filepath.Join(t.TempDir(), "Library")})
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
	return newWorkerManager(paths, store, cloner, junctions, testWorkerPolicy(), &fakeWorkerStorageMeter{hostFree: 100 << 30})
}

func testWorkerPolicy() PoolPolicy {
	return PoolPolicy{MaximumBytes: 2 << 30, SoftBudgetBytes: 1 << 30, WorkerReserveBytes: 1 << 20, MinimumHostFreeBytes: 1, VHDXOverheadReserveBytes: 1, ClusterSize: 4096}
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
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
	request := func(id string) WorkerRequest {
		return WorkerRequest{
			Key:          key,
			LeaseID:      id,
			JunctionPath: filepath.Join(workspace, id+"-Library"),
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
	policy := testWorkerPolicy()
	policy.MaximumBytes, policy.SoftBudgetBytes, policy.WorkerReserveBytes = 120, 100, 20
	manager := newWorkerManager(paths, store, copyClaimingCloner{}, testJunctioner{}, policy, meter)
	_, _, err := manager.Acquire(context.Background(), WorkerRequest{
		Key:          testCompatibilityKey("e"),
		LeaseID:      "lease-0003",
		JunctionPath: filepath.Join(t.TempDir(), "Library"),
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
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{fallback: true}, testJunctioner{})
	_, _, err = manager.Acquire(context.Background(), WorkerRequest{
		Key:          key,
		LeaseID:      "lease-0004",
		JunctionPath: filepath.Join(t.TempDir(), "Library"),
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
		Key:          key,
		LeaseID:      "lease-0005",
		JunctionPath: filepath.Join(t.TempDir(), "Library"),
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
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
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

type stagingContractCloner struct {
	t     *testing.T
	paths Paths
}

type treeClonerFunc func(context.Context, CloneRequest) (CloneMetrics, error)

func (function treeClonerFunc) CloneTree(ctx context.Context, request CloneRequest) (CloneMetrics, error) {
	return function(ctx, request)
}

func (cloner stagingContractCloner) CloneTree(ctx context.Context, request CloneRequest) (CloneMetrics, error) {
	cloner.t.Helper()
	parent := filepath.Dir(request.Destination)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		cloner.t.Fatalf("clone parent is not an existing real directory: info=%v err=%v", info, err)
	}
	if _, err := os.Lstat(request.Destination); !os.IsNotExist(err) {
		cloner.t.Fatalf("clone destination must not exist: %v", err)
	}
	if request.TrustedRoot != cloner.paths.PoolRoot {
		cloner.t.Fatalf("trustedRoot=%q expected=%q", request.TrustedRoot, cloner.paths.PoolRoot)
	}
	if !strings.EqualFold(filepath.Clean(filepath.Dir(parent)), filepath.Clean(cloner.paths.Workers)) || !PathWithin(cloner.paths.PoolRoot, parent) {
		cloner.t.Fatalf("staging parent escaped workers root: %s", parent)
	}
	return (copyClaimingCloner{}).CloneTree(ctx, request)
}

func TestWorkerStagingRootContractAndSuccessLifecycle(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("a")
	if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("s", 8193)), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	manager := newWorkerTestManager(paths, store, stagingContractCloner{t: t, paths: paths}, testJunctioner{})
	leaseID := "lease-stage1"
	leasePath := filepath.Join(paths.Leases, "worker-"+leaseID+".json")
	var staging string
	originalMkdir := manager.mkdir
	manager.mkdir = func(path string, mode os.FileMode) error {
		data, err := os.ReadFile(leasePath)
		if err != nil {
			t.Fatalf("CLONING journal was not persisted before mkdir: %v", err)
		}
		var metadata WorkerMetadata
		if err := json.Unmarshal(data, &metadata); err != nil || metadata.State != LeaseCloning {
			t.Fatalf("journal=%+v err=%v", metadata, err)
		}
		staging = path
		return originalMkdir(path, mode)
	}
	junction := filepath.Join(t.TempDir(), "Library")
	lease, metrics, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: leaseID, JunctionPath: junction})
	if err != nil {
		t.Fatal(err)
	}
	metadata := lease.Metadata()
	if staging == "" || filepath.Dir(staging) != paths.Workers || !PathWithin(paths.PoolRoot, staging) {
		t.Fatalf("staging=%q", staging)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("committed staging remains: %v", err)
	}
	for _, path := range []string{metadata.WorkerPath, filepath.Join(metadata.WorkerPath, "Library"), filepath.Join(metadata.WorkerPath, workerOwnerFile)} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("committed worker path missing %s: %v", path, err)
		}
	}
	if metadata.State != LeaseReady || metrics.ClonedBytes != 8192 || !metadata.Clone.RegularBlockCloneIOCTLAttempted {
		t.Fatalf("metadata=%+v metrics=%+v", metadata, metrics)
	}
	if count, _ := countEntries(paths.Leases, "active-", ".json"); count != 1 {
		t.Fatalf("active use count=%d", count)
	}
	if _, err := lease.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertWorkerAcquireResidualZero(t, paths, junction)
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerStagingCollisionIsRejectedAndPreserved(t *testing.T) {
	paths, store, key := workerStagingTestFixture(t, "b")
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
	var collision string
	manager.mkdir = func(path string, mode os.FileMode) error {
		collision = path
		if err := os.Mkdir(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "foreign.txt"), []byte("foreign"), 0600); err != nil {
			t.Fatal(err)
		}
		return os.ErrExist
	}
	junction := filepath.Join(t.TempDir(), "Library")
	_, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: "lease-stage2", JunctionPath: junction})
	if ErrorCode(err) != CodeLeaseConflict {
		t.Fatalf("err=%v", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(collision, "foreign.txt")); readErr != nil || string(data) != "foreign" {
		t.Fatalf("collision was removed or changed: %q err=%v", data, readErr)
	}
	if count, _ := countEntries(paths.Leases, "worker-", ".json"); count != 0 {
		t.Fatalf("lease residual=%d", count)
	}
	if count, _ := countEntries(paths.Leases, "active-", ".json"); count != 0 {
		t.Fatalf("active residual=%d", count)
	}
	if err := os.RemoveAll(collision); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerStagingMkdirFailurePreservesPrimaryError(t *testing.T) {
	paths, store, key := workerStagingTestFixture(t, "c")
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
	primary := errors.New("mkdir injection")
	manager.mkdir = func(string, os.FileMode) error { return primary }
	junction := filepath.Join(t.TempDir(), "Library")
	_, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: "lease-stage3", JunctionPath: junction})
	if ErrorCode(err) != CodeCloneFailed || !errors.Is(err, primary) {
		t.Fatalf("err=%v", err)
	}
	assertWorkerAcquireResidualZero(t, paths, junction)
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCloneFailureRemovesOwnedStagingAndReferences(t *testing.T) {
	paths, store, key := workerStagingTestFixture(t, "d")
	primary := errors.New("clone injection")
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{fail: primary}, testJunctioner{})
	junction := filepath.Join(t.TempDir(), "Library")
	_, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: "lease-stage4", JunctionPath: junction})
	if ErrorCode(err) != CodeCloneFailed || !errors.Is(err, primary) {
		t.Fatalf("err=%v", err)
	}
	assertWorkerAcquireResidualZero(t, paths, junction)
	resolution, _, verifyErr := store.Resolve(context.Background(), key)
	if verifyErr != nil || resolution.State != BaselineValid {
		t.Fatalf("baseline state=%s err=%v", resolution.State, verifyErr)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRejectsReparseWorkersRootAndFinalCollision(t *testing.T) {
	t.Run("reparse-workers-root", func(t *testing.T) {
		paths, store, key := workerStagingTestFixture(t, "e")
		original := inspectPathReparse
		inspectPathReparse = func(path string) (bool, error) {
			if strings.EqualFold(filepath.Clean(path), filepath.Clean(paths.Workers)) {
				return true, nil
			}
			return original(path)
		}
		t.Cleanup(func() { inspectPathReparse = original })
		manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
		_, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: "lease-stage5", JunctionPath: filepath.Join(t.TempDir(), "Library")})
		if ErrorCode(err) != CodeOwnershipMismatch {
			t.Fatalf("err=%v", err)
		}
		if err := store.Clear(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("final-worker-collision", func(t *testing.T) {
		paths, store, key := workerStagingTestFixture(t, "f")
		workerPath := filepath.Join(paths.Workers, "lease-stage6")
		if err := os.Mkdir(workerPath, 0700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(workerPath, "foreign.txt")
		if err := os.WriteFile(marker, []byte("foreign"), 0600); err != nil {
			t.Fatal(err)
		}
		manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
		_, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: "lease-stage6", JunctionPath: filepath.Join(t.TempDir(), "Library")})
		if ErrorCode(err) != CodeLeaseConflict {
			t.Fatalf("err=%v", err)
		}
		if data, readErr := os.ReadFile(marker); readErr != nil || string(data) != "foreign" {
			t.Fatalf("final collision changed: %q err=%v", data, readErr)
		}
		if err := os.RemoveAll(workerPath); err != nil {
			t.Fatal(err)
		}
		if err := store.Clear(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	})
}

func TestWorkerAcquireFailureStagesCleanOwnedArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		wantCode  string
		configure func(*testing.T, *WorkerManager, Paths, error)
	}{
		{
			name: "before-clone-validation", wantCode: CodeLeaseConflict,
			configure: func(t *testing.T, manager *WorkerManager, _ Paths, _ error) {
				original := manager.mkdir
				manager.mkdir = func(path string, mode os.FileMode) error {
					if err := original(path, mode); err != nil {
						return err
					}
					return os.Mkdir(filepath.Join(path, "Library"), 0700)
				}
			},
		},
		{
			name: "clone-midway", wantCode: CodeCloneFailed,
			configure: func(_ *testing.T, manager *WorkerManager, _ Paths, primary error) {
				manager.cloner = treeClonerFunc(func(_ context.Context, request CloneRequest) (CloneMetrics, error) {
					if err := os.Mkdir(request.Destination, 0700); err != nil {
						return CloneMetrics{}, err
					}
					if err := os.WriteFile(filepath.Join(request.Destination, "partial.bin"), []byte("partial"), 0600); err != nil {
						return CloneMetrics{}, err
					}
					return CloneMetrics{FailedFileCount: 1}, primary
				})
			},
		},
		{
			name: "clone-metrics", wantCode: CodeBlockCloneUnavailable,
			configure: func(_ *testing.T, manager *WorkerManager, _ Paths, _ error) {
				manager.cloner = copyClaimingCloner{fallback: true}
			},
		},
		{
			name: "owner-write", wantCode: CodeCloneFailed,
			configure: func(_ *testing.T, manager *WorkerManager, _ Paths, primary error) {
				manager.writeFile = func(string, []byte, os.FileMode) error { return primary }
			},
		},
		{
			name: "make-writable", wantCode: CodeCloneFailed,
			configure: func(_ *testing.T, manager *WorkerManager, _ Paths, primary error) {
				manager.makeWritable = func(string) error { return primary }
			},
		},
		{
			name: "rename", wantCode: CodeCloneFailed,
			configure: func(_ *testing.T, manager *WorkerManager, _ Paths, primary error) {
				manager.rename = func(string, string) error { return primary }
			},
		},
		{
			name: "post-clone-baseline-verify", wantCode: CodeBaselineCorrupt,
			configure: func(_ *testing.T, manager *WorkerManager, _ Paths, primary error) {
				original := manager.resolveBaseline
				calls := 0
				manager.resolveBaseline = func(ctx context.Context, key CompatibilityKey) (BaselineResolution, BaselineMetrics, error) {
					calls++
					if calls == 2 {
						return BaselineResolution{}, BaselineMetrics{}, primary
					}
					return original(ctx, key)
				}
			},
		},
		{
			name: "junction-create", wantCode: CodeJunctionFailed,
			configure: func(_ *testing.T, manager *WorkerManager, _ Paths, primary error) {
				manager.junctions = failingJunctioner{err: primary}
			},
		},
		{
			name: "ready-journal", wantCode: CodeLeaseConflict,
			configure: func(_ *testing.T, manager *WorkerManager, _ Paths, primary error) {
				manager.updateLeaseHook = func(metadata *WorkerMetadata) error {
					if metadata.State == LeaseReady {
						return primary
					}
					return nil
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths, store, key := workerStagingTestFixture(t, "7")
			manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
			primary := errors.New("injected " + test.name)
			test.configure(t, manager, paths, primary)
			junction := filepath.Join(t.TempDir(), "Library")
			_, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: "lease-stage7", JunctionPath: junction})
			if ErrorCode(err) != test.wantCode {
				t.Fatalf("err=%v", err)
			}
			if test.name != "before-clone-validation" && test.name != "clone-metrics" && !errors.Is(err, primary) {
				t.Fatalf("primary error was not preserved: %v", err)
			}
			assertWorkerAcquireResidualZero(t, paths, junction)
			resolution, _, verifyErr := store.Resolve(context.Background(), key)
			if verifyErr != nil || resolution.State != BaselineValid {
				t.Fatalf("baseline state=%s err=%v", resolution.State, verifyErr)
			}
			if err := store.Clear(context.Background(), key); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func workerStagingTestFixture(t *testing.T, seed string) (Paths, *LibraryBaselineStore, CompatibilityKey) {
	t.Helper()
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey(seed)
	if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("x", 8193)), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	return paths, store, key
}

func assertWorkerAcquireResidualZero(t *testing.T, paths Paths, junction string) {
	t.Helper()
	residual, err := measureMountedResidual(paths)
	if err != nil {
		t.Fatal(err)
	}
	for name, metric := range map[string]ResidualMetric{
		"active": residual.ActiveBaselineUses, "journals": residual.WorkerLeaseJournals,
		"workers": residual.WorkerDirectories, "staging": residual.WorkerStagingDirs,
		"unknown-workers": residual.UnknownWorkerArtifacts, "quarantine": residual.QuarantineEntries,
		"reservation": residual.ReservationLocks, "junctions": residual.Junctions,
	} {
		if !metric.Measured || metric.Count != 0 {
			t.Fatalf("%s residual=%+v", name, metric)
		}
	}
	if _, err := os.Lstat(junction); !os.IsNotExist(err) {
		t.Fatalf("junction residual: %v", err)
	}
}
