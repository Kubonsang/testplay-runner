package refsworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/unityvhdxfixture"
)

const UnityPhase2SchemaVersion = 1

var (
	phase2EditModeTests = []string{
		"TestPlayFixture.Tests.LibraryMountTests.DeterministicRuntimeStateTest",
		"TestPlayFixture.Tests.LibraryMountTests.LibraryMountWriteReadTest",
	}
	phase2PlayModeTests = []string{
		"TestPlayFixture.Tests.DeterministicPlayModeTests.DeterministicPlayModeSmokeTest",
	}
)

type UnityPhase2Config struct {
	Pool            Config        `json:"pool"`
	UnityEditorPath string        `json:"unityEditorPath"`
	FixturePath     string        `json:"fixturePath"`
	ArtifactRoot    string        `json:"artifactRoot"`
	TestTimeout     time.Duration `json:"testTimeout"`
}

type UnityPhase2SourceSnapshot struct {
	RepositoryRoot     string   `json:"repositoryRoot"`
	Revision           string   `json:"revision"`
	Branch             string   `json:"branch"`
	GitStatus          string   `json:"gitStatus"`
	FixtureGitStatus   string   `json:"fixtureGitStatus"`
	UnityVersion       string   `json:"unityVersion"`
	Assets             TreeInfo `json:"assets"`
	Packages           TreeInfo `json:"packages"`
	ProjectSettings    TreeInfo `json:"projectSettings"`
	PackagesLockSHA256 string   `json:"packagesLockSha256"`
}

type UnityPhase2StorageSnapshot struct {
	Name            string          `json:"name"`
	MeasuredAt      time.Time       `json:"measuredAt"`
	RefsUsedBytes   *int64          `json:"refsUsedBytes,omitempty"`
	VHDX            *FileUsage      `json:"vhdx,omitempty"`
	HostFreeBytes   *int64          `json:"hostFreeBytes,omitempty"`
	Baseline        *directoryUsage `json:"baseline,omitempty"`
	Worker          *directoryUsage `json:"worker,omitempty"`
	WorkerFileCount *int64          `json:"workerFileCount,omitempty"`
}

type UnityPhase2StorageDeltas struct {
	WorkerLogicalAmplificationBytes *int64 `json:"workerLogicalAmplificationBytes,omitempty"`
	WorkerPhysicalAllocationDelta   *int64 `json:"workerPhysicalAllocationDelta,omitempty"`
	UnityLogicalWriteDelta          *int64 `json:"unityLogicalWriteDelta,omitempty"`
	UnityPhysicalWriteDelta         *int64 `json:"unityPhysicalWriteDelta,omitempty"`
	RefsReclaimedAfterRelease       *int64 `json:"refsReclaimedAfterRelease,omitempty"`
	VHDXAllocatedGrowthThroughRun   *int64 `json:"vhdxAllocatedGrowthThroughRun,omitempty"`
}

type directoryUsage struct {
	LogicalBytes   int64 `json:"logicalBytes"`
	AllocatedBytes int64 `json:"allocatedBytes"`
}

type UnityPhase2BaselineBuild struct {
	CompatibilityKey  CompatibilityKey                 `json:"compatibilityKey"`
	StateBefore       BaselineState                    `json:"stateBefore"`
	Metrics           BaselineMetrics                  `json:"metrics"`
	ReferenceEditMode *unityvhdxfixture.PlatformResult `json:"referenceEditMode,omitempty"`
	ReferencePlayMode *unityvhdxfixture.PlatformResult `json:"referencePlayMode,omitempty"`
	JunctionRemoved   bool                             `json:"junctionRemoved"`
	Finalized         bool                             `json:"finalized"`
	Baseline          *Baseline                        `json:"baseline,omitempty"`
}

type UnityPhase2WorkerEvidence struct {
	LeaseID          string                           `json:"leaseId"`
	Metadata         WorkerMetadata                   `json:"metadata"`
	Metrics          WorkerMetrics                    `json:"metrics"`
	Clone            CloneMetrics                     `json:"clone"`
	JunctionVerified bool                             `json:"junctionVerified"`
	EditMode         *unityvhdxfixture.PlatformResult `json:"editMode,omitempty"`
	PlayMode         *unityvhdxfixture.PlatformResult `json:"playMode,omitempty"`
	ChangedEntries   []string                         `json:"changedEntries,omitempty"`
}

type UnityPhase2Parity struct {
	EditModeEqual bool `json:"editModeEqual"`
	PlayModeEqual bool `json:"playModeEqual"`
	ExactTestSets bool `json:"exactTestSets"`
}

type UnityPhase2Summary struct {
	SchemaVersion          int                              `json:"schemaVersion"`
	Status                 string                           `json:"status"`
	Verdict                string                           `json:"verdict"`
	StartedAt              time.Time                        `json:"startedAt"`
	CompletedAt            time.Time                        `json:"completedAt"`
	DurationMs             int64                            `json:"durationMs"`
	Config                 UnityPhase2Config                `json:"config"`
	SourceBefore           *UnityPhase2SourceSnapshot       `json:"sourceBefore,omitempty"`
	SourceAfter            *UnityPhase2SourceSnapshot       `json:"sourceAfter,omitempty"`
	Setup                  *Result                          `json:"setup,omitempty"`
	Baseline               *UnityPhase2BaselineBuild        `json:"baseline,omitempty"`
	Worker                 *UnityPhase2WorkerEvidence       `json:"worker,omitempty"`
	Parity                 *UnityPhase2Parity               `json:"parity,omitempty"`
	Storage                []UnityPhase2StorageSnapshot     `json:"storage"`
	StorageDeltas          *UnityPhase2StorageDeltas        `json:"storageDeltas,omitempty"`
	Release                *WorkerMetrics                   `json:"release,omitempty"`
	ReleaseResidual        *Residual                        `json:"releaseResidual,omitempty"`
	ReleaseMountedResidual *Residual                        `json:"releaseMountedResidual,omitempty"`
	ReleaseDurableResidual *Residual                        `json:"releaseDurableResidual,omitempty"`
	ReleaseDurability      *WorkerReleaseDurabilityEvidence `json:"releaseDurability,omitempty"`
	PoolStatus             *Result                          `json:"poolStatus,omitempty"`
	PoolRemove             *Result                          `json:"poolRemove,omitempty"`
	SourceUnchanged        *bool                            `json:"sourceUnchanged,omitempty"`
	BaselineUnchanged      *bool                            `json:"baselineUnchanged,omitempty"`
	CleanupState           string                           `json:"cleanupState"`
	Error                  string                           `json:"error,omitempty"`
}

type phase2Entry struct {
	Size       int64
	ModifiedAt int64
	Digest     string
}

// RunUnityPhase2 is deliberately separate from the public product CLI. It
// exercises one verified production worker and records evidence outside the
// managed VHDX.
func RunUnityPhase2(ctx context.Context, requested UnityPhase2Config) (summary *UnityPhase2Summary, returnErr error) {
	started := time.Now()
	artifactRoot := ""
	summary = &UnityPhase2Summary{SchemaVersion: UnityPhase2SchemaVersion, Status: "FAILED", Verdict: "FAILED", StartedAt: started.UTC(), Config: requested, CleanupState: "not-started"}
	defer func() {
		summary.CompletedAt = time.Now().UTC()
		summary.DurationMs = time.Since(started).Milliseconds()
		if returnErr != nil {
			summary.Error = returnErr.Error()
		}
		_ = writePhase2JSON(artifactRoot, "summary.json", summary)
	}()

	config, paths, err := validateUnityPhase2Config(requested)
	if err != nil {
		return summary, err
	}
	requested = config
	artifactRoot = requested.ArtifactRoot
	summary.Config = config
	if err := os.MkdirAll(requested.ArtifactRoot, 0700); err != nil {
		return summary, newError(CodeInvalidConfiguration, "create-phase2-artifact-root", requested.ArtifactRoot, err)
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
		snapshotCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		after, snapshotErr := captureUnityPhase2Source(snapshotCtx, requested.FixturePath)
		if snapshotErr != nil {
			returnErr = errors.Join(returnErr, newError(CodeOwnershipMismatch, "capture-fixture-source-after-run", requested.FixturePath, snapshotErr))
			return
		}
		summary.SourceAfter = &after
		unchanged := before == after
		summary.SourceUnchanged = &unchanged
		if !unchanged {
			returnErr = errors.Join(returnErr, newError(CodeOwnershipMismatch, "verify-fixture-source-isolation", requested.FixturePath, nil))
		}
	}()
	_ = writePhase2JSON(requested.ArtifactRoot, "environment.json", map[string]any{"config": requested, "source": before})

	executor := unityvhdxfixture.UnityExecutor{EditorPath: requested.UnityEditorPath, Version: unityvhdxfixture.TargetUnityVersion}
	versionCtx, cancelVersion := context.WithTimeout(ctx, requested.TestTimeout)
	err = executor.ValidateVersion(versionCtx, before.UnityVersion)
	cancelVersion()
	if err != nil {
		return summary, err
	}

	service := NewNativeService()
	setup, err := service.Setup(ctx, requested.Pool)
	summary.Setup = setup
	_ = writePhase2JSON(requested.ArtifactRoot, "pool-setup.json", phase2ResultOrError(setup, err))
	if err != nil {
		var setupErr *Error
		if errors.As(err, &setupErr) && setupErr.CleanupState != "" {
			summary.CleanupState = setupErr.CleanupState
		}
		return summary, err
	}
	poolExists := true
	var mounted MountedPool
	var lease *WorkerLease
	workerReleased := false
	preservePool := false
	defer func() {
		var cleanupErr error
		detachedCleanly := true
		if lease != nil && !workerReleased {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			_, releaseErr := lease.Release(cleanupCtx)
			cancel()
			if releaseErr == nil {
				workerReleased = true
				if mounted != nil {
					flushCtx, flushCancel := context.WithTimeout(context.Background(), cleanupTimeout)
					flushErr := mounted.Flush(flushCtx)
					flushCancel()
					cleanupErr = errors.Join(cleanupErr, flushErr)
				}
			}
			cleanupErr = errors.Join(cleanupErr, releaseErr)
		}
		if mounted != nil {
			closeErr := closeMountedBounded(mounted)
			cleanupErr = errors.Join(cleanupErr, closeErr)
			detachedCleanly = closeErr == nil
			mounted = nil
		}
		if poolExists && !preservePool && (lease == nil || workerReleased) {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*cleanupTimeout)
			removed, removeErr := service.Remove(cleanupCtx, requested.Pool)
			cancel()
			if removeErr == nil && removed != nil {
				summary.PoolRemove = removed
				_ = writePhase2JSON(requested.ArtifactRoot, "pool-remove.json", removed)
				poolExists = false
			}
			cleanupErr = errors.Join(cleanupErr, removeErr)
		}
		if !poolExists && cleanupErr == nil {
			summary.CleanupState = "released"
		} else if poolExists && detachedCleanly && cleanupWasPreserved(cleanupErr) {
			summary.CleanupState = "preserved"
		} else {
			summary.CleanupState = "uncertain"
		}
		returnErr = errors.Join(returnErr, cleanupErr)
	}()

	mounted, host, pool, err := mountUnityPhase2Pool(ctx, service, paths)
	if err != nil {
		return summary, err
	}
	volume := mounted.Volume()
	summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "before-baseline", service, paths, "", ""))

	key, _, err := ComputeCompatibilityKey(ctx, CompatibilityOptions{ProjectPath: requested.FixturePath, UnityExecutable: requested.UnityEditorPath, BuildTarget: "StandaloneWindows64", ScriptingBackend: "Mono"})
	if err != nil {
		return summary, err
	}
	store := NewLibraryBaselineStore(paths)
	buildEvidence := &UnityPhase2BaselineBuild{CompatibilityKey: key}
	summary.Baseline = buildEvidence
	referenceWorkspace := filepath.Join(requested.ArtifactRoot, "workspaces", "baseline-reference")
	referenceMarker := "baseline-reference-" + key.Digest[:16]
	executor.Marker = referenceMarker
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
		editCtx, cancel := context.WithTimeout(buildCtx, requested.TestTimeout)
		edit, runErr := executor.RunTests(editCtx, referenceWorkspace, unityvhdxfixture.PlatformEditMode, filepath.Join(requested.ArtifactRoot, "baseline-reference-results.xml"), filepath.Join(requested.ArtifactRoot, "baseline-reference.log"))
		cancel()
		buildEvidence.ReferenceEditMode = &edit
		if runErr != nil {
			return runErr
		}
		if gateErr := unityvhdxfixture.RequireExpectedTests(edit, phase2EditModeTests); gateErr != nil {
			return gateErr
		}
		playCtx, cancel := context.WithTimeout(buildCtx, requested.TestTimeout)
		play, runErr := executor.RunTests(playCtx, referenceWorkspace, unityvhdxfixture.PlatformPlayMode, filepath.Join(requested.ArtifactRoot, "baseline-reference-playmode-results.xml"), filepath.Join(requested.ArtifactRoot, "baseline-reference-playmode.log"))
		cancel()
		buildEvidence.ReferencePlayMode = &play
		if runErr != nil {
			return runErr
		}
		return unityvhdxfixture.RequireExpectedTests(play, phase2PlayModeTests)
	})
	buildEvidence.StateBefore, buildEvidence.Metrics, buildEvidence.Baseline = state, baselineMetrics, baseline
	buildEvidence.Finalized = err == nil && baseline != nil
	_ = writePhase2JSON(requested.ArtifactRoot, "baseline-build.json", phase2ResultOrError(buildEvidence, err))
	if err != nil {
		return summary, err
	}
	verifiedBefore, _, err := store.Verify(ctx, baseline)
	if err != nil || verifiedBefore.State != BaselineValid || verifiedBefore.Baseline == nil {
		return summary, newError(CodeBaselineCorrupt, "verify-baseline-before-worker", baseline.Path, errors.Join(err, fmt.Errorf("state=%s", verifiedBefore.State)))
	}
	baselineTreeBefore, err := HashTree(ctx, baseline.LibraryPath)
	if err != nil {
		return summary, err
	}
	baselineMetadataBefore, err := os.ReadFile(filepath.Join(baseline.Path, baselineMetadataFile))
	if err != nil {
		return summary, err
	}
	baselineCompleteBefore, err := os.ReadFile(filepath.Join(baseline.Path, baselineCompleteFile))
	if err != nil {
		return summary, err
	}
	summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-baseline", service, paths, baseline.LibraryPath, ""))
	_ = writePhase2JSON(requested.ArtifactRoot, "baseline-before-worker.json", map[string]any{"baseline": verifiedBefore, "tree": baselineTreeBefore, "storage": summary.Storage[len(summary.Storage)-1], "activeUseCount": countMatching(paths.Leases, "active-", ".json")})

	workerWorkspace := filepath.Join(requested.ArtifactRoot, "workspaces", "worker-workspace")
	if _, err := unityvhdxfixture.CopyFixtureProject(ctx, requested.FixturePath, workerWorkspace); err != nil {
		return summary, err
	}
	manager, err := NewVerifiedWorkerManager(paths, store, NewNativeTreeCloner(), NewNativeJunctioner(), host, pool, volume)
	if err != nil {
		return summary, err
	}
	leaseID := "unity-phase2-" + key.Digest[:16]
	lease, workerMetrics, err := manager.Acquire(ctx, WorkerRequest{Key: key, LeaseID: leaseID, JunctionPath: filepath.Join(workerWorkspace, "Library")})
	if err != nil {
		_ = writePhase2JSON(requested.ArtifactRoot, "worker-acquire.json", phase2ResultOrError(map[string]any{"leaseId": leaseID, "metrics": workerMetrics}, err))
		baselineAfterFailure, _, verifyErr := store.Verify(ctx, baseline)
		baselineTreeAfterFailure, hashErr := HashTree(ctx, baseline.LibraryPath)
		unchanged := verifyErr == nil && hashErr == nil && baselineAfterFailure.State == BaselineValid && baselineTreeAfterFailure == baselineTreeBefore
		summary.BaselineUnchanged = &unchanged
		_ = writePhase2JSON(requested.ArtifactRoot, "baseline-after-worker.json", map[string]any{"baseline": baselineAfterFailure, "tree": baselineTreeAfterFailure, "unchanged": unchanged, "workerAcquireFailed": true})
		return summary, err
	}
	metadata := lease.Metadata()
	workerEvidence := &UnityPhase2WorkerEvidence{LeaseID: leaseID, Metadata: metadata, Metrics: workerMetrics, Clone: metadata.Clone}
	summary.Worker = workerEvidence
	if err := ValidateCloneMetrics(metadata.Clone); err != nil {
		return summary, err
	}
	workerEvidence.JunctionVerified = verifyPhase2Junction(filepath.Join(metadata.WorkerPath, "Library"), metadata.JunctionPath) == nil
	if !workerEvidence.JunctionVerified {
		return summary, newError(CodeJunctionFailed, "verify-worker-workspace-junction", metadata.JunctionPath, nil)
	}
	workerTreeBefore, err := snapshotPhase2Entries(ctx, filepath.Join(metadata.WorkerPath, "Library"))
	if err != nil {
		return summary, err
	}
	summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-worker-acquire", service, paths, baseline.LibraryPath, filepath.Join(metadata.WorkerPath, "Library")))
	_ = writePhase2JSON(requested.ArtifactRoot, "worker-acquire.json", workerEvidence)
	if err := lease.MarkRunning(); err != nil {
		return summary, err
	}
	executor.Marker = "worker-write-isolation-" + key.Digest[:16]
	editCtx, cancelEdit := context.WithTimeout(ctx, requested.TestTimeout)
	workerEdit, err := executor.RunTests(editCtx, workerWorkspace, unityvhdxfixture.PlatformEditMode, filepath.Join(requested.ArtifactRoot, "worker-editmode-results.xml"), filepath.Join(requested.ArtifactRoot, "worker-editmode.log"))
	cancelEdit()
	workerEvidence.EditMode = &workerEdit
	if err != nil {
		return summary, err
	}
	if err := unityvhdxfixture.RequireExpectedTests(workerEdit, phase2EditModeTests); err != nil {
		return summary, err
	}
	playCtx, cancelPlay := context.WithTimeout(ctx, requested.TestTimeout)
	workerPlay, err := executor.RunTests(playCtx, workerWorkspace, unityvhdxfixture.PlatformPlayMode, filepath.Join(requested.ArtifactRoot, "worker-playmode-results.xml"), filepath.Join(requested.ArtifactRoot, "worker-playmode.log"))
	cancelPlay()
	workerEvidence.PlayMode = &workerPlay
	if err != nil {
		return summary, err
	}
	if err := unityvhdxfixture.RequireExpectedTests(workerPlay, phase2PlayModeTests); err != nil {
		return summary, err
	}
	parity := &UnityPhase2Parity{ExactTestSets: true}
	parity.EditModeEqual = unityvhdxfixture.CompareSemantic(*buildEvidence.ReferenceEditMode, workerEdit) == nil
	parity.PlayModeEqual = unityvhdxfixture.CompareSemantic(*buildEvidence.ReferencePlayMode, workerPlay) == nil
	summary.Parity = parity
	_ = writePhase2JSON(requested.ArtifactRoot, "parity.json", parity)
	if !parity.EditModeEqual || !parity.PlayModeEqual {
		return summary, newError(CodePoolCorrupt, "compare-unity-semantic-parity", workerWorkspace, fmt.Errorf("edit=%t play=%t", parity.EditModeEqual, parity.PlayModeEqual))
	}
	workerTreeAfter, err := snapshotPhase2Entries(ctx, filepath.Join(metadata.WorkerPath, "Library"))
	if err != nil {
		return summary, err
	}
	workerEvidence.ChangedEntries = phase2ChangedEntries(workerTreeBefore, workerTreeAfter)
	if len(workerEvidence.ChangedEntries) == 0 {
		return summary, newError(CodePoolCorrupt, "verify-worker-library-write", metadata.WorkerPath, fmt.Errorf("Unity did not create or modify a Library entry"))
	}
	summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-unity", service, paths, baseline.LibraryPath, filepath.Join(metadata.WorkerPath, "Library")))

	baselineAfter, _, err := store.Verify(ctx, baseline)
	baselineTreeAfter, hashErr := HashTree(ctx, baseline.LibraryPath)
	baselineMetadataAfter, metadataErr := os.ReadFile(filepath.Join(baseline.Path, baselineMetadataFile))
	baselineCompleteAfter, completeErr := os.ReadFile(filepath.Join(baseline.Path, baselineCompleteFile))
	if err != nil || hashErr != nil || metadataErr != nil || completeErr != nil || baselineAfter.State != BaselineValid || baselineTreeAfter != baselineTreeBefore || string(baselineMetadataAfter) != string(baselineMetadataBefore) || string(baselineCompleteAfter) != string(baselineCompleteBefore) {
		return summary, newError(CodeBaselineCorrupt, "verify-baseline-after-unity", baseline.Path, errors.Join(err, hashErr, metadataErr, completeErr))
	}
	baselineUnchanged := true
	summary.BaselineUnchanged = &baselineUnchanged
	_ = writePhase2JSON(requested.ArtifactRoot, "baseline-after-worker.json", map[string]any{"baseline": baselineAfter, "tree": baselineTreeAfter, "unchanged": true})

	releaseResult, err := runWorkerReleaseDurability(workerReleaseDurabilityOps{
		release: func() (WorkerMetrics, error) {
			return lease.Release(ctx)
		},
		measure: func() (Residual, error) {
			residual, residualErr := measureMountedResidual(paths)
			if residualErr != nil {
				return residual, newError(CodeCleanupFailed, "measure-worker-release-mounted-residual", paths.PoolRoot, residualErr)
			}
			return residual, nil
		},
		writeArtifact: func(artifact workerReleaseArtifact) error {
			if writeErr := writePhase2JSON(requested.ArtifactRoot, "worker-release.json", artifact); writeErr != nil {
				return newError(CodeCleanupFailed, "write-worker-release-artifact", requested.ArtifactRoot, writeErr)
			}
			return nil
		},
		flush: func() error {
			if flushErr := mounted.Flush(ctx); flushErr != nil {
				return newError(CodeCleanupFailed, "flush-worker-release", volume.VolumeGUIDPath, flushErr)
			}
			return nil
		},
		beforeDetach: func() error {
			summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-worker-release", service, paths, baseline.LibraryPath, ""))
			after, snapshotErr := captureUnityPhase2Source(ctx, requested.FixturePath)
			if snapshotErr != nil {
				return snapshotErr
			}
			summary.SourceAfter = &after
			sourceUnchanged := before == after
			summary.SourceUnchanged = &sourceUnchanged
			if !sourceUnchanged {
				return newError(CodeOwnershipMismatch, "verify-fixture-source-isolation", requested.FixturePath, nil)
			}
			return nil
		},
		detach: func() error {
			if closeErr := closeMountedBounded(mounted); closeErr != nil {
				return cleanupFailure("detach-pool-after-worker-release", paths.VHDX, closeErr, true)
			}
			mounted = nil
			return nil
		},
		status: func() (*Result, error) {
			status, statusErr := service.Status(ctx, requested.Pool)
			_ = writePhase2JSON(requested.ArtifactRoot, "pool-status.json", phase2ResultOrError(status, statusErr))
			return status, statusErr
		},
		remove: func() (*Result, error) {
			removed, removeErr := service.Remove(ctx, requested.Pool)
			_ = writePhase2JSON(requested.ArtifactRoot, "pool-remove.json", phase2ResultOrError(removed, removeErr))
			return removed, removeErr
		},
	})
	workerReleased = releaseResult.Durability.ReleaseSucceeded
	summary.Release = &releaseResult.Metrics
	summary.ReleaseResidual = &releaseResult.MountedResidual
	summary.ReleaseMountedResidual = &releaseResult.MountedResidual
	summary.ReleaseDurability = &releaseResult.Durability
	if releaseResult.Durability.DurableResidual != nil {
		summary.ReleaseDurableResidual = releaseResult.Durability.DurableResidual
	}
	summary.PoolStatus = releaseResult.PoolStatus
	summary.PoolRemove = releaseResult.PoolRemove
	if releaseResult.Durability.PoolRemoveSucceeded {
		poolExists = false
	}
	if err != nil {
		preservePool = poolExists
		return summary, err
	}
	summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-pool-remove", service, paths, "", ""))
	summary.StorageDeltas = calculateUnityPhase2StorageDeltas(summary.Storage)
	summary.Status = "PASS"
	summary.Verdict = "UNITY_PHASE2_SINGLE_WORKER_COMPATIBLE"
	summary.CleanupState = "released"
	return summary, nil
}

func cleanupWasPreserved(err error) bool {
	if err == nil {
		return true
	}
	var evidence *Error
	return errors.As(err, &evidence) && evidence.CleanupState == "preserved"
}

func validateUnityPhase2Config(requested UnityPhase2Config) (UnityPhase2Config, Paths, error) {
	for name, path := range map[string]string{"Unity editor": requested.UnityEditorPath, "fixture": requested.FixturePath, "artifact root": requested.ArtifactRoot} {
		if path == "" || !filepath.IsAbs(path) {
			return requested, Paths{}, newError(CodeInvalidConfiguration, "validate-phase2-config", path, fmt.Errorf("%s path must be absolute", name))
		}
	}
	requested.UnityEditorPath = filepath.Clean(requested.UnityEditorPath)
	requested.FixturePath = filepath.Clean(requested.FixturePath)
	requested.ArtifactRoot = filepath.Clean(requested.ArtifactRoot)
	artifactRoot, err := resolveConfiguredRoot(requested.ArtifactRoot)
	if err != nil {
		return requested, Paths{}, newError(CodeInvalidConfiguration, "canonical-phase2-artifact-root", requested.ArtifactRoot, err)
	}
	requested.ArtifactRoot = artifactRoot
	if requested.TestTimeout <= 0 {
		return requested, Paths{}, newError(CodeInvalidConfiguration, "validate-phase2-timeout", requested.ArtifactRoot, fmt.Errorf("positive test timeout required"))
	}
	if err := unityvhdxfixture.ValidateFixtureSource(requested.FixturePath); err != nil {
		return requested, Paths{}, err
	}
	if info, err := os.Stat(requested.UnityEditorPath); err != nil || !info.Mode().IsRegular() {
		return requested, Paths{}, newError(CodeInvalidConfiguration, "validate-unity-editor", requested.UnityEditorPath, err)
	}
	pool, paths, err := NewPaths(requested.Pool)
	requested.Pool = pool
	if err != nil {
		return requested, Paths{}, err
	}
	if PathWithin(paths.Root, requested.ArtifactRoot) || PathWithin(paths.Mount, requested.ArtifactRoot) || strings.EqualFold(paths.Root, requested.ArtifactRoot) {
		return requested, Paths{}, newError(CodeInvalidConfiguration, "validate-artifact-isolation", requested.ArtifactRoot, fmt.Errorf("artifacts must be outside the pool"))
	}
	return requested, paths, nil
}

func mountUnityPhase2Pool(ctx context.Context, service *Service, paths Paths) (MountedPool, PoolMetadata, PoolMetadata, error) {
	if err := service.checkNative(ctx); err != nil {
		return nil, PoolMetadata{}, PoolMetadata{}, err
	}
	if err := rejectPendingOwner(paths); err != nil {
		return nil, PoolMetadata{}, PoolMetadata{}, err
	}
	if err := validateExistingPoolPaths(paths); err != nil {
		return nil, PoolMetadata{}, PoolMetadata{}, err
	}
	host, err := service.readMetadata(paths.Owner)
	if err != nil {
		return nil, PoolMetadata{}, PoolMetadata{}, err
	}
	if err := service.validateHostOwnership(paths, host); err != nil {
		return nil, PoolMetadata{}, PoolMetadata{}, err
	}
	mounted, err := service.native.Mount(ctx, paths.VHDX, paths.Mount, false)
	if err != nil {
		return nil, PoolMetadata{}, PoolMetadata{}, err
	}
	fail := func(cause error) (MountedPool, PoolMetadata, PoolMetadata, error) {
		return nil, PoolMetadata{}, PoolMetadata{}, errors.Join(cause, closeMountedBounded(mounted))
	}
	if err := validateVolume(mounted.Volume()); err != nil {
		return fail(err)
	}
	if _, err := mounted.WaitReady(ctx, paths, host); err != nil {
		return fail(err)
	}
	pool, err := service.readMetadata(paths.PoolFile)
	if err != nil {
		return fail(err)
	}
	if err := service.compareIdentity(paths, host, pool, mounted.Volume()); err != nil {
		return fail(err)
	}
	return mounted, host, pool, nil
}

func captureUnityPhase2Source(ctx context.Context, fixture string) (UnityPhase2SourceSnapshot, error) {
	repo, err := runGit(fixture, "rev-parse", "--show-toplevel")
	if err != nil {
		return UnityPhase2SourceSnapshot{}, err
	}
	repo = strings.TrimSpace(repo)
	revision, err := runGit(repo, "rev-parse", "HEAD")
	if err != nil {
		return UnityPhase2SourceSnapshot{}, err
	}
	branch, err := runGit(repo, "branch", "--show-current")
	if err != nil {
		return UnityPhase2SourceSnapshot{}, err
	}
	status, err := runGit(repo, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return UnityPhase2SourceSnapshot{}, err
	}
	// Scope the pathspec from the fixture directory itself. This avoids Git
	// rejecting an equivalent temp path when the OS canonicalizes an ancestor
	// differently (for example /var -> /private/var or a Windows short name).
	fixtureStatus, err := runGit(fixture, "status", "--porcelain=v1", "--untracked-files=all", "--", ".")
	if err != nil {
		return UnityPhase2SourceSnapshot{}, err
	}
	version, err := unityvhdxfixture.FixtureVersion(fixture)
	if err != nil {
		return UnityPhase2SourceSnapshot{}, err
	}
	assets, err := HashTree(ctx, filepath.Join(fixture, "Assets"))
	if err != nil {
		return UnityPhase2SourceSnapshot{}, err
	}
	packages, err := HashTree(ctx, filepath.Join(fixture, "Packages"))
	if err != nil {
		return UnityPhase2SourceSnapshot{}, err
	}
	settings, err := HashTree(ctx, filepath.Join(fixture, "ProjectSettings"))
	if err != nil {
		return UnityPhase2SourceSnapshot{}, err
	}
	lockDigest, err := hashFileContext(ctx, filepath.Join(fixture, "Packages", "packages-lock.json"))
	if err != nil {
		return UnityPhase2SourceSnapshot{}, err
	}
	return UnityPhase2SourceSnapshot{RepositoryRoot: repo, Revision: strings.TrimSpace(revision), Branch: strings.TrimSpace(branch), GitStatus: strings.TrimSpace(status), FixtureGitStatus: strings.TrimSpace(fixtureStatus), UnityVersion: version, Assets: assets, Packages: packages, ProjectSettings: settings, PackagesLockSHA256: lockDigest}, nil
}

func runGit(directory string, arguments ...string) (string, error) {
	command := exec.Command("git", append([]string{"-c", "safe.directory=C:/Dev/testplay-runner", "-C", directory}, arguments...)...)
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(output), nil
}

func measureUnityPhase2Storage(ctx context.Context, name string, service *Service, paths Paths, baselinePath, workerPath string) UnityPhase2StorageSnapshot {
	result := UnityPhase2StorageSnapshot{Name: name, MeasuredAt: time.Now().UTC()}
	if pathExists(paths.Mount) {
		if used, err := newNativeWorkerStorageMeter(paths).VolumeUsedBytes(ctx); err == nil {
			result.RefsUsedBytes = &used
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
	if baselinePath != "" {
		if usage, err := shadow.MeasureDirectoryUsage(baselinePath); err == nil {
			result.Baseline = &directoryUsage{LogicalBytes: usage.LogicalBytes, AllocatedBytes: usage.AllocatedBytes}
		}
	}
	if workerPath != "" {
		if usage, err := shadow.MeasureDirectoryUsage(workerPath); err == nil {
			result.Worker = &directoryUsage{LogicalBytes: usage.LogicalBytes, AllocatedBytes: usage.AllocatedBytes}
			if tree, hashErr := HashTree(ctx, workerPath); hashErr == nil {
				result.WorkerFileCount = &tree.FileCount
			}
		}
	}
	return result
}

func calculateUnityPhase2StorageDeltas(snapshots []UnityPhase2StorageSnapshot) *UnityPhase2StorageDeltas {
	byName := make(map[string]UnityPhase2StorageSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byName[snapshot.Name] = snapshot
	}
	result := &UnityPhase2StorageDeltas{}
	baseline := byName["after-baseline"]
	acquired := byName["after-worker-acquire"]
	afterUnity := byName["after-unity"]
	afterRelease := byName["after-worker-release"]
	before := byName["before-baseline"]
	if baseline.Baseline != nil && acquired.Worker != nil {
		value := acquired.Worker.LogicalBytes - baseline.Baseline.LogicalBytes
		result.WorkerLogicalAmplificationBytes = &value
	}
	if baseline.RefsUsedBytes != nil && acquired.RefsUsedBytes != nil {
		value := *acquired.RefsUsedBytes - *baseline.RefsUsedBytes
		result.WorkerPhysicalAllocationDelta = &value
	}
	if acquired.Worker != nil && afterUnity.Worker != nil {
		value := afterUnity.Worker.LogicalBytes - acquired.Worker.LogicalBytes
		result.UnityLogicalWriteDelta = &value
		value = afterUnity.Worker.AllocatedBytes - acquired.Worker.AllocatedBytes
		result.UnityPhysicalWriteDelta = &value
	}
	if afterUnity.RefsUsedBytes != nil && afterRelease.RefsUsedBytes != nil {
		value := *afterUnity.RefsUsedBytes - *afterRelease.RefsUsedBytes
		result.RefsReclaimedAfterRelease = &value
	}
	if before.VHDX != nil && afterRelease.VHDX != nil {
		value := afterRelease.VHDX.AllocatedBytes - before.VHDX.AllocatedBytes
		result.VHDXAllocatedGrowthThroughRun = &value
	}
	return result
}

func snapshotPhase2Entries(ctx context.Context, root string) (map[string]phase2Entry, error) {
	result := make(map[string]phase2Entry)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported entry: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		digest, err := hashFileContext(ctx, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(rel)] = phase2Entry{Size: info.Size(), ModifiedAt: info.ModTime().UnixNano(), Digest: digest}
		return nil
	})
	return result, err
}

func phase2ChangedEntries(before, after map[string]phase2Entry) []string {
	changed := make([]string, 0)
	for path, current := range after {
		if previous, ok := before[path]; !ok || previous != current {
			changed = append(changed, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

func verifyPhase2Junction(target, junction string) error {
	info, err := os.Lstat(junction)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return errors.Join(err, fmt.Errorf("Library is not a junction"))
	}
	return verifyNativeJunctionIdentity(target, junction)
}

func countMatching(directory, prefix, suffix string) int {
	count, err := countEntries(directory, prefix, suffix)
	if err != nil {
		return -1
	}
	return count
}

func phase2ResultOrError(result any, err error) any {
	if err == nil {
		return result
	}
	return map[string]any{"result": result, "error": err.Error(), "code": ErrorCode(err)}
}

func writePhase2JSON(root, name string, value any) error {
	if root == "" {
		return nil
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(root, name), value, 0600)
}
