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
	"time"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/unityvhdxfixture"
)

const UnityParallelSchemaVersion = 2

type UnityParallelConfig struct {
	UnityPhase2Config
	WorkerCount             int   `json:"workerCount"`
	SizingOnly              bool  `json:"sizingOnly"`
	BaselineSizingUsedBytes int64 `json:"baselineSizingUsedBytes,omitempty"`
}

type ParallelAcquireEvent struct {
	Stage string    `json:"stage"`
	At    time.Time `json:"at"`
}

type ParallelUnityProcess struct {
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	ExitCode    int       `json:"exitCode"`
	TimedOut    bool      `json:"timedOut"`
}

type UnityParallelWorkerEvidence struct {
	Name              string                           `json:"name"`
	LeaseID           string                           `json:"leaseId"`
	Workspace         string                           `json:"workspace"`
	Marker            string                           `json:"marker"`
	Metadata          WorkerMetadata                   `json:"metadata"`
	Metrics           WorkerMetrics                    `json:"metrics"`
	AcquireEvents     []ParallelAcquireEvent           `json:"acquireEvents"`
	AcquireStartedAt  time.Time                        `json:"acquireStartedAt"`
	AcquireEndedAt    time.Time                        `json:"acquireEndedAt"`
	AcquireDurationMs int64                            `json:"acquireDurationMs"`
	JunctionVerified  bool                             `json:"junctionVerified"`
	InitialTree       TreeInfo                         `json:"initialTree"`
	InitialUsage      directoryUsage                   `json:"initialUsage"`
	EditMode          *unityvhdxfixture.PlatformResult `json:"editMode,omitempty"`
	EditProcess       *ParallelUnityProcess            `json:"editProcess,omitempty"`
	PlayMode          *unityvhdxfixture.PlatformResult `json:"playMode,omitempty"`
	PlayProcess       *ParallelUnityProcess            `json:"playProcess,omitempty"`
	ChangedEntries    []string                         `json:"changedEntries,omitempty"`
	Release           *WorkerMetrics                   `json:"release,omitempty"`
}

type UnityParallelParity struct {
	ReferenceAEdit    bool                        `json:"referenceAEdit"`
	ReferenceBEdit    bool                        `json:"referenceBEdit"`
	ReferenceAPlay    bool                        `json:"referenceAPlay"`
	ReferenceBPlay    bool                        `json:"referenceBPlay"`
	ABEdit            bool                        `json:"aBEdit"`
	ABPlay            bool                        `json:"aBPlay"`
	ExactTestSets     bool                        `json:"exactTestSets"`
	Workers           []UnityParallelWorkerParity `json:"workers,omitempty"`
	AllReferenceEqual bool                        `json:"allReferenceEqual"`
	AllPairwiseEqual  bool                        `json:"allPairwiseEqual"`
}

type UnityParallelWorkerParity struct {
	Name          string `json:"name"`
	ReferenceEdit bool   `json:"referenceEdit"`
	ReferencePlay bool   `json:"referencePlay"`
	ExactTestSets bool   `json:"exactTestSets"`
}

type UnityParallelWorkerIsolation struct {
	Name           string   `json:"name"`
	MarkerPath     string   `json:"markerPath"`
	MarkerValue    string   `json:"markerValue"`
	MarkerIsolated bool     `json:"markerIsolated"`
	ChangedEntries []string `json:"changedEntries,omitempty"`
}

type UnityParallelIsolation struct {
	WorkerAMarkerPath      string                         `json:"workerAMarkerPath"`
	WorkerBMarkerPath      string                         `json:"workerBMarkerPath"`
	WorkerAMarkerValue     string                         `json:"workerAMarkerValue"`
	WorkerBMarkerValue     string                         `json:"workerBMarkerValue"`
	WorkerAChangedEntries  []string                       `json:"workerAChangedEntries"`
	WorkerBChangedEntries  []string                       `json:"workerBChangedEntries"`
	WorkerAMarkerIsolated  bool                           `json:"workerAMarkerIsolated"`
	WorkerBMarkerIsolated  bool                           `json:"workerBMarkerIsolated"`
	BaselineUnchanged      bool                           `json:"baselineUnchanged"`
	FixtureSourceUnchanged bool                           `json:"fixtureSourceUnchanged"`
	Workers                []UnityParallelWorkerIsolation `json:"workers,omitempty"`
	AllMarkersIsolated     bool                           `json:"allMarkersIsolated"`
}

type UnityParallelRemainingWorker struct {
	Name          string `json:"name"`
	LeaseID       string `json:"leaseId"`
	OwnerValid    bool   `json:"ownerValid"`
	JunctionValid bool   `json:"junctionValid"`
	MarkerValid   bool   `json:"markerValid"`
}

type UnityParallelIntermediate struct {
	Status                 string                         `json:"status"`
	Residual               Residual                       `json:"residual"`
	ActiveReservationBytes int64                          `json:"activeReservationBytes"`
	WorkerBValid           bool                           `json:"workerBValid"`
	WorkerBJunctionValid   bool                           `json:"workerBJunctionValid"`
	WorkerBMarkerValid     bool                           `json:"workerBMarkerValid"`
	BaselineValid          bool                           `json:"baselineValid"`
	ReleasedWorker         string                         `json:"releasedWorker,omitempty"`
	RemainingWorkerCount   int                            `json:"remainingWorkerCount"`
	RemainingWorkers       []UnityParallelRemainingWorker `json:"remainingWorkers,omitempty"`
}

type UnityParallelWorkerStorage struct {
	Name      string          `json:"name"`
	Usage     *directoryUsage `json:"usage,omitempty"`
	FileCount *int64          `json:"fileCount,omitempty"`
}

type UnityParallelStorageSnapshot struct {
	Name                   string                       `json:"name"`
	MeasuredAt             time.Time                    `json:"measuredAt"`
	RefsUsedBytes          *int64                       `json:"refsUsedBytes,omitempty"`
	VHDX                   *FileUsage                   `json:"vhdx,omitempty"`
	HostFreeBytes          *int64                       `json:"hostFreeBytes,omitempty"`
	Baseline               *directoryUsage              `json:"baseline,omitempty"`
	WorkerA                *directoryUsage              `json:"workerA,omitempty"`
	WorkerB                *directoryUsage              `json:"workerB,omitempty"`
	WorkerAFileCount       *int64                       `json:"workerAFileCount,omitempty"`
	WorkerBFileCount       *int64                       `json:"workerBFileCount,omitempty"`
	ActiveReservationBytes *int64                       `json:"activeReservationBytes,omitempty"`
	ActiveMarkerCount      int                          `json:"activeMarkerCount"`
	WorkerJournalCount     int                          `json:"workerJournalCount"`
	Workers                []UnityParallelWorkerStorage `json:"workers,omitempty"`
}

type UnityParallelStorageDeltas struct {
	CombinedWorkerLogicalBytes      *int64           `json:"combinedWorkerLogicalBytes,omitempty"`
	CombinedPhysicalAllocationDelta *int64           `json:"combinedPhysicalAllocationDelta,omitempty"`
	WorkerAUnityWriteDelta          *int64           `json:"workerAUnityWriteDelta,omitempty"`
	WorkerBUnityWriteDelta          *int64           `json:"workerBUnityWriteDelta,omitempty"`
	ReleaseAReclaimedBytes          *int64           `json:"releaseAReclaimedBytes,omitempty"`
	ReleaseBReclaimedBytes          *int64           `json:"releaseBReclaimedBytes,omitempty"`
	VHDXAllocatedGrowth             *int64           `json:"vhdxAllocatedGrowth,omitempty"`
	WorkerUnityWriteDeltas          map[string]int64 `json:"workerUnityWriteDeltas,omitempty"`
	ReleaseReclaimedBytes           []int64          `json:"releaseReclaimedBytes,omitempty"`
}

type UnityParallelSummary struct {
	SchemaVersion             int                              `json:"schemaVersion"`
	Status                    string                           `json:"status"`
	Verdict                   string                           `json:"verdict"`
	StartedAt                 time.Time                        `json:"startedAt"`
	CompletedAt               time.Time                        `json:"completedAt"`
	DurationMs                int64                            `json:"durationMs"`
	WorkerCount               int                              `json:"workerCount"`
	ExecutionMode             string                           `json:"executionMode"`
	Config                    UnityParallelConfig              `json:"config"`
	SourceBefore              *UnityPhase2SourceSnapshot       `json:"sourceBefore,omitempty"`
	SourceAfter               *UnityPhase2SourceSnapshot       `json:"sourceAfter,omitempty"`
	SourceUnchanged           *bool                            `json:"sourceUnchanged,omitempty"`
	Setup                     *Result                          `json:"setup,omitempty"`
	Baseline                  *UnityPhase2BaselineBuild        `json:"baseline,omitempty"`
	BaselineHash              *TreeInfo                        `json:"baselineHash,omitempty"`
	Workers                   []*UnityParallelWorkerEvidence   `json:"workers"`
	ConcurrentCloneObserved   bool                             `json:"concurrentCloneObserved"`
	ConcurrentEditObserved    bool                             `json:"concurrentEditObserved"`
	ConcurrentPlayObserved    bool                             `json:"concurrentPlayObserved"`
	Parity                    *UnityParallelParity             `json:"parity,omitempty"`
	Isolation                 *UnityParallelIsolation          `json:"isolation,omitempty"`
	IntermediateAfterReleaseA *UnityParallelIntermediate       `json:"intermediateAfterReleaseA,omitempty"`
	IntermediateReleases      []UnityParallelIntermediate      `json:"intermediateReleases,omitempty"`
	ReleaseMountedResidual    *Residual                        `json:"releaseMountedResidual,omitempty"`
	ReleaseDurableResidual    *Residual                        `json:"releaseDurableResidual,omitempty"`
	ReleaseDurability         *WorkerReleaseDurabilityEvidence `json:"releaseDurability,omitempty"`
	PoolStatus                *Result                          `json:"poolStatus,omitempty"`
	PoolRemove                *Result                          `json:"poolRemove,omitempty"`
	Storage                   []UnityParallelStorageSnapshot   `json:"storage"`
	StorageDeltas             *UnityParallelStorageDeltas      `json:"storageDeltas,omitempty"`
	Sizing                    *WorkerLadderSizingEvidence      `json:"sizing,omitempty"`
	CleanupState              string                           `json:"cleanupState"`
	Error                     string                           `json:"error,omitempty"`
}

type parallelAcquireOutcome struct {
	index   int
	lease   *WorkerLease
	metrics WorkerMetrics
	err     error
	start   time.Time
	end     time.Time
}

type parallelAcquireRecorder struct {
	mu          sync.Mutex
	events      map[string][]ParallelAcquireEvent
	cloneGate   chan struct{}
	cloneOnce   sync.Once
	cloneCount  int
	workerCount int
}

func newParallelAcquireRecorder(timeout time.Duration, workerCounts ...int) *parallelAcquireRecorder {
	workerCount := 2
	if len(workerCounts) > 0 {
		workerCount = workerCounts[0]
	}
	recorder := &parallelAcquireRecorder{events: make(map[string][]ParallelAcquireEvent), cloneGate: make(chan struct{}), workerCount: workerCount}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	time.AfterFunc(timeout, func() { recorder.cloneOnce.Do(func() { close(recorder.cloneGate) }) })
	return recorder
}

func (recorder *parallelAcquireRecorder) observe(leaseID, stage string) {
	recorder.mu.Lock()
	recorder.events[leaseID] = append(recorder.events[leaseID], ParallelAcquireEvent{Stage: stage, At: time.Now().UTC()})
	if stage == "clone-start" {
		recorder.cloneCount++
		if recorder.cloneCount == recorder.workerCount {
			recorder.cloneOnce.Do(func() { close(recorder.cloneGate) })
		}
	}
	recorder.mu.Unlock()
	if stage == "clone-start" {
		<-recorder.cloneGate
	}
}

func (recorder *parallelAcquireRecorder) forLease(leaseID string) []ParallelAcquireEvent {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]ParallelAcquireEvent(nil), recorder.events[leaseID]...)
}

// RunUnityParallel is a standalone experimental harness. It deliberately does
// not change the public testplay CLI or the single-worker Phase 2A contract.
func RunUnityParallel(ctx context.Context, requested UnityParallelConfig) (summary *UnityParallelSummary, returnErr error) {
	started := time.Now()
	artifactRoot := ""
	summary = &UnityParallelSummary{SchemaVersion: UnityParallelSchemaVersion, Status: "FAILED", Verdict: "FAILED", StartedAt: started.UTC(), WorkerCount: requested.WorkerCount, Config: requested, CleanupState: "not-started"}
	defer func() {
		summary.CompletedAt = time.Now().UTC()
		summary.DurationMs = time.Since(started).Milliseconds()
		if returnErr != nil {
			summary.Error = returnErr.Error()
		}
		_ = writePhase2JSON(artifactRoot, "summary.json", summary)
	}()

	config, paths, err := validateUnityParallelConfig(requested)
	if err != nil {
		return summary, err
	}
	requested, artifactRoot, summary.Config = config, config.ArtifactRoot, config
	summary.WorkerCount = config.WorkerCount
	summary.ExecutionMode = fmt.Sprintf("%d worker pipelines; EditMode rounds concurrent, then PlayMode rounds concurrent", config.WorkerCount)
	summary.Workers = make([]*UnityParallelWorkerEvidence, config.WorkerCount)
	if err := os.MkdirAll(artifactRoot, 0700); err != nil {
		return summary, newError(CodeInvalidConfiguration, "create-parallel-artifact-root", artifactRoot, err)
	}
	before, err := captureUnityPhase2Source(ctx, requested.FixturePath)
	if err != nil {
		return summary, err
	}
	if before.FixtureGitStatus != "" {
		return summary, newError(CodeInvalidConfiguration, "validate-fixture-git-status", requested.FixturePath, fmt.Errorf("fixture source is not clean: %s", before.FixtureGitStatus))
	}
	summary.SourceBefore = &before
	defer func() {
		if summary.SourceAfter != nil {
			return
		}
		after, snapshotErr := captureUnityPhase2Source(context.Background(), requested.FixturePath)
		if snapshotErr != nil {
			returnErr = errors.Join(returnErr, snapshotErr)
			return
		}
		summary.SourceAfter = &after
		unchanged := before == after
		summary.SourceUnchanged = &unchanged
	}()
	_ = writePhase2JSON(artifactRoot, "environment.json", map[string]any{"config": requested, "source": before, "executionMode": summary.ExecutionMode})

	baseExecutor := unityvhdxfixture.UnityExecutor{EditorPath: requested.UnityEditorPath, Version: unityvhdxfixture.TargetUnityVersion}
	versionCtx, cancelVersion := context.WithTimeout(ctx, requested.TestTimeout)
	err = baseExecutor.ValidateVersion(versionCtx, before.UnityVersion)
	cancelVersion()
	if err != nil {
		return summary, err
	}

	service := NewNativeService()
	setup, err := service.Setup(ctx, requested.Pool)
	summary.Setup = setup
	_ = writePhase2JSON(artifactRoot, "pool-setup.json", phase2ResultOrError(setup, err))
	if err != nil {
		return summary, err
	}
	poolExists := true
	var mounted MountedPool
	leases := make([]*WorkerLease, requested.WorkerCount)
	released := make([]bool, requested.WorkerCount)
	preservePool := false
	defer func() {
		var cleanupErr error
		for index := range leases {
			if leases[index] == nil || released[index] {
				continue
			}
			metrics, releaseErr := leases[index].Release(context.Background())
			if releaseErr == nil {
				released[index] = true
				if summary.Workers[index] != nil {
					summary.Workers[index].Release = &metrics
				}
			}
			cleanupErr = errors.Join(cleanupErr, releaseErr)
		}
		if mounted != nil {
			cleanupErr = errors.Join(cleanupErr, mounted.Flush(context.Background()))
			closeErr := closeMountedBounded(mounted)
			cleanupErr = errors.Join(cleanupErr, closeErr)
			mounted = nil
		}
		allReleased := true
		for index := range leases {
			allReleased = allReleased && (leases[index] == nil || released[index])
		}
		if poolExists && !preservePool && allReleased {
			removed, removeErr := service.Remove(context.Background(), requested.Pool)
			if removeErr == nil {
				summary.PoolRemove, poolExists = removed, false
			}
			cleanupErr = errors.Join(cleanupErr, removeErr)
		}
		if !poolExists && cleanupErr == nil {
			summary.CleanupState = "released"
		} else if poolExists && cleanupWasPreserved(cleanupErr) {
			summary.CleanupState = "preserved"
		} else {
			summary.CleanupState = "uncertain"
		}
		returnErr = errors.Join(returnErr, cleanupErr)
	}()

	var host, pool PoolMetadata
	mounted, host, pool, err = mountUnityPhase2Pool(ctx, service, paths)
	if err != nil {
		return summary, err
	}
	volume := mounted.Volume()
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "before-baseline", service, paths, "", nil))

	key, _, err := ComputeCompatibilityKey(ctx, CompatibilityOptions{ProjectPath: requested.FixturePath, UnityExecutable: requested.UnityEditorPath, BuildTarget: "StandaloneWindows64", ScriptingBackend: "Mono"})
	if err != nil {
		return summary, err
	}
	store := NewLibraryBaselineStore(paths)
	buildEvidence := &UnityPhase2BaselineBuild{CompatibilityKey: key}
	summary.Baseline = buildEvidence
	referenceWorkspace := filepath.Join(artifactRoot, "workspaces", "baseline-reference")
	referenceExecutor := baseExecutor
	referenceExecutor.Marker = "baseline-reference-" + key.Digest[:16]
	baseline, state, baselineMetrics, err := store.Ensure(ctx, key, func(buildCtx context.Context, libraryPath string) (buildErr error) {
		if _, copyErr := unityvhdxfixture.CopyFixtureProject(buildCtx, requested.FixturePath, referenceWorkspace); copyErr != nil {
			return copyErr
		}
		junctionPath := filepath.Join(referenceWorkspace, "Library")
		junctions := NewNativeJunctioner()
		if createErr := junctions.Create(libraryPath, junctionPath); createErr != nil {
			return createErr
		}
		defer func() {
			removeErr := junctions.Remove(libraryPath, junctionPath)
			buildEvidence.JunctionRemoved = removeErr == nil
			buildErr = errors.Join(buildErr, removeErr)
		}()
		edit, runErr := runParallelReferenceTest(buildCtx, requested.TestTimeout, referenceExecutor, referenceWorkspace, unityvhdxfixture.PlatformEditMode, filepath.Join(artifactRoot, "baseline-reference-editmode.xml"), filepath.Join(artifactRoot, "baseline-reference.log"))
		buildEvidence.ReferenceEditMode = &edit
		if runErr != nil {
			return runErr
		}
		if gateErr := unityvhdxfixture.RequireExpectedTests(edit, phase2EditModeTests); gateErr != nil {
			return gateErr
		}
		play, runErr := runParallelReferenceTest(buildCtx, requested.TestTimeout, referenceExecutor, referenceWorkspace, unityvhdxfixture.PlatformPlayMode, filepath.Join(artifactRoot, "baseline-reference-playmode.xml"), filepath.Join(artifactRoot, "baseline-reference-playmode.log"))
		buildEvidence.ReferencePlayMode = &play
		if runErr != nil {
			return runErr
		}
		return unityvhdxfixture.RequireExpectedTests(play, phase2PlayModeTests)
	})
	buildEvidence.StateBefore, buildEvidence.Metrics, buildEvidence.Baseline = state, baselineMetrics, baseline
	buildEvidence.Finalized = err == nil && baseline != nil
	_ = writePhase2JSON(artifactRoot, "baseline-build.json", phase2ResultOrError(buildEvidence, err))
	if err != nil {
		return summary, err
	}
	verifiedBefore, _, err := store.Verify(ctx, baseline)
	if err != nil || verifiedBefore.State != BaselineValid || countMatching(paths.Leases, "active-", ".json") != 0 {
		return summary, newError(CodeBaselineCorrupt, "verify-baseline-before-parallel-workers", baseline.Path, err)
	}
	baselineTreeBefore, err := HashTree(ctx, baseline.LibraryPath)
	if err != nil {
		return summary, err
	}
	summary.BaselineHash = &baselineTreeBefore
	baselineMetadataBefore, err := os.ReadFile(filepath.Join(baseline.Path, baselineMetadataFile))
	if err != nil {
		return summary, err
	}
	baselineCompleteBefore, err := os.ReadFile(filepath.Join(baseline.Path, baselineCompleteFile))
	if err != nil {
		return summary, err
	}
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-baseline", service, paths, baseline.LibraryPath, nil))
	usedAfterBaseline := int64(-1)
	if latest := summary.Storage[len(summary.Storage)-1]; latest.RefsUsedBytes != nil {
		usedAfterBaseline = *latest.RefsUsedBytes
	}
	sizingBasis := usedAfterBaseline
	if requested.BaselineSizingUsedBytes > 0 {
		sizingBasis = requested.BaselineSizingUsedBytes
	}
	sizing, sizingErr := CalculateWorkerLadderSizing(sizingBasis, requested.WorkerCount, requested.Pool.WorkerReserveBytes, requested.Pool.MinimumHostFreeBytes, requested.Pool.VHDXOverheadReserveBytes)
	summary.Sizing = &sizing
	_ = writePhase2JSON(artifactRoot, "baseline-sizing.json", phase2ResultOrError(sizing, sizingErr))
	if sizingErr != nil {
		return summary, sizingErr
	}
	if requested.BaselineSizingUsedBytes > 0 && requested.Pool.SoftBudgetBytes != sizing.CalculatedSoftBudgetBytes {
		return summary, newError(CodeInvalidConfiguration, "validate-worker-ladder-soft-budget", paths.PoolRoot, fmt.Errorf("configured=%d calculated=%d", requested.Pool.SoftBudgetBytes, sizing.CalculatedSoftBudgetBytes))
	}
	if requested.BaselineSizingUsedBytes > 0 {
		actualSizing, actualSizingErr := CalculateWorkerLadderSizing(usedAfterBaseline, requested.WorkerCount, requested.Pool.WorkerReserveBytes, requested.Pool.MinimumHostFreeBytes, requested.Pool.VHDXOverheadReserveBytes)
		if actualSizingErr != nil || requested.Pool.SoftBudgetBytes < actualSizing.CalculatedSoftBudgetBytes {
			return summary, newError(CodeStorageBudgetExceeded, "validate-worker-ladder-actual-budget", paths.PoolRoot, errors.Join(actualSizingErr, fmt.Errorf("configured=%d actualRequired=%d", requested.Pool.SoftBudgetBytes, actualSizing.CalculatedSoftBudgetBytes)))
		}
	}
	if summary.Storage[len(summary.Storage)-1].HostFreeBytes == nil || *summary.Storage[len(summary.Storage)-1].HostFreeBytes < sizing.RequiredHostFreeBytes {
		return summary, newError(CodeHostFreeSpaceFloor, "validate-worker-ladder-host-free", paths.Root, fmt.Errorf("required=%d", sizing.RequiredHostFreeBytes))
	}
	if requested.SizingOnly {
		if err := mounted.Flush(ctx); err != nil {
			return summary, err
		}
		if err := closeMountedBounded(mounted); err != nil {
			return summary, err
		}
		mounted = nil
		removed, err := service.Remove(ctx, requested.Pool)
		summary.PoolRemove = removed
		if err != nil {
			return summary, err
		}
		poolExists = false
		summary.Status, summary.Verdict, summary.CleanupState = "PASS", "BASELINE_SIZING_COMPLETE", "released"
		return summary, nil
	}

	workspaces := make([]string, requested.WorkerCount)
	leaseIDs := make([]string, requested.WorkerCount)
	names := make([]string, requested.WorkerCount)
	markers := make([]string, requested.WorkerCount)
	for index := 0; index < requested.WorkerCount; index++ {
		names[index] = parallelWorkerName(index)
		label := strings.ToLower(names[index])
		workspaces[index] = filepath.Join(artifactRoot, "workspaces", "worker-"+label)
		leaseIDs[index] = "unity-phase2" + label + "-" + key.Digest[:16]
		markers[index] = "worker-" + label + "-" + leaseIDs[index]
	}
	for _, workspace := range workspaces {
		if _, err := unityvhdxfixture.CopyFixtureProject(ctx, requested.FixturePath, workspace); err != nil {
			return summary, err
		}
	}
	manager, err := NewVerifiedWorkerManager(paths, store, NewNativeTreeCloner(), NewNativeJunctioner(), host, pool, volume)
	if err != nil {
		return summary, err
	}
	recorder := newParallelAcquireRecorder(requested.TestTimeout, requested.WorkerCount)
	manager.acquireHook = recorder.observe
	outcomes := make(chan parallelAcquireOutcome, requested.WorkerCount)
	startGate := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(requested.WorkerCount)
	for index := 0; index < requested.WorkerCount; index++ {
		go func(index int) {
			ready.Done()
			<-startGate
			acquireStart := time.Now().UTC()
			lease, metrics, acquireErr := manager.Acquire(ctx, WorkerRequest{Key: key, LeaseID: leaseIDs[index], JunctionPath: filepath.Join(workspaces[index], "Library")})
			outcomes <- parallelAcquireOutcome{index: index, lease: lease, metrics: metrics, err: acquireErr, start: acquireStart, end: time.Now().UTC()}
		}(index)
	}
	ready.Wait()
	close(startGate)
	var acquireErrs []error
	for range requested.WorkerCount {
		outcome := <-outcomes
		leases[outcome.index] = outcome.lease
		evidence := &UnityParallelWorkerEvidence{Name: names[outcome.index], LeaseID: leaseIDs[outcome.index], Workspace: workspaces[outcome.index], Marker: markers[outcome.index], Metrics: outcome.metrics, AcquireStartedAt: outcome.start, AcquireEndedAt: outcome.end, AcquireDurationMs: outcome.end.Sub(outcome.start).Milliseconds(), AcquireEvents: recorder.forLease(leaseIDs[outcome.index])}
		if outcome.lease != nil {
			evidence.Metadata = outcome.lease.Metadata()
			evidence.JunctionVerified = verifyPhase2Junction(filepath.Join(evidence.Metadata.WorkerPath, "Library"), evidence.Metadata.JunctionPath) == nil
		}
		summary.Workers[outcome.index] = evidence
		_ = writePhase2JSON(artifactRoot, "worker-"+strings.ToLower(names[outcome.index])+"-acquire.json", phase2ResultOrError(evidence, outcome.err))
		if outcome.err != nil {
			acquireErrs = append(acquireErrs, outcome.err)
		}
	}
	if err := errors.Join(acquireErrs...); err != nil {
		return summary, err
	}
	if err := validateParallelAcquire(paths, summary.Workers, requested.Pool.WorkerReserveBytes); err != nil {
		return summary, err
	}
	summary.ConcurrentCloneObserved = acquireGroupIntervalsOverlap(summary.Workers, "clone-start", "clone-end")
	if !summary.ConcurrentCloneObserved {
		return summary, newError(CodeCloneFailed, "verify-concurrent-worker-clone", paths.Workers, nil)
	}
	workerTreesBefore := make([]map[string]phase2Entry, requested.WorkerCount)
	for index, evidence := range summary.Workers {
		library := filepath.Join(evidence.Metadata.WorkerPath, "Library")
		workerTreesBefore[index], err = snapshotPhase2Entries(ctx, library)
		if err != nil {
			return summary, err
		}
		evidence.InitialTree, err = HashTree(ctx, library)
		if err != nil {
			return summary, err
		}
		usage, usageErr := shadow.MeasureDirectoryUsage(library)
		if usageErr != nil {
			return summary, usageErr
		}
		evidence.InitialUsage = directoryUsage{LogicalBytes: usage.LogicalBytes, AllocatedBytes: usage.AllocatedBytes}
	}
	if err := validateParallelInitialIdentities(summary.Workers); err != nil {
		return summary, newError(CodeOwnershipMismatch, "verify-parallel-worker-identities", paths.Workers, err)
	}
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-concurrent-acquire", service, paths, baseline.LibraryPath, parallelWorkerLibraryPaths(summary.Workers), parallelWorkerNames(summary.Workers)))

	for _, lease := range leases {
		if err := lease.MarkRunning(); err != nil {
			return summary, err
		}
	}
	editResults, editProcesses, editOverlap, err := runParallelUnityRound(ctx, requested, summary.Workers, unityvhdxfixture.PlatformEditMode)
	for index := range summary.Workers {
		summary.Workers[index].EditMode = &editResults[index]
		summary.Workers[index].EditProcess = &editProcesses[index]
	}
	summary.ConcurrentEditObserved = editOverlap
	if err != nil {
		return summary, err
	}
	playResults, playProcesses, playOverlap, err := runParallelUnityRound(ctx, requested, summary.Workers, unityvhdxfixture.PlatformPlayMode)
	for index := range summary.Workers {
		summary.Workers[index].PlayMode = &playResults[index]
		summary.Workers[index].PlayProcess = &playProcesses[index]
	}
	summary.ConcurrentPlayObserved = playOverlap
	if err != nil {
		return summary, err
	}
	if !editOverlap || !playOverlap {
		return summary, newError(CodePoolCorrupt, "verify-concurrent-unity-processes", artifactRoot, fmt.Errorf("edit=%t play=%t", editOverlap, playOverlap))
	}

	parity := buildParallelParity(*buildEvidence.ReferenceEditMode, *buildEvidence.ReferencePlayMode, summary.Workers, editResults, playResults)
	summary.Parity = parity
	_ = writePhase2JSON(artifactRoot, "parallel-parity.json", parity)
	if !parallelParityPassed(parity) {
		return summary, newError(CodePoolCorrupt, "compare-parallel-unity-semantic-parity", artifactRoot, nil)
	}

	isolation := &UnityParallelIsolation{AllMarkersIsolated: true}
	var isolationErrs []error
	for index, evidence := range summary.Workers {
		afterTree, snapshotErr := snapshotPhase2Entries(ctx, filepath.Join(evidence.Metadata.WorkerPath, "Library"))
		if snapshotErr != nil {
			return summary, snapshotErr
		}
		evidence.ChangedEntries = phase2ChangedEntries(workerTreesBefore[index], afterTree)
		if len(evidence.ChangedEntries) == 0 {
			return summary, newError(CodePoolCorrupt, "verify-parallel-worker-library-write", evidence.Metadata.WorkerPath, nil)
		}
		markerPath := filepath.Join(evidence.Metadata.WorkerPath, "Library", "TestPlayVHDX", "marker.txt")
		markerValue, markerErr := os.ReadFile(markerPath)
		markerIsolated := markerErr == nil && string(markerValue) == evidence.Marker
		for otherIndex, other := range summary.Workers {
			if otherIndex != index && string(markerValue) == other.Marker {
				markerIsolated = false
			}
		}
		isolation.Workers = append(isolation.Workers, UnityParallelWorkerIsolation{Name: evidence.Name, MarkerPath: markerPath, MarkerValue: string(markerValue), MarkerIsolated: markerIsolated, ChangedEntries: evidence.ChangedEntries})
		isolation.AllMarkersIsolated = isolation.AllMarkersIsolated && markerIsolated
		isolationErrs = append(isolationErrs, markerErr)
	}
	populateLegacyParallelIsolation(isolation)
	baselineAfter, _, baselineVerifyErr := store.Verify(ctx, baseline)
	baselineTreeAfter, baselineHashErr := HashTree(ctx, baseline.LibraryPath)
	metadataAfter, metadataErr := os.ReadFile(filepath.Join(baseline.Path, baselineMetadataFile))
	completeAfter, completeErr := os.ReadFile(filepath.Join(baseline.Path, baselineCompleteFile))
	isolation.BaselineUnchanged = baselineVerifyErr == nil && baselineHashErr == nil && metadataErr == nil && completeErr == nil && baselineAfter.State == BaselineValid && baselineTreeAfter == baselineTreeBefore && string(metadataAfter) == string(baselineMetadataBefore) && string(completeAfter) == string(baselineCompleteBefore)
	afterSource, sourceErr := captureUnityPhase2Source(ctx, requested.FixturePath)
	isolation.FixtureSourceUnchanged = sourceErr == nil && before == afterSource
	summary.SourceAfter = &afterSource
	summary.SourceUnchanged = &isolation.FixtureSourceUnchanged
	summary.Isolation = isolation
	_ = writePhase2JSON(artifactRoot, "mutual-isolation.json", phase2ResultOrError(isolation, errors.Join(append(isolationErrs, baselineVerifyErr, baselineHashErr, metadataErr, completeErr, sourceErr)...)))
	if !isolation.AllMarkersIsolated || !isolation.BaselineUnchanged || !isolation.FixtureSourceUnchanged {
		return summary, newError(CodeOwnershipMismatch, "verify-parallel-worker-isolation", paths.Workers, nil)
	}
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-concurrent-unity", service, paths, baseline.LibraryPath, parallelWorkerLibraryPaths(summary.Workers), parallelWorkerNames(summary.Workers)))

	for index := 0; index < requested.WorkerCount-1; index++ {
		label := strings.ToLower(summary.Workers[index].Name)
		releaseMetrics, releaseErr := leases[index].Release(ctx)
		summary.Workers[index].Release = &releaseMetrics
		_ = writePhase2JSON(artifactRoot, "release-"+label+".json", phase2ResultOrError(releaseMetrics, releaseErr))
		if releaseErr != nil {
			return summary, releaseErr
		}
		released[index] = true
		flushErr := mounted.Flush(ctx)
		_ = writePhase2JSON(artifactRoot, "flush-after-release-"+label+".json", map[string]any{"attempted": true, "succeeded": flushErr == nil, "error": workerLadderErrorString(flushErr)})
		if flushErr != nil {
			return summary, newError(CodeCleanupFailed, "flush-after-release-"+label, volume.VolumeGUIDPath, flushErr)
		}
		remaining := summary.Workers[index+1:]
		intermediate, intermediateErr := validateWorkersRemaining(ctx, paths, store, baseline, summary.Workers[index].Name, remaining, requested.Pool.WorkerReserveBytes)
		summary.IntermediateReleases = append(summary.IntermediateReleases, intermediate)
		if index == 0 && requested.WorkerCount == 2 {
			summary.IntermediateAfterReleaseA = &summary.IntermediateReleases[len(summary.IntermediateReleases)-1]
		}
		_ = writePhase2JSON(artifactRoot, "after-release-"+label+".json", phase2ResultOrError(intermediate, intermediateErr))
		if intermediateErr != nil {
			return summary, intermediateErr
		}
		summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-release-"+label, service, paths, baseline.LibraryPath, parallelWorkerLibraryPaths(remaining), parallelWorkerNames(remaining)))
	}

	lastIndex := requested.WorkerCount - 1
	lastLabel := strings.ToLower(summary.Workers[lastIndex].Name)
	releaseResult, err := runWorkerReleaseDurability(workerReleaseDurabilityOps{
		release: func() (WorkerMetrics, error) { return leases[lastIndex].Release(ctx) },
		measure: func() (Residual, error) { return measureMountedResidual(paths) },
		writeArtifact: func(artifact workerReleaseArtifact) error {
			return errors.Join(
				writePhase2JSON(artifactRoot, "release-"+lastLabel+".json", artifact),
				writePhase2JSON(artifactRoot, "final-mounted-residual.json", artifact.MountedResidual),
			)
		},
		flush: func() error { return mounted.Flush(ctx) },
		beforeDetach: func() error {
			summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-release-"+lastLabel, service, paths, baseline.LibraryPath, nil))
			return nil
		},
		detach: func() error {
			if closeErr := closeMountedBounded(mounted); closeErr != nil {
				return closeErr
			}
			mounted = nil
			return nil
		},
		status: func() (*Result, error) {
			status, statusErr := service.Status(ctx, requested.Pool)
			_ = writePhase2JSON(artifactRoot, "durable-status.json", phase2ResultOrError(status, statusErr))
			return status, statusErr
		},
		remove: func() (*Result, error) {
			removed, removeErr := service.Remove(ctx, requested.Pool)
			_ = writePhase2JSON(artifactRoot, "pool-remove.json", phase2ResultOrError(removed, removeErr))
			return removed, removeErr
		},
	})
	summary.Workers[lastIndex].Release = &releaseResult.Metrics
	summary.ReleaseMountedResidual = &releaseResult.MountedResidual
	summary.ReleaseDurability = &releaseResult.Durability
	if releaseResult.Durability.DurableResidual != nil {
		summary.ReleaseDurableResidual = releaseResult.Durability.DurableResidual
	}
	summary.PoolStatus, summary.PoolRemove = releaseResult.PoolStatus, releaseResult.PoolRemove
	_ = writePhase2JSON(artifactRoot, "final-mounted-residual.json", releaseResult.MountedResidual)
	if releaseResult.PoolStatus != nil {
		summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-durable-reattach", service, paths, baseline.LibraryPath, nil))
	}
	released[lastIndex] = releaseResult.Durability.ReleaseSucceeded
	if releaseResult.Durability.PoolRemoveSucceeded {
		poolExists = false
	}
	if err != nil {
		preservePool = poolExists
		return summary, err
	}
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-pool-remove", service, paths, "", nil))
	summary.StorageDeltas = calculateUnityParallelStorageDeltas(summary.Storage)
	summary.Status = "PASS"
	summary.Verdict = unityParallelVerdict(requested.WorkerCount)
	summary.CleanupState = "released"
	return summary, nil
}

func validateUnityParallelConfig(requested UnityParallelConfig) (UnityParallelConfig, Paths, error) {
	if !validParallelWorkerCount(requested.WorkerCount) {
		return requested, Paths{}, newError(CodeInvalidConfiguration, "validate-parallel-worker-count", fmt.Sprint(requested.WorkerCount), fmt.Errorf("worker count must be one of 2, 4, or 8"))
	}
	phase2, paths, err := validateUnityPhase2Config(requested.UnityPhase2Config)
	requested.UnityPhase2Config = phase2
	return requested, paths, err
}

func runParallelReferenceTest(ctx context.Context, timeout time.Duration, executor unityvhdxfixture.UnityExecutor, workspace, platform, results, log string) (unityvhdxfixture.PlatformResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return executor.RunTests(runCtx, workspace, platform, results, log)
}

type parallelRoundOutcome struct {
	index   int
	result  unityvhdxfixture.PlatformResult
	process ParallelUnityProcess
	err     error
}

func runParallelUnityRound(ctx context.Context, config UnityParallelConfig, workers []*UnityParallelWorkerEvidence, platform string) ([]unityvhdxfixture.PlatformResult, []ParallelUnityProcess, bool, error) {
	results := make([]unityvhdxfixture.PlatformResult, len(workers))
	processes := make([]ParallelUnityProcess, len(workers))
	startGate := make(chan struct{})
	var gateOnce sync.Once
	var gateMu sync.Mutex
	startedCount := 0
	gateTimeout := config.TestTimeout
	if gateTimeout > 30*time.Second {
		gateTimeout = 30 * time.Second
	}
	time.AfterFunc(gateTimeout, func() { gateOnce.Do(func() { close(startGate) }) })
	outcomes := make(chan parallelRoundOutcome, len(workers))
	for index := range workers {
		go func(index int) {
			process := ParallelUnityProcess{ExitCode: -1}
			executor := unityvhdxfixture.UnityExecutor{EditorPath: config.UnityEditorPath, Version: unityvhdxfixture.TargetUnityVersion, Marker: workers[index].Marker}
			executor.OnStart = func(pid int, at time.Time) {
				process.PID, process.StartedAt = pid, at
				gateMu.Lock()
				startedCount++
				if startedCount == len(workers) {
					gateOnce.Do(func() { close(startGate) })
				}
				gateMu.Unlock()
				<-startGate
			}
			label := strings.ToLower(workers[index].Name)
			mode := "editmode"
			if platform == unityvhdxfixture.PlatformPlayMode {
				mode = "playmode"
			}
			runCtx, cancel := context.WithTimeout(ctx, config.TestTimeout)
			result, runErr := executor.RunTests(runCtx, workers[index].Workspace, platform, filepath.Join(config.ArtifactRoot, "worker-"+label+"-"+mode+".xml"), filepath.Join(config.ArtifactRoot, "worker-"+label+"-"+mode+".log"))
			process.CompletedAt, process.ExitCode = time.Now().UTC(), result.ExitCode
			process.TimedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded)
			cancel()
			if runErr == nil {
				expected := phase2EditModeTests
				if platform == unityvhdxfixture.PlatformPlayMode {
					expected = phase2PlayModeTests
				}
				runErr = unityvhdxfixture.RequireExpectedTests(result, expected)
			}
			outcomes <- parallelRoundOutcome{index: index, result: result, process: process, err: runErr}
		}(index)
	}
	var roundErrs []error
	for range workers {
		outcome := <-outcomes
		results[outcome.index], processes[outcome.index] = outcome.result, outcome.process
		if outcome.err != nil {
			roundErrs = append(roundErrs, outcome.err)
		}
	}
	overlap := processGroupIntervalsOverlap(processes)
	return results, processes, overlap, errors.Join(roundErrs...)
}

func validateParallelAcquire(paths Paths, workers []*UnityParallelWorkerEvidence, reserve int64) error {
	if !validParallelWorkerCount(len(workers)) {
		return fmt.Errorf("2, 4, or 8 worker evidence records required")
	}
	leaseIDs, workerPaths, junctionPaths, ownershipTokens := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, worker := range workers {
		if worker == nil {
			return fmt.Errorf("nil worker evidence")
		}
		if worker.Metadata.State != LeaseReady || !worker.JunctionVerified || worker.Metadata.Clone.FallbackUsed || worker.Metadata.Clone.ClonedBytes <= 0 || !worker.Metadata.Clone.RegularBlockCloneIOCTLAttempted || worker.Metadata.ReservedBytes != reserve {
			return newError(CodeCloneFailed, "validate-parallel-worker-acquire", worker.Metadata.WorkerPath, fmt.Errorf("state=%s junction=%t clone=%d fallback=%t reserve=%d", worker.Metadata.State, worker.JunctionVerified, worker.Metadata.Clone.ClonedBytes, worker.Metadata.Clone.FallbackUsed, worker.Metadata.ReservedBytes))
		}
		if leaseIDs[worker.LeaseID] || workerPaths[worker.Metadata.WorkerPath] || junctionPaths[worker.Metadata.JunctionPath] || ownershipTokens[worker.Metadata.OwnershipToken] {
			return newError(CodeOwnershipMismatch, "validate-parallel-worker-identity", paths.Workers, nil)
		}
		leaseIDs[worker.LeaseID], workerPaths[worker.Metadata.WorkerPath], junctionPaths[worker.Metadata.JunctionPath], ownershipTokens[worker.Metadata.OwnershipToken] = true, true, true, true
	}
	residual, err := measureMountedResidual(paths)
	if err != nil {
		return err
	}
	workerCount := len(workers)
	if residual.ActiveBaselineUses.Count != workerCount || residual.WorkerLeaseJournals.Count != workerCount || residual.WorkerDirectories.Count != workerCount || residual.Junctions.Count != workerCount || residual.WorkerStagingDirs.Count != 0 || residual.UnknownLeaseArtifacts.Count != 0 || residual.UnknownWorkerArtifacts.Count != 0 {
		return newError(CodePoolCorrupt, "validate-parallel-worker-residual", paths.PoolRoot, fmt.Errorf("residual=%+v", residual))
	}
	reservations, err := activeReservationBytes(paths.Leases)
	expectedReservations := int64(workerCount) * reserve
	if err != nil || reservations != expectedReservations {
		return newError(CodeStorageBudgetExceeded, "validate-parallel-worker-reservations", paths.Leases, errors.Join(err, fmt.Errorf("reservations=%d expected=%d", reservations, expectedReservations)))
	}
	return nil
}

func validateOneWorkerRemaining(ctx context.Context, paths Paths, store *LibraryBaselineStore, baseline *Baseline, workerB *UnityParallelWorkerEvidence, reserve int64) (UnityParallelIntermediate, error) {
	return validateWorkersRemaining(ctx, paths, store, baseline, "A", []*UnityParallelWorkerEvidence{workerB}, reserve)
}

func validateWorkersRemaining(ctx context.Context, paths Paths, store *LibraryBaselineStore, baseline *Baseline, releasedWorker string, workers []*UnityParallelWorkerEvidence, reserve int64) (UnityParallelIntermediate, error) {
	result := UnityParallelIntermediate{ReleasedWorker: releasedWorker, RemainingWorkerCount: len(workers), Status: fmt.Sprintf("EXPECTED_%d_WORKERS_REMAINING", len(workers))}
	if len(workers) == 1 {
		result.Status = "EXPECTED_ONE_WORKER_REMAINING"
	}
	residual, err := measureMountedResidual(paths)
	result.Residual = residual
	if err != nil {
		return result, err
	}
	result.ActiveReservationBytes, err = activeReservationBytes(paths.Leases)
	if err != nil {
		return result, err
	}
	allWorkersValid := true
	for _, worker := range workers {
		remaining := UnityParallelRemainingWorker{Name: worker.Name, LeaseID: worker.LeaseID}
		remaining.OwnerValid = verifyWorkerOwner(worker.Metadata.WorkerPath, worker.Metadata) == nil && pathExists(filepath.Join(paths.Leases, "worker-"+worker.LeaseID+".json"))
		remaining.JunctionValid = verifyPhase2Junction(filepath.Join(worker.Metadata.WorkerPath, "Library"), worker.Metadata.JunctionPath) == nil
		marker, markerErr := os.ReadFile(filepath.Join(worker.Metadata.WorkerPath, "Library", "TestPlayVHDX", "marker.txt"))
		remaining.MarkerValid = markerErr == nil && string(marker) == worker.Marker
		allWorkersValid = allWorkersValid && remaining.OwnerValid && remaining.JunctionValid && remaining.MarkerValid
		result.RemainingWorkers = append(result.RemainingWorkers, remaining)
	}
	if len(result.RemainingWorkers) == 1 {
		result.WorkerBValid = result.RemainingWorkers[0].OwnerValid
		result.WorkerBJunctionValid = result.RemainingWorkers[0].JunctionValid
		result.WorkerBMarkerValid = result.RemainingWorkers[0].MarkerValid
	}
	resolution, _, baselineErr := store.Verify(ctx, baseline)
	result.BaselineValid = baselineErr == nil && resolution.State == BaselineValid
	remainingCount := len(workers)
	if residual.Status != "MOUNTED_MEASURED_NONZERO" || residual.ActiveBaselineUses.Count != remainingCount || residual.WorkerLeaseJournals.Count != remainingCount || residual.WorkerDirectories.Count != remainingCount || residual.Junctions.Count != remainingCount || residual.WorkerStagingDirs.Count != 0 || residual.UnknownLeaseArtifacts.Count != 0 || residual.UnknownWorkerArtifacts.Count != 0 || result.ActiveReservationBytes != int64(remainingCount)*reserve || !allWorkersValid || !result.BaselineValid {
		return result, newError(CodeCleanupFailed, "verify-release-leaves-expected-workers", paths.PoolRoot, fmt.Errorf("intermediate=%+v", result))
	}
	return result, nil
}

func activeReservationBytes(leases string) (int64, error) {
	entries, err := os.ReadDir(leases)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if !workerJournalPattern.MatchString(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(leases, entry.Name()))
		if err != nil {
			return 0, err
		}
		var metadata WorkerMetadata
		if err := json.Unmarshal(data, &metadata); err != nil {
			return 0, err
		}
		if metadata.State != LeaseReleased {
			total += metadata.ReservedBytes
		}
	}
	return total, nil
}

func acquireIntervalsOverlap(a, b []ParallelAcquireEvent, start, end string) bool {
	find := func(events []ParallelAcquireEvent, stage string) time.Time {
		for _, event := range events {
			if event.Stage == stage {
				return event.At
			}
		}
		return time.Time{}
	}
	return timeIntervalsOverlap(find(a, start), find(a, end), find(b, start), find(b, end))
}

func acquireGroupIntervalsOverlap(workers []*UnityParallelWorkerEvidence, start, end string) bool {
	intervals := make([][2]time.Time, 0, len(workers))
	for _, worker := range workers {
		if worker == nil {
			return false
		}
		var interval [2]time.Time
		for _, event := range worker.AcquireEvents {
			if event.Stage == start {
				interval[0] = event.At
			}
			if event.Stage == end {
				interval[1] = event.At
			}
		}
		intervals = append(intervals, interval)
	}
	return intervalsHaveCommonOverlap(intervals)
}

func processGroupIntervalsOverlap(processes []ParallelUnityProcess) bool {
	intervals := make([][2]time.Time, len(processes))
	for index, process := range processes {
		intervals[index] = [2]time.Time{process.StartedAt, process.CompletedAt}
	}
	return intervalsHaveCommonOverlap(intervals)
}

func intervalsHaveCommonOverlap(intervals [][2]time.Time) bool {
	if len(intervals) < 2 {
		return false
	}
	latestStart, earliestEnd := intervals[0][0], intervals[0][1]
	if latestStart.IsZero() || earliestEnd.IsZero() {
		return false
	}
	for _, interval := range intervals[1:] {
		if interval[0].IsZero() || interval[1].IsZero() {
			return false
		}
		if interval[0].After(latestStart) {
			latestStart = interval[0]
		}
		if interval[1].Before(earliestEnd) {
			earliestEnd = interval[1]
		}
	}
	return latestStart.Before(earliestEnd)
}

func timeIntervalsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return !aStart.IsZero() && !aEnd.IsZero() && !bStart.IsZero() && !bEnd.IsZero() && aStart.Before(bEnd) && bStart.Before(aEnd)
}

func parallelParityPassed(parity *UnityParallelParity) bool {
	return parity != nil && parity.AllReferenceEqual && parity.AllPairwiseEqual && parity.ExactTestSets
}

func buildParallelParity(referenceEdit, referencePlay unityvhdxfixture.PlatformResult, workers []*UnityParallelWorkerEvidence, editResults, playResults []unityvhdxfixture.PlatformResult) *UnityParallelParity {
	parity := &UnityParallelParity{ExactTestSets: len(workers) == len(editResults) && len(workers) == len(playResults), AllReferenceEqual: true, AllPairwiseEqual: true}
	for index, worker := range workers {
		workerParity := UnityParallelWorkerParity{Name: worker.Name, ReferenceEdit: unityvhdxfixture.CompareSemantic(referenceEdit, editResults[index]) == nil, ReferencePlay: unityvhdxfixture.CompareSemantic(referencePlay, playResults[index]) == nil, ExactTestSets: true}
		workerParity.ExactTestSets = workerParity.ReferenceEdit && workerParity.ReferencePlay
		parity.Workers = append(parity.Workers, workerParity)
		parity.AllReferenceEqual = parity.AllReferenceEqual && workerParity.ReferenceEdit && workerParity.ReferencePlay
	}
	for left := range workers {
		for right := left + 1; right < len(workers); right++ {
			parity.AllPairwiseEqual = parity.AllPairwiseEqual && unityvhdxfixture.CompareSemantic(editResults[left], editResults[right]) == nil && unityvhdxfixture.CompareSemantic(playResults[left], playResults[right]) == nil
		}
	}
	if len(workers) == 2 {
		parity.ReferenceAEdit, parity.ReferenceBEdit = parity.Workers[0].ReferenceEdit, parity.Workers[1].ReferenceEdit
		parity.ReferenceAPlay, parity.ReferenceBPlay = parity.Workers[0].ReferencePlay, parity.Workers[1].ReferencePlay
		parity.ABEdit = unityvhdxfixture.CompareSemantic(editResults[0], editResults[1]) == nil
		parity.ABPlay = unityvhdxfixture.CompareSemantic(playResults[0], playResults[1]) == nil
	}
	return parity
}

func populateLegacyParallelIsolation(isolation *UnityParallelIsolation) {
	if isolation == nil || len(isolation.Workers) != 2 {
		return
	}
	isolation.WorkerAMarkerPath, isolation.WorkerBMarkerPath = isolation.Workers[0].MarkerPath, isolation.Workers[1].MarkerPath
	isolation.WorkerAMarkerValue, isolation.WorkerBMarkerValue = isolation.Workers[0].MarkerValue, isolation.Workers[1].MarkerValue
	isolation.WorkerAChangedEntries, isolation.WorkerBChangedEntries = isolation.Workers[0].ChangedEntries, isolation.Workers[1].ChangedEntries
	isolation.WorkerAMarkerIsolated, isolation.WorkerBMarkerIsolated = isolation.Workers[0].MarkerIsolated, isolation.Workers[1].MarkerIsolated
}

func validParallelWorkerCount(count int) bool { return count == 2 || count == 4 || count == 8 }

func parallelWorkerName(index int) string { return string(rune('A' + index)) }

func unityParallelVerdict(count int) string {
	switch count {
	case 2:
		return "UNITY_PHASE2_TWO_WORKERS_COMPATIBLE"
	case 4:
		return "UNITY_PHASE2_FOUR_WORKERS_COMPATIBLE"
	case 8:
		return "UNITY_PHASE2_EIGHT_WORKERS_COMPATIBLE"
	default:
		return "FAILED"
	}
}

func validateParallelInitialIdentities(workers []*UnityParallelWorkerEvidence) error {
	if len(workers) == 0 || workers[0] == nil {
		return fmt.Errorf("worker evidence required")
	}
	tree := workers[0].InitialTree
	tokens := make(map[string]bool, len(workers))
	for _, worker := range workers {
		if worker == nil || worker.InitialTree != tree || worker.Metadata.OwnershipToken == "" || tokens[worker.Metadata.OwnershipToken] {
			return fmt.Errorf("worker tree or identity mismatch")
		}
		tokens[worker.Metadata.OwnershipToken] = true
	}
	return nil
}

func parallelWorkerLibraryPaths(workers []*UnityParallelWorkerEvidence) []string {
	paths := make([]string, 0, len(workers))
	for _, worker := range workers {
		if worker != nil && worker.Metadata.WorkerPath != "" {
			paths = append(paths, filepath.Join(worker.Metadata.WorkerPath, "Library"))
		}
	}
	return paths
}

func parallelWorkerNames(workers []*UnityParallelWorkerEvidence) []string {
	names := make([]string, 0, len(workers))
	for _, worker := range workers {
		if worker != nil {
			names = append(names, worker.Name)
		}
	}
	return names
}

func measureUnityParallelStorage(ctx context.Context, name string, service *Service, paths Paths, baselinePath string, workerPaths []string, workerNames ...[]string) UnityParallelStorageSnapshot {
	result := UnityParallelStorageSnapshot{Name: name, MeasuredAt: time.Now().UTC(), ActiveMarkerCount: countMatching(paths.Leases, "active-", ".json"), WorkerJournalCount: countMatching(paths.Leases, "worker-", ".json")}
	if pathExists(paths.Mount) {
		if used, err := newNativeWorkerStorageMeter(paths).VolumeUsedBytes(ctx); err == nil {
			result.RefsUsedBytes = &used
		}
		if reserved, err := activeReservationBytes(paths.Leases); err == nil {
			result.ActiveReservationBytes = &reserved
		}
	}
	if usage, err := service.native.FileUsage(paths.VHDX); err == nil {
		result.VHDX = &usage
	}
	if free, err := service.native.HostFreeBytes(paths.Root); err == nil {
		result.HostFreeBytes = &free
	} else if ancestor, _, findErr := nearestExistingAncestor(paths.Root); findErr == nil {
		if free, measureErr := service.native.HostFreeBytes(ancestor); measureErr == nil {
			result.HostFreeBytes = &free
		}
	}
	measure := func(path string) (*directoryUsage, *int64) {
		if path == "" {
			return nil, nil
		}
		usage, err := shadow.MeasureDirectoryUsage(path)
		if err != nil {
			return nil, nil
		}
		value := &directoryUsage{LogicalBytes: usage.LogicalBytes, AllocatedBytes: usage.AllocatedBytes}
		tree, err := HashTree(ctx, path)
		if err != nil {
			return value, nil
		}
		count := tree.FileCount
		return value, &count
	}
	result.Baseline, _ = measure(baselinePath)
	for index, workerPath := range workerPaths {
		usage, fileCount := measure(workerPath)
		workerName := parallelWorkerName(index)
		if len(workerNames) > 0 && index < len(workerNames[0]) {
			workerName = workerNames[0][index]
		}
		result.Workers = append(result.Workers, UnityParallelWorkerStorage{Name: workerName, Usage: usage, FileCount: fileCount})
	}
	if len(result.Workers) == 2 {
		result.WorkerA, result.WorkerAFileCount = result.Workers[0].Usage, result.Workers[0].FileCount
		result.WorkerB, result.WorkerBFileCount = result.Workers[1].Usage, result.Workers[1].FileCount
	}
	return result
}

func calculateUnityParallelStorageDeltas(snapshots []UnityParallelStorageSnapshot) *UnityParallelStorageDeltas {
	byName := make(map[string]UnityParallelStorageSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byName[snapshot.Name] = snapshot
	}
	result := &UnityParallelStorageDeltas{WorkerUnityWriteDeltas: make(map[string]int64)}
	baseline, acquired := byName["after-baseline"], byName["after-concurrent-acquire"]
	unity, before := byName["after-concurrent-unity"], byName["before-baseline"]
	if len(acquired.Workers) > 0 {
		var value int64
		for _, worker := range acquired.Workers {
			if worker.Usage != nil {
				value += worker.Usage.LogicalBytes
			}
		}
		result.CombinedWorkerLogicalBytes = &value
	}
	if baseline.RefsUsedBytes != nil && acquired.RefsUsedBytes != nil {
		value := *acquired.RefsUsedBytes - *baseline.RefsUsedBytes
		result.CombinedPhysicalAllocationDelta = &value
	}
	for index := 0; index < len(acquired.Workers) && index < len(unity.Workers); index++ {
		if acquired.Workers[index].Usage != nil && unity.Workers[index].Usage != nil {
			value := unity.Workers[index].Usage.AllocatedBytes - acquired.Workers[index].Usage.AllocatedBytes
			result.WorkerUnityWriteDeltas[acquired.Workers[index].Name] = value
			if len(acquired.Workers) == 2 && index == 0 {
				result.WorkerAUnityWriteDelta = &value
			}
			if len(acquired.Workers) == 2 && index == 1 {
				result.WorkerBUnityWriteDelta = &value
			}
		}
	}
	previous := unity
	for index := range acquired.Workers {
		release, ok := byName["after-release-"+strings.ToLower(parallelWorkerName(index))]
		if !ok {
			continue
		}
		if previous.RefsUsedBytes != nil && release.RefsUsedBytes != nil {
			value := *previous.RefsUsedBytes - *release.RefsUsedBytes
			result.ReleaseReclaimedBytes = append(result.ReleaseReclaimedBytes, value)
			if len(acquired.Workers) == 2 && index == 0 {
				result.ReleaseAReclaimedBytes = &value
			}
			if len(acquired.Workers) == 2 && index == 1 {
				result.ReleaseBReclaimedBytes = &value
			}
		}
		previous = release
	}
	if before.VHDX != nil && previous.VHDX != nil {
		value := previous.VHDX.AllocatedBytes - before.VHDX.AllocatedBytes
		result.VHDXAllocatedGrowth = &value
	}
	return result
}
