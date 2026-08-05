package refsworkspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParallelConcurrentAcquireAndIndependentRelease(t *testing.T) {
	paths := testPoolPaths(t)
	store := NewLibraryBaselineStore(paths)
	key := testCompatibilityKey("9")
	baseline, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("p", 8193)), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := testWorkerPolicy()
	policy.MaximumBytes, policy.SoftBudgetBytes, policy.WorkerReserveBytes = 20<<30, 14<<30, 2<<30
	manager := newWorkerManager(paths, store, copyClaimingCloner{}, testJunctioner{}, policy, &fakeWorkerStorageMeter{hostFree: 100 << 30})
	recorder := newParallelAcquireRecorder(time.Second)
	manager.acquireHook = recorder.observe
	workspace := t.TempDir()
	leaseIDs := []string{"parallel-a1", "parallel-b1"}
	start := make(chan struct{})
	type result struct {
		index   int
		lease   *WorkerLease
		metrics WorkerMetrics
		err     error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for index := range leaseIDs {
		go func(index int) {
			ready.Done()
			<-start
			lease, metrics, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: leaseIDs[index], JunctionPath: filepath.Join(workspace, leaseIDs[index])})
			results <- result{index: index, lease: lease, metrics: metrics, err: err}
		}(index)
	}
	ready.Wait()
	close(start)
	workers := make([]*UnityParallelWorkerEvidence, 2)
	leases := make([]*WorkerLease, 2)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		leases[result.index] = result.lease
		metadata := result.lease.Metadata()
		workers[result.index] = &UnityParallelWorkerEvidence{LeaseID: leaseIDs[result.index], Metadata: metadata, Metrics: result.metrics, JunctionVerified: true, AcquireEvents: recorder.forLease(leaseIDs[result.index])}
	}
	if err := validateParallelAcquire(paths, workers, policy.WorkerReserveBytes); err != nil {
		t.Fatal(err)
	}
	if !acquireIntervalsOverlap(workers[0].AcquireEvents, workers[1].AcquireEvents, "clone-start", "clone-end") {
		t.Fatal("clone intervals did not overlap")
	}
	for index := range leases {
		if err := leases[index].MarkRunning(); err != nil {
			t.Fatal(err)
		}
	}
	markerB := filepath.Join(workers[1].Metadata.WorkerPath, "Library", "TestPlayVHDX")
	if err := os.MkdirAll(markerB, 0700); err != nil {
		t.Fatal(err)
	}
	workers[1].Marker = "parallel-worker-b"
	if err := os.WriteFile(filepath.Join(markerB, "marker.txt"), []byte(workers[1].Marker), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := leases[0].Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	intermediate, err := validateOneWorkerRemaining(context.Background(), paths, store, baseline, workers[1], policy.WorkerReserveBytes)
	if err != nil {
		t.Fatal(err)
	}
	if intermediate.Status != "EXPECTED_ONE_WORKER_REMAINING" || intermediate.ActiveReservationBytes != 2<<30 {
		t.Fatalf("intermediate=%+v", intermediate)
	}
	if _, err := leases[1].Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	residual, err := measureMountedResidual(paths)
	if err != nil || residual.Status != "MOUNTED_MEASURED_ZERO" {
		t.Fatalf("residual=%+v err=%v", residual, err)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestParallelSameLeaseCollisionPreservesWinner(t *testing.T) {
	paths, store, key := workerStagingTestFixture(t, "a")
	manager := newWorkerTestManager(paths, store, copyClaimingCloner{}, testJunctioner{})
	start := make(chan struct{})
	type result struct {
		lease *WorkerLease
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			lease, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: key, LeaseID: "same-lease1", JunctionPath: filepath.Join(t.TempDir(), "Library")})
			results <- result{lease: lease, err: err}
		}()
	}
	close(start)
	var winner *WorkerLease
	successes, conflicts := 0, 0
	for range 2 {
		result := <-results
		if result.err == nil {
			successes++
			winner = result.lease
		} else if ErrorCode(result.err) == CodeLeaseConflict {
			conflicts++
		} else {
			t.Fatalf("unexpected error: %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 || winner == nil || !pathExists(winner.Metadata().WorkerPath) {
		t.Fatalf("successes=%d conflicts=%d winner=%v", successes, conflicts, winner)
	}
	if _, err := winner.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestParallelIntervalAndParityHelpers(t *testing.T) {
	now := time.Now()
	if !timeIntervalsOverlap(now, now.Add(2*time.Second), now.Add(time.Second), now.Add(3*time.Second)) {
		t.Fatal("expected overlap")
	}
	if timeIntervalsOverlap(now, now.Add(time.Second), now.Add(2*time.Second), now.Add(3*time.Second)) {
		t.Fatal("unexpected overlap")
	}
	if parallelParityPassed(&UnityParallelParity{ReferenceAEdit: true, ReferenceBEdit: true, ReferenceAPlay: true, ReferenceBPlay: true, ABEdit: true, ABPlay: true, ExactTestSets: true}) != true {
		t.Fatal("complete parity should pass")
	}
}

func TestValidateUnityParallelConfigRequiresExactlyTwo(t *testing.T) {
	_, _, err := validateUnityParallelConfig(UnityParallelConfig{WorkerCount: 1})
	if ErrorCode(err) != CodeInvalidConfiguration {
		t.Fatalf("err=%v", err)
	}
}
