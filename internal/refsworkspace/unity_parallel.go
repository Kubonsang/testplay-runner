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

const UnityParallelSchemaVersion = 1

type UnityParallelConfig struct {
	UnityPhase2Config
	WorkerCount int `json:"workerCount"`
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
	ReferenceAEdit bool `json:"referenceAEdit"`
	ReferenceBEdit bool `json:"referenceBEdit"`
	ReferenceAPlay bool `json:"referenceAPlay"`
	ReferenceBPlay bool `json:"referenceBPlay"`
	ABEdit         bool `json:"aBEdit"`
	ABPlay         bool `json:"aBPlay"`
	ExactTestSets  bool `json:"exactTestSets"`
}

type UnityParallelIsolation struct {
	WorkerAMarkerPath      string   `json:"workerAMarkerPath"`
	WorkerBMarkerPath      string   `json:"workerBMarkerPath"`
	WorkerAMarkerValue     string   `json:"workerAMarkerValue"`
	WorkerBMarkerValue     string   `json:"workerBMarkerValue"`
	WorkerAChangedEntries  []string `json:"workerAChangedEntries"`
	WorkerBChangedEntries  []string `json:"workerBChangedEntries"`
	WorkerAMarkerIsolated  bool     `json:"workerAMarkerIsolated"`
	WorkerBMarkerIsolated  bool     `json:"workerBMarkerIsolated"`
	BaselineUnchanged      bool     `json:"baselineUnchanged"`
	FixtureSourceUnchanged bool     `json:"fixtureSourceUnchanged"`
}

type UnityParallelIntermediate struct {
	Status                 string   `json:"status"`
	Residual               Residual `json:"residual"`
	ActiveReservationBytes int64    `json:"activeReservationBytes"`
	WorkerBValid           bool     `json:"workerBValid"`
	WorkerBJunctionValid   bool     `json:"workerBJunctionValid"`
	WorkerBMarkerValid     bool     `json:"workerBMarkerValid"`
	BaselineValid          bool     `json:"baselineValid"`
}

type UnityParallelStorageSnapshot struct {
	Name                   string          `json:"name"`
	MeasuredAt             time.Time       `json:"measuredAt"`
	RefsUsedBytes          *int64          `json:"refsUsedBytes,omitempty"`
	VHDX                   *FileUsage      `json:"vhdx,omitempty"`
	HostFreeBytes          *int64          `json:"hostFreeBytes,omitempty"`
	Baseline               *directoryUsage `json:"baseline,omitempty"`
	WorkerA                *directoryUsage `json:"workerA,omitempty"`
	WorkerB                *directoryUsage `json:"workerB,omitempty"`
	WorkerAFileCount       *int64          `json:"workerAFileCount,omitempty"`
	WorkerBFileCount       *int64          `json:"workerBFileCount,omitempty"`
	ActiveReservationBytes *int64          `json:"activeReservationBytes,omitempty"`
	ActiveMarkerCount      int             `json:"activeMarkerCount"`
	WorkerJournalCount     int             `json:"workerJournalCount"`
}

type UnityParallelStorageDeltas struct {
	CombinedWorkerLogicalBytes      *int64 `json:"combinedWorkerLogicalBytes,omitempty"`
	CombinedPhysicalAllocationDelta *int64 `json:"combinedPhysicalAllocationDelta,omitempty"`
	WorkerAUnityWriteDelta          *int64 `json:"workerAUnityWriteDelta,omitempty"`
	WorkerBUnityWriteDelta          *int64 `json:"workerBUnityWriteDelta,omitempty"`
	ReleaseAReclaimedBytes          *int64 `json:"releaseAReclaimedBytes,omitempty"`
	ReleaseBReclaimedBytes          *int64 `json:"releaseBReclaimedBytes,omitempty"`
	VHDXAllocatedGrowth             *int64 `json:"vhdxAllocatedGrowth,omitempty"`
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
	ReleaseMountedResidual    *Residual                        `json:"releaseMountedResidual,omitempty"`
	ReleaseDurableResidual    *Residual                        `json:"releaseDurableResidual,omitempty"`
	ReleaseDurability         *WorkerReleaseDurabilityEvidence `json:"releaseDurability,omitempty"`
	PoolStatus                *Result                          `json:"poolStatus,omitempty"`
	PoolRemove                *Result                          `json:"poolRemove,omitempty"`
	Storage                   []UnityParallelStorageSnapshot   `json:"storage"`
	StorageDeltas             *UnityParallelStorageDeltas      `json:"storageDeltas,omitempty"`
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
	mu         sync.Mutex
	events     map[string][]ParallelAcquireEvent
	cloneGate  chan struct{}
	cloneOnce  sync.Once
	cloneCount int
}

func newParallelAcquireRecorder(timeout time.Duration) *parallelAcquireRecorder {
	recorder := &parallelAcquireRecorder{events: make(map[string][]ParallelAcquireEvent), cloneGate: make(chan struct{})}
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
		if recorder.cloneCount == 2 {
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
	summary = &UnityParallelSummary{SchemaVersion: UnityParallelSchemaVersion, Status: "FAILED", Verdict: "FAILED", StartedAt: started.UTC(), WorkerCount: requested.WorkerCount, ExecutionMode: "two worker pipelines; EditMode rounds concurrent, then PlayMode rounds concurrent", Config: requested, Workers: make([]*UnityParallelWorkerEvidence, 2), CleanupState: "not-started"}
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
	leases := make([]*WorkerLease, 2)
	released := make([]bool, 2)
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
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "before-baseline", service, paths, "", "", ""))

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
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-baseline", service, paths, baseline.LibraryPath, "", ""))

	workspaces := []string{filepath.Join(artifactRoot, "workspaces", "worker-a"), filepath.Join(artifactRoot, "workspaces", "worker-b")}
	for _, workspace := range workspaces {
		if _, err := unityvhdxfixture.CopyFixtureProject(ctx, requested.FixturePath, workspace); err != nil {
			return summary, err
		}
	}
	leaseIDs := []string{"unity-phase2a-" + key.Digest[:16], "unity-phase2b-" + key.Digest[:16]}
	names := []string{"A", "B"}
	markers := []string{"worker-a-" + leaseIDs[0], "worker-b-" + leaseIDs[1]}
	manager, err := NewVerifiedWorkerManager(paths, store, NewNativeTreeCloner(), NewNativeJunctioner(), host, pool, volume)
	if err != nil {
		return summary, err
	}
	recorder := newParallelAcquireRecorder(requested.TestTimeout)
	manager.acquireHook = recorder.observe
	outcomes := make(chan parallelAcquireOutcome, 2)
	startGate := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(2)
	for index := 0; index < 2; index++ {
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
	for range 2 {
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
	summary.ConcurrentCloneObserved = acquireIntervalsOverlap(summary.Workers[0].AcquireEvents, summary.Workers[1].AcquireEvents, "clone-start", "clone-end")
	if !summary.ConcurrentCloneObserved {
		return summary, newError(CodeCloneFailed, "verify-concurrent-worker-clone", paths.Workers, nil)
	}
	workerTreesBefore := make([]map[string]phase2Entry, 2)
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
	if summary.Workers[0].InitialTree != summary.Workers[1].InitialTree || summary.Workers[0].Metadata.OwnershipToken == summary.Workers[1].Metadata.OwnershipToken {
		return summary, newError(CodeOwnershipMismatch, "verify-parallel-worker-identities", paths.Workers, nil)
	}
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-concurrent-acquire", service, paths, baseline.LibraryPath, filepath.Join(summary.Workers[0].Metadata.WorkerPath, "Library"), filepath.Join(summary.Workers[1].Metadata.WorkerPath, "Library")))

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

	parity := &UnityParallelParity{ExactTestSets: true}
	parity.ReferenceAEdit = unityvhdxfixture.CompareSemantic(*buildEvidence.ReferenceEditMode, editResults[0]) == nil
	parity.ReferenceBEdit = unityvhdxfixture.CompareSemantic(*buildEvidence.ReferenceEditMode, editResults[1]) == nil
	parity.ReferenceAPlay = unityvhdxfixture.CompareSemantic(*buildEvidence.ReferencePlayMode, playResults[0]) == nil
	parity.ReferenceBPlay = unityvhdxfixture.CompareSemantic(*buildEvidence.ReferencePlayMode, playResults[1]) == nil
	parity.ABEdit = unityvhdxfixture.CompareSemantic(editResults[0], editResults[1]) == nil
	parity.ABPlay = unityvhdxfixture.CompareSemantic(playResults[0], playResults[1]) == nil
	summary.Parity = parity
	_ = writePhase2JSON(artifactRoot, "parallel-parity.json", parity)
	if !parallelParityPassed(parity) {
		return summary, newError(CodePoolCorrupt, "compare-parallel-unity-semantic-parity", artifactRoot, nil)
	}

	isolation := &UnityParallelIsolation{}
	for index, evidence := range summary.Workers {
		afterTree, snapshotErr := snapshotPhase2Entries(ctx, filepath.Join(evidence.Metadata.WorkerPath, "Library"))
		if snapshotErr != nil {
			return summary, snapshotErr
		}
		evidence.ChangedEntries = phase2ChangedEntries(workerTreesBefore[index], afterTree)
		if len(evidence.ChangedEntries) == 0 {
			return summary, newError(CodePoolCorrupt, "verify-parallel-worker-library-write", evidence.Metadata.WorkerPath, nil)
		}
	}
	isolation.WorkerAChangedEntries, isolation.WorkerBChangedEntries = summary.Workers[0].ChangedEntries, summary.Workers[1].ChangedEntries
	isolation.WorkerAMarkerPath = filepath.Join(summary.Workers[0].Metadata.WorkerPath, "Library", "TestPlayVHDX", "marker.txt")
	isolation.WorkerBMarkerPath = filepath.Join(summary.Workers[1].Metadata.WorkerPath, "Library", "TestPlayVHDX", "marker.txt")
	markerA, markerAErr := os.ReadFile(isolation.WorkerAMarkerPath)
	markerB, markerBErr := os.ReadFile(isolation.WorkerBMarkerPath)
	isolation.WorkerAMarkerValue, isolation.WorkerBMarkerValue = string(markerA), string(markerB)
	isolation.WorkerAMarkerIsolated = markerAErr == nil && string(markerA) == markers[0] && string(markerA) != markers[1]
	isolation.WorkerBMarkerIsolated = markerBErr == nil && string(markerB) == markers[1] && string(markerB) != markers[0]
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
	_ = writePhase2JSON(artifactRoot, "mutual-isolation.json", phase2ResultOrError(isolation, errors.Join(markerAErr, markerBErr, baselineVerifyErr, baselineHashErr, metadataErr, completeErr, sourceErr)))
	if !isolation.WorkerAMarkerIsolated || !isolation.WorkerBMarkerIsolated || !isolation.BaselineUnchanged || !isolation.FixtureSourceUnchanged {
		return summary, newError(CodeOwnershipMismatch, "verify-parallel-worker-isolation", paths.Workers, nil)
	}
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-concurrent-unity", service, paths, baseline.LibraryPath, filepath.Join(summary.Workers[0].Metadata.WorkerPath, "Library"), filepath.Join(summary.Workers[1].Metadata.WorkerPath, "Library")))

	releaseA, err := leases[0].Release(ctx)
	summary.Workers[0].Release = &releaseA
	_ = writePhase2JSON(artifactRoot, "release-a.json", phase2ResultOrError(releaseA, err))
	if err != nil {
		return summary, err
	}
	released[0] = true
	if err := mounted.Flush(ctx); err != nil {
		return summary, newError(CodeCleanupFailed, "flush-after-release-a", volume.VolumeGUIDPath, err)
	}
	intermediate, err := validateOneWorkerRemaining(ctx, paths, store, baseline, summary.Workers[1], requested.Pool.WorkerReserveBytes)
	summary.IntermediateAfterReleaseA = &intermediate
	_ = writePhase2JSON(artifactRoot, "after-release-a.json", phase2ResultOrError(intermediate, err))
	if err != nil {
		return summary, err
	}
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-release-a", service, paths, baseline.LibraryPath, "", filepath.Join(summary.Workers[1].Metadata.WorkerPath, "Library")))

	releaseResult, err := runWorkerReleaseDurability(workerReleaseDurabilityOps{
		release: func() (WorkerMetrics, error) { return leases[1].Release(ctx) },
		measure: func() (Residual, error) { return measureMountedResidual(paths) },
		writeArtifact: func(artifact workerReleaseArtifact) error {
			return errors.Join(
				writePhase2JSON(artifactRoot, "release-b.json", artifact),
				writePhase2JSON(artifactRoot, "final-mounted-residual.json", artifact.MountedResidual),
			)
		},
		flush: func() error { return mounted.Flush(ctx) },
		beforeDetach: func() error {
			summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-release-b", service, paths, baseline.LibraryPath, "", ""))
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
	summary.Workers[1].Release = &releaseResult.Metrics
	summary.ReleaseMountedResidual = &releaseResult.MountedResidual
	summary.ReleaseDurability = &releaseResult.Durability
	if releaseResult.Durability.DurableResidual != nil {
		summary.ReleaseDurableResidual = releaseResult.Durability.DurableResidual
	}
	summary.PoolStatus, summary.PoolRemove = releaseResult.PoolStatus, releaseResult.PoolRemove
	_ = writePhase2JSON(artifactRoot, "final-mounted-residual.json", releaseResult.MountedResidual)
	if releaseResult.PoolStatus != nil {
		summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-durable-reattach", service, paths, baseline.LibraryPath, "", ""))
	}
	released[1] = releaseResult.Durability.ReleaseSucceeded
	if releaseResult.Durability.PoolRemoveSucceeded {
		poolExists = false
	}
	if err != nil {
		preservePool = poolExists
		return summary, err
	}
	summary.Storage = append(summary.Storage, measureUnityParallelStorage(ctx, "after-pool-remove", service, paths, "", "", ""))
	summary.StorageDeltas = calculateUnityParallelStorageDeltas(summary.Storage)
	summary.Status = "PASS"
	summary.Verdict = "UNITY_PHASE2_TWO_WORKERS_COMPATIBLE"
	summary.CleanupState = "released"
	return summary, nil
}

func validateUnityParallelConfig(requested UnityParallelConfig) (UnityParallelConfig, Paths, error) {
	if requested.WorkerCount != 2 {
		return requested, Paths{}, newError(CodeInvalidConfiguration, "validate-parallel-worker-count", fmt.Sprint(requested.WorkerCount), fmt.Errorf("exactly two workers are required"))
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

func runParallelUnityRound(ctx context.Context, config UnityParallelConfig, workers []*UnityParallelWorkerEvidence, platform string) ([2]unityvhdxfixture.PlatformResult, [2]ParallelUnityProcess, bool, error) {
	var results [2]unityvhdxfixture.PlatformResult
	var processes [2]ParallelUnityProcess
	startGate := make(chan struct{})
	var gateOnce sync.Once
	var gateMu sync.Mutex
	startedCount := 0
	time.AfterFunc(config.TestTimeout, func() { gateOnce.Do(func() { close(startGate) }) })
	outcomes := make(chan parallelRoundOutcome, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			process := ParallelUnityProcess{ExitCode: -1}
			executor := unityvhdxfixture.UnityExecutor{EditorPath: config.UnityEditorPath, Version: unityvhdxfixture.TargetUnityVersion, Marker: workers[index].Marker}
			executor.OnStart = func(pid int, at time.Time) {
				process.PID, process.StartedAt = pid, at
				gateMu.Lock()
				startedCount++
				if startedCount == 2 {
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
	for range 2 {
		outcome := <-outcomes
		results[outcome.index], processes[outcome.index] = outcome.result, outcome.process
		if outcome.err != nil {
			roundErrs = append(roundErrs, outcome.err)
		}
	}
	overlap := timeIntervalsOverlap(processes[0].StartedAt, processes[0].CompletedAt, processes[1].StartedAt, processes[1].CompletedAt)
	return results, processes, overlap, errors.Join(roundErrs...)
}

func validateParallelAcquire(paths Paths, workers []*UnityParallelWorkerEvidence, reserve int64) error {
	if len(workers) != 2 || workers[0] == nil || workers[1] == nil {
		return fmt.Errorf("two worker evidence records required")
	}
	for _, worker := range workers {
		if worker.Metadata.State != LeaseReady || !worker.JunctionVerified || worker.Metadata.Clone.FallbackUsed || worker.Metadata.Clone.ClonedBytes <= 0 || !worker.Metadata.Clone.RegularBlockCloneIOCTLAttempted || worker.Metadata.ReservedBytes != reserve {
			return newError(CodeCloneFailed, "validate-parallel-worker-acquire", worker.Metadata.WorkerPath, fmt.Errorf("state=%s junction=%t clone=%d fallback=%t reserve=%d", worker.Metadata.State, worker.JunctionVerified, worker.Metadata.Clone.ClonedBytes, worker.Metadata.Clone.FallbackUsed, worker.Metadata.ReservedBytes))
		}
	}
	if workers[0].LeaseID == workers[1].LeaseID || workers[0].Metadata.WorkerPath == workers[1].Metadata.WorkerPath || workers[0].Metadata.JunctionPath == workers[1].Metadata.JunctionPath || workers[0].Metadata.OwnershipToken == workers[1].Metadata.OwnershipToken {
		return newError(CodeOwnershipMismatch, "validate-parallel-worker-identity", paths.Workers, nil)
	}
	residual, err := measureMountedResidual(paths)
	if err != nil {
		return err
	}
	if residual.ActiveBaselineUses.Count != 2 || residual.WorkerLeaseJournals.Count != 2 || residual.WorkerDirectories.Count != 2 || residual.Junctions.Count != 2 || residual.WorkerStagingDirs.Count != 0 || residual.UnknownLeaseArtifacts.Count != 0 || residual.UnknownWorkerArtifacts.Count != 0 {
		return newError(CodePoolCorrupt, "validate-parallel-worker-residual", paths.PoolRoot, fmt.Errorf("residual=%+v", residual))
	}
	reservations, err := activeReservationBytes(paths.Leases)
	if err != nil || reservations != 2*reserve {
		return newError(CodeStorageBudgetExceeded, "validate-parallel-worker-reservations", paths.Leases, errors.Join(err, fmt.Errorf("reservations=%d expected=%d", reservations, 2*reserve)))
	}
	return nil
}

func validateOneWorkerRemaining(ctx context.Context, paths Paths, store *LibraryBaselineStore, baseline *Baseline, workerB *UnityParallelWorkerEvidence, reserve int64) (UnityParallelIntermediate, error) {
	result := UnityParallelIntermediate{Status: "EXPECTED_ONE_WORKER_REMAINING"}
	residual, err := measureMountedResidual(paths)
	result.Residual = residual
	if err != nil {
		return result, err
	}
	result.ActiveReservationBytes, err = activeReservationBytes(paths.Leases)
	if err != nil {
		return result, err
	}
	result.WorkerBValid = verifyWorkerOwner(workerB.Metadata.WorkerPath, workerB.Metadata) == nil && pathExists(filepath.Join(paths.Leases, "worker-"+workerB.LeaseID+".json"))
	result.WorkerBJunctionValid = verifyPhase2Junction(filepath.Join(workerB.Metadata.WorkerPath, "Library"), workerB.Metadata.JunctionPath) == nil
	marker, markerErr := os.ReadFile(filepath.Join(workerB.Metadata.WorkerPath, "Library", "TestPlayVHDX", "marker.txt"))
	result.WorkerBMarkerValid = markerErr == nil && string(marker) == workerB.Marker
	resolution, _, baselineErr := store.Verify(ctx, baseline)
	result.BaselineValid = baselineErr == nil && resolution.State == BaselineValid
	if residual.Status != "MOUNTED_MEASURED_NONZERO" || residual.ActiveBaselineUses.Count != 1 || residual.WorkerLeaseJournals.Count != 1 || residual.WorkerDirectories.Count != 1 || residual.Junctions.Count != 1 || residual.WorkerStagingDirs.Count != 0 || residual.UnknownLeaseArtifacts.Count != 0 || residual.UnknownWorkerArtifacts.Count != 0 || result.ActiveReservationBytes != reserve || !result.WorkerBValid || !result.WorkerBJunctionValid || !result.WorkerBMarkerValid || !result.BaselineValid {
		return result, newError(CodeCleanupFailed, "verify-release-a-leaves-worker-b", paths.PoolRoot, fmt.Errorf("intermediate=%+v", result))
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

func timeIntervalsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return !aStart.IsZero() && !aEnd.IsZero() && !bStart.IsZero() && !bEnd.IsZero() && aStart.Before(bEnd) && bStart.Before(aEnd)
}

func parallelParityPassed(parity *UnityParallelParity) bool {
	return parity != nil && parity.ReferenceAEdit && parity.ReferenceBEdit && parity.ReferenceAPlay && parity.ReferenceBPlay && parity.ABEdit && parity.ABPlay && parity.ExactTestSets
}

func measureUnityParallelStorage(ctx context.Context, name string, service *Service, paths Paths, baselinePath, workerAPath, workerBPath string) UnityParallelStorageSnapshot {
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
	result.WorkerA, result.WorkerAFileCount = measure(workerAPath)
	result.WorkerB, result.WorkerBFileCount = measure(workerBPath)
	return result
}

func calculateUnityParallelStorageDeltas(snapshots []UnityParallelStorageSnapshot) *UnityParallelStorageDeltas {
	byName := make(map[string]UnityParallelStorageSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byName[snapshot.Name] = snapshot
	}
	result := &UnityParallelStorageDeltas{}
	baseline, acquired := byName["after-baseline"], byName["after-concurrent-acquire"]
	unity, releaseA := byName["after-concurrent-unity"], byName["after-release-a"]
	releaseB, before := byName["after-release-b"], byName["before-baseline"]
	if acquired.WorkerA != nil && acquired.WorkerB != nil {
		value := acquired.WorkerA.LogicalBytes + acquired.WorkerB.LogicalBytes
		result.CombinedWorkerLogicalBytes = &value
	}
	if baseline.RefsUsedBytes != nil && acquired.RefsUsedBytes != nil {
		value := *acquired.RefsUsedBytes - *baseline.RefsUsedBytes
		result.CombinedPhysicalAllocationDelta = &value
	}
	if acquired.WorkerA != nil && unity.WorkerA != nil {
		value := unity.WorkerA.AllocatedBytes - acquired.WorkerA.AllocatedBytes
		result.WorkerAUnityWriteDelta = &value
	}
	if acquired.WorkerB != nil && unity.WorkerB != nil {
		value := unity.WorkerB.AllocatedBytes - acquired.WorkerB.AllocatedBytes
		result.WorkerBUnityWriteDelta = &value
	}
	if unity.RefsUsedBytes != nil && releaseA.RefsUsedBytes != nil {
		value := *unity.RefsUsedBytes - *releaseA.RefsUsedBytes
		result.ReleaseAReclaimedBytes = &value
	}
	if releaseA.RefsUsedBytes != nil && releaseB.RefsUsedBytes != nil {
		value := *releaseA.RefsUsedBytes - *releaseB.RefsUsedBytes
		result.ReleaseBReclaimedBytes = &value
	}
	if before.VHDX != nil && releaseB.VHDX != nil {
		value := releaseB.VHDX.AllocatedBytes - before.VHDX.AllocatedBytes
		result.VHDXAllocatedGrowth = &value
	}
	return result
}
