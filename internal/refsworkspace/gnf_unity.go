package refsworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/unityvhdxfixture"
)

const GNFUnitySchemaVersion = 1

const gnfUnityCLIConnectorPackage = "com.youngwoocho02.unity-cli-connector"

type GNFUnityConfig struct {
	Pool             Config        `json:"pool"`
	UnityEditorPath  string        `json:"unityEditorPath"`
	ProjectPath      string        `json:"projectPath"`
	ArtifactRoot     string        `json:"artifactRoot"`
	TestTimeout      time.Duration `json:"testTimeout"`
	PolicyOverride   bool          `json:"policyOverride"`
	LocalPackagePath string        `json:"localPackagePath"`
}

type GNFTestSelection struct {
	FrozenAt          time.Time `json:"frozenAt"`
	Reason            string    `json:"reason"`
	HistoricalTest    string    `json:"historicalTest"`
	HistoricalFound   bool      `json:"historicalFound"`
	EditMode          []string  `json:"editMode"`
	PlayMode          []string  `json:"playMode"`
	EditModeInventory []string  `json:"editModeInventory"`
	PlayModeInventory []string  `json:"playModeInventory"`
}

type GNFUnityProcessEvidence struct {
	PID                 int                             `json:"pid"`
	StartedAt           time.Time                       `json:"startedAt"`
	CompletedAt         time.Time                       `json:"completedAt"`
	TimedOut            bool                            `json:"timedOut"`
	GitLongPathsEnabled bool                            `json:"gitLongPathsEnabled"`
	Result              unityvhdxfixture.PlatformResult `json:"result"`
}

type GNFLocalPackageEvidence struct {
	Path           string   `json:"path"`
	RepositoryRoot string   `json:"repositoryRoot"`
	Revision       string   `json:"revision"`
	Origin         string   `json:"origin"`
	GitStatus      string   `json:"gitStatus"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Tree           TreeInfo `json:"tree"`
}

type GNFBaselineEvidence struct {
	CompatibilityKey CompatibilityKey         `json:"compatibilityKey"`
	StateBefore      BaselineState            `json:"stateBefore"`
	Metrics          BaselineMetrics          `json:"metrics"`
	Baseline         *Baseline                `json:"baseline,omitempty"`
	ReferenceEdit    *GNFUnityProcessEvidence `json:"referenceEdit,omitempty"`
	ReferencePlay    *GNFUnityProcessEvidence `json:"referencePlay,omitempty"`
	JunctionRemoved  bool                     `json:"junctionRemoved"`
	Finalized        bool                     `json:"finalized"`
	Tree             *TreeInfo                `json:"tree,omitempty"`
	MetadataSHA256   string                   `json:"metadataSha256,omitempty"`
	CompleteSHA256   string                   `json:"completeSha256,omitempty"`
}

type GNFBudgetEvidence struct {
	SoftBudgetBytes                int64 `json:"softBudgetBytes"`
	WorkerReserveBytes             int64 `json:"workerReserveBytes"`
	RefsUsedBytes                  int64 `json:"refsUsedBytes"`
	AvailableBytes                 int64 `json:"availableBytes"`
	BaselineLogicalBytes           int64 `json:"baselineLogicalBytes"`
	BaselineAllocatedBytes         int64 `json:"baselineAllocatedBytes"`
	HostFreeBytes                  int64 `json:"hostFreeBytes"`
	VHDXAllocatedBytes             int64 `json:"vhdxAllocatedBytes"`
	MinimumRequiredSoftBudgetBytes int64 `json:"minimumRequiredSoftBudgetBytes"`
	ReservationPossible            bool  `json:"reservationPossible"`
	OverrideUsed                   bool  `json:"overrideUsed"`
}

type GNFWorkerEvidence struct {
	LeaseID           string                   `json:"leaseId"`
	Metadata          WorkerMetadata           `json:"metadata"`
	Metrics           WorkerMetrics            `json:"metrics"`
	AcquireDurationMs int64                    `json:"acquireDurationMs"`
	JunctionVerified  bool                     `json:"junctionVerified"`
	BeforeTree        TreeInfo                 `json:"beforeTree"`
	BeforeUsage       directoryUsage           `json:"beforeUsage"`
	EditMode          *GNFUnityProcessEvidence `json:"editMode,omitempty"`
	PlayMode          *GNFUnityProcessEvidence `json:"playMode,omitempty"`
	AfterTree         TreeInfo                 `json:"afterTree"`
	AfterUsage        directoryUsage           `json:"afterUsage"`
	ChangedEntries    []string                 `json:"changedEntries"`
	Release           *WorkerMetrics           `json:"release,omitempty"`
}

type GNFSemanticParity struct {
	EditModeEqual bool `json:"editModeEqual"`
	PlayModeEqual bool `json:"playModeEqual"`
	ExactTestSets bool `json:"exactTestSets"`
}

type GNFUnitySummary struct {
	SchemaVersion          int                              `json:"schemaVersion"`
	Status                 string                           `json:"status"`
	Verdict                string                           `json:"verdict"`
	Code                   string                           `json:"code,omitempty"`
	StartedAt              time.Time                        `json:"startedAt"`
	CompletedAt            time.Time                        `json:"completedAt"`
	DurationMs             int64                            `json:"durationMs"`
	Config                 GNFUnityConfig                   `json:"config"`
	SourceBefore           *UnityPhase2SourceSnapshot       `json:"sourceBefore,omitempty"`
	SourceAfter            *UnityPhase2SourceSnapshot       `json:"sourceAfter,omitempty"`
	SourceUnchanged        *bool                            `json:"sourceUnchanged,omitempty"`
	LocalPackageBefore     *GNFLocalPackageEvidence         `json:"localPackageBefore,omitempty"`
	LocalPackageAfter      *GNFLocalPackageEvidence         `json:"localPackageAfter,omitempty"`
	LocalPackageUnchanged  *bool                            `json:"localPackageUnchanged,omitempty"`
	Selection              *GNFTestSelection                `json:"testSelection,omitempty"`
	Setup                  *Result                          `json:"setup,omitempty"`
	Baseline               *GNFBaselineEvidence             `json:"baseline,omitempty"`
	Budget                 *GNFBudgetEvidence               `json:"budget,omitempty"`
	Worker                 *GNFWorkerEvidence               `json:"worker,omitempty"`
	Parity                 *GNFSemanticParity               `json:"semanticParity,omitempty"`
	BaselineUnchanged      *bool                            `json:"baselineUnchanged,omitempty"`
	ReleaseMountedResidual *Residual                        `json:"releaseMountedResidual,omitempty"`
	ReleaseDurableResidual *Residual                        `json:"releaseDurableResidual,omitempty"`
	ReleaseDurability      *WorkerReleaseDurabilityEvidence `json:"releaseDurability,omitempty"`
	PoolStatus             *Result                          `json:"poolStatus,omitempty"`
	PoolRemove             *Result                          `json:"poolRemove,omitempty"`
	Storage                []UnityPhase2StorageSnapshot     `json:"storage"`
	StorageDeltas          *UnityPhase2StorageDeltas        `json:"storageDeltas,omitempty"`
	CleanupState           string                           `json:"cleanupState"`
	Error                  string                           `json:"error,omitempty"`
}

func RunGNFUnity(ctx context.Context, requested GNFUnityConfig) (summary *GNFUnitySummary, returnErr error) {
	started := time.Now()
	artifactRoot := requested.ArtifactRoot
	summary = &GNFUnitySummary{SchemaVersion: GNFUnitySchemaVersion, Status: "FAILED", Verdict: "FAILED", StartedAt: started.UTC(), Config: requested, CleanupState: "not-started"}
	defer func() {
		summary.CompletedAt = time.Now().UTC()
		summary.DurationMs = time.Since(started).Milliseconds()
		if returnErr != nil {
			summary.Error = returnErr.Error()
			if summary.Code == "" {
				summary.Code = ErrorCode(returnErr)
			}
		}
		_ = writePhase2JSON(artifactRoot, "summary.json", summary)
	}()

	config, paths, source, selection, err := validateGNFConfig(ctx, requested)
	requested, summary.Config = config, config
	artifactRoot = config.ArtifactRoot
	if artifactRoot != "" {
		if mkdirErr := os.MkdirAll(artifactRoot, 0700); mkdirErr != nil {
			return summary, mkdirErr
		}
	}
	if err != nil {
		if isGNFBlockedCode(ErrorCode(err)) {
			summary.Status, summary.Verdict, summary.Code, summary.CleanupState = "BLOCKED", "BLOCKED", ErrorCode(err), "released"
		}
		return summary, err
	}
	summary.SourceBefore, summary.Selection = &source, &selection
	localPackage, err := validateGNFLocalPackage(ctx, config.ProjectPath, config.LocalPackagePath)
	if err != nil {
		if ErrorCode(err) == CodeGNFLocalPackageNotFound {
			summary.Status, summary.Verdict, summary.Code, summary.CleanupState = "BLOCKED", "BLOCKED", ErrorCode(err), "released"
		}
		return summary, err
	}
	summary.LocalPackageBefore = localPackage
	portableLocalPackagePath := ""
	if localPackage != nil {
		portableLocalPackagePath = config.LocalPackagePath
	}
	_ = writePhase2JSON(artifactRoot, "environment.json", map[string]any{"config": config, "source": source, "localPackage": localPackage})
	_ = writePhase2JSON(artifactRoot, "test-selection.json", selection)
	workspacesRoot, err := prepareGNFWorkspacesRoot(artifactRoot)
	if err != nil {
		return summary, err
	}
	defer func() {
		if summary.SourceAfter != nil {
			return
		}
		after, snapshotErr := captureUnityPhase2Source(context.Background(), config.ProjectPath)
		if snapshotErr != nil {
			returnErr = errors.Join(returnErr, snapshotErr)
			return
		}
		summary.SourceAfter = &after
		unchanged := source == after
		summary.SourceUnchanged = &unchanged
	}()
	if localPackage != nil {
		defer func() {
			after, packageErr := captureGNFLocalPackage(context.Background(), config.LocalPackagePath)
			if packageErr != nil {
				returnErr = errors.Join(returnErr, packageErr)
				return
			}
			summary.LocalPackageAfter = after
			unchanged := *localPackage == *after
			summary.LocalPackageUnchanged = &unchanged
			if !unchanged {
				returnErr = errors.Join(returnErr, newError(CodeOwnershipMismatch, "verify-gnf-local-package-isolation", config.LocalPackagePath, nil))
			}
		}()
	}

	baseExecutor := unityvhdxfixture.UnityExecutor{EditorPath: config.UnityEditorPath, Version: unityvhdxfixture.TargetUnityVersion}
	versionCtx, cancelVersion := context.WithTimeout(ctx, config.TestTimeout)
	err = baseExecutor.ValidateVersion(versionCtx, source.UnityVersion)
	cancelVersion()
	if err != nil {
		summary.Status, summary.Verdict, summary.Code, summary.CleanupState = "BLOCKED", "BLOCKED", CodeUnityVersionMismatch, "released"
		return summary, newError(CodeUnityVersionMismatch, "validate-gnf-unity-version", config.UnityEditorPath, err)
	}

	service := NewNativeService()
	setup, err := service.Setup(ctx, config.Pool)
	summary.Setup = setup
	_ = writePhase2JSON(artifactRoot, "pool-setup.json", phase2ResultOrError(setup, err))
	if err != nil {
		if ErrorCode(err) == CodeHostFreeSpaceFloor || ErrorCode(err) == CodeStorageBudgetExceeded {
			summary.Status, summary.Verdict, summary.Code = "BLOCKED", "BLOCKED", ErrorCode(err)
			var setupErr *Error
			if errors.As(err, &setupErr) && setupErr.CleanupState != "" {
				summary.CleanupState = setupErr.CleanupState
			} else {
				summary.CleanupState = "released"
			}
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
		if lease != nil && !workerReleased {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			metrics, releaseErr := lease.Release(cleanupCtx)
			cancel()
			if releaseErr == nil {
				workerReleased = true
				if summary.Worker != nil {
					summary.Worker.Release = &metrics
				}
			}
			cleanupErr = errors.Join(cleanupErr, releaseErr)
		}
		if mounted != nil {
			flushCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			cleanupErr = errors.Join(cleanupErr, mounted.Flush(flushCtx))
			cancel()
			cleanupErr = errors.Join(cleanupErr, closeMountedBounded(mounted))
			mounted = nil
		}
		if poolExists && !preservePool && (lease == nil || workerReleased) {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*cleanupTimeout)
			removed, removeErr := service.Remove(cleanupCtx, config.Pool)
			cancel()
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

	mounted, host, pool, err := mountUnityPhase2Pool(ctx, service, paths)
	if err != nil {
		return summary, err
	}
	volume := mounted.Volume()
	summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "before-baseline", service, paths, "", ""))

	localPackageDigest := ""
	if localPackage != nil {
		localPackageDigest = localPackage.Tree.Digest
	}
	key, _, err := ComputeCompatibilityKey(ctx, CompatibilityOptions{ProjectPath: config.ProjectPath, UnityExecutable: config.UnityEditorPath, BuildTarget: "StandaloneWindows64", ScriptingBackend: "Mono", LocalPackagesSHA256: localPackageDigest})
	if err != nil {
		return summary, err
	}
	store := NewLibraryBaselineStore(paths)
	baselineEvidence := &GNFBaselineEvidence{CompatibilityKey: key}
	summary.Baseline = baselineEvidence
	referenceWorkspace := filepath.Join(workspacesRoot, "reference")
	baseline, state, baselineMetrics, err := store.Ensure(ctx, key, func(buildCtx context.Context, libraryPath string) (buildErr error) {
		if copyErr := copyGNFProjectInputs(buildCtx, config.ProjectPath, referenceWorkspace, portableLocalPackagePath); copyErr != nil {
			return copyErr
		}
		junctionPath := filepath.Join(referenceWorkspace, "Library")
		junctions := NewNativeJunctioner()
		if createErr := junctions.Create(libraryPath, junctionPath); createErr != nil {
			return createErr
		}
		defer func() {
			removeErr := junctions.Remove(libraryPath, junctionPath)
			baselineEvidence.JunctionRemoved = removeErr == nil
			buildErr = errors.Join(buildErr, removeErr)
		}()
		edit, runErr := runGNFSelectedTest(buildCtx, config, referenceWorkspace, unityvhdxfixture.PlatformEditMode, selection.EditMode, filepath.Join(artifactRoot, "baseline-reference-editmode.xml"), filepath.Join(artifactRoot, "baseline-reference-editmode.log"))
		baselineEvidence.ReferenceEdit = &edit
		if runErr != nil {
			return runErr
		}
		play, runErr := runGNFSelectedTest(buildCtx, config, referenceWorkspace, unityvhdxfixture.PlatformPlayMode, selection.PlayMode, filepath.Join(artifactRoot, "baseline-reference-playmode.xml"), filepath.Join(artifactRoot, "baseline-reference-playmode.log"))
		baselineEvidence.ReferencePlay = &play
		return runErr
	})
	baselineEvidence.StateBefore, baselineEvidence.Metrics, baselineEvidence.Baseline = state, baselineMetrics, baseline
	baselineEvidence.Finalized = err == nil && baseline != nil
	_ = writePhase2JSON(artifactRoot, "baseline-build.json", phase2ResultOrError(baselineEvidence, err))
	if err != nil {
		return summary, err
	}
	verifiedBefore, _, err := store.Verify(ctx, baseline)
	if err != nil || verifiedBefore.State != BaselineValid || verifiedBefore.Baseline == nil {
		return summary, newError(CodeBaselineCorrupt, "verify-gnf-baseline-before-worker", baseline.Path, err)
	}
	baselineTreeBefore, err := HashTree(ctx, baseline.LibraryPath)
	if err != nil {
		return summary, err
	}
	baselineEvidence.Tree = &baselineTreeBefore
	metadataBefore, err := os.ReadFile(filepath.Join(baseline.Path, baselineMetadataFile))
	if err != nil {
		return summary, err
	}
	completeBefore, err := os.ReadFile(filepath.Join(baseline.Path, baselineCompleteFile))
	if err != nil {
		return summary, err
	}
	baselineEvidence.MetadataSHA256 = hashBytes(metadataBefore)
	baselineEvidence.CompleteSHA256 = hashBytes(completeBefore)
	summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-baseline", service, paths, baseline.LibraryPath, ""))
	budget, err := measureGNFBudget(ctx, service, paths, config, baseline)
	summary.Budget = &budget
	_ = writePhase2JSON(artifactRoot, "baseline-before-worker.json", map[string]any{"baseline": verifiedBefore, "tree": baselineTreeBefore, "budget": budget, "activeUseCount": countMatching(paths.Leases, "active-", ".json"), "metadataSha256": baselineEvidence.MetadataSHA256, "completeSha256": baselineEvidence.CompleteSHA256})
	if err != nil {
		summary.Status, summary.Verdict, summary.Code = "BLOCKED", "BLOCKED", CodeStorageBudgetExceeded
		return summary, err
	}

	workerWorkspace := filepath.Join(workspacesRoot, "worker")
	if err := copyGNFProjectInputs(ctx, config.ProjectPath, workerWorkspace, portableLocalPackagePath); err != nil {
		return summary, err
	}
	manager, err := NewVerifiedWorkerManager(paths, store, NewNativeTreeCloner(), NewNativeJunctioner(), host, pool, volume)
	if err != nil {
		return summary, err
	}
	leaseID := "gnf-single-" + key.Digest[:16]
	acquireStarted := time.Now()
	lease, workerMetrics, err := manager.Acquire(ctx, WorkerRequest{Key: key, LeaseID: leaseID, JunctionPath: filepath.Join(workerWorkspace, "Library")})
	workerEvidence := &GNFWorkerEvidence{LeaseID: leaseID, Metrics: workerMetrics, AcquireDurationMs: time.Since(acquireStarted).Milliseconds()}
	summary.Worker = workerEvidence
	if err != nil {
		_ = writePhase2JSON(artifactRoot, "worker-acquire.json", phase2ResultOrError(workerEvidence, err))
		return summary, err
	}
	workerEvidence.Metadata = lease.Metadata()
	workerEvidence.JunctionVerified = verifyPhase2Junction(filepath.Join(workerEvidence.Metadata.WorkerPath, "Library"), workerEvidence.Metadata.JunctionPath) == nil
	if err := ValidateCloneMetrics(workerEvidence.Metadata.Clone); err != nil || !workerEvidence.JunctionVerified || workerEvidence.Metadata.State != LeaseReady {
		return summary, errors.Join(err, newError(CodeCloneFailed, "validate-gnf-worker-acquire", workerEvidence.Metadata.WorkerPath, nil))
	}
	workerLibrary := filepath.Join(workerEvidence.Metadata.WorkerPath, "Library")
	workerEntriesBefore, err := snapshotPhase2Entries(ctx, workerLibrary)
	if err != nil {
		return summary, err
	}
	workerEvidence.BeforeTree, err = HashTree(ctx, workerLibrary)
	if err != nil {
		return summary, err
	}
	if usage, usageErr := shadow.MeasureDirectoryUsage(workerLibrary); usageErr == nil {
		workerEvidence.BeforeUsage = directoryUsage{LogicalBytes: usage.LogicalBytes, AllocatedBytes: usage.AllocatedBytes}
	} else {
		return summary, usageErr
	}
	_ = writePhase2JSON(artifactRoot, "worker-acquire.json", workerEvidence)
	summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-worker-acquire", service, paths, baseline.LibraryPath, workerLibrary))
	if err := lease.MarkRunning(); err != nil {
		return summary, err
	}
	workerEdit, runErr := runGNFSelectedTest(ctx, config, workerWorkspace, unityvhdxfixture.PlatformEditMode, selection.EditMode, filepath.Join(artifactRoot, "worker-editmode.xml"), filepath.Join(artifactRoot, "worker-editmode.log"))
	workerEvidence.EditMode = &workerEdit
	if runErr != nil {
		return summary, newError(CodePoolCorrupt, "run-gnf-worker-editmode", workerWorkspace, runErr)
	}
	workerPlay, runErr := runGNFSelectedTest(ctx, config, workerWorkspace, unityvhdxfixture.PlatformPlayMode, selection.PlayMode, filepath.Join(artifactRoot, "worker-playmode.xml"), filepath.Join(artifactRoot, "worker-playmode.log"))
	workerEvidence.PlayMode = &workerPlay
	if runErr != nil {
		return summary, newError(CodePoolCorrupt, "run-gnf-worker-playmode", workerWorkspace, runErr)
	}
	parity := &GNFSemanticParity{ExactTestSets: true}
	parity.EditModeEqual = unityvhdxfixture.CompareSemantic(baselineEvidence.ReferenceEdit.Result, workerEvidence.EditMode.Result) == nil
	parity.PlayModeEqual = unityvhdxfixture.CompareSemantic(baselineEvidence.ReferencePlay.Result, workerEvidence.PlayMode.Result) == nil
	summary.Parity = parity
	_ = writePhase2JSON(artifactRoot, "semantic-parity.json", parity)
	if !parity.EditModeEqual || !parity.PlayModeEqual {
		return summary, newError(CodePoolCorrupt, "compare-gnf-semantic-parity", workerWorkspace, nil)
	}
	workerEntriesAfter, err := snapshotPhase2Entries(ctx, workerLibrary)
	if err != nil {
		return summary, err
	}
	workerEvidence.ChangedEntries = phase2ChangedEntries(workerEntriesBefore, workerEntriesAfter)
	workerEvidence.AfterTree, err = HashTree(ctx, workerLibrary)
	if err != nil {
		return summary, err
	}
	if usage, usageErr := shadow.MeasureDirectoryUsage(workerLibrary); usageErr == nil {
		workerEvidence.AfterUsage = directoryUsage{LogicalBytes: usage.LogicalBytes, AllocatedBytes: usage.AllocatedBytes}
	} else {
		return summary, usageErr
	}
	_ = writePhase2JSON(artifactRoot, "worker-after-unity.json", workerEvidence)
	if len(workerEvidence.ChangedEntries) == 0 || (workerEvidence.BeforeUsage == workerEvidence.AfterUsage && workerEvidence.BeforeTree == workerEvidence.AfterTree) {
		return summary, newError(CodePoolCorrupt, "verify-gnf-worker-library-change", workerLibrary, nil)
	}
	summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-unity", service, paths, baseline.LibraryPath, workerLibrary))

	baselineAfter, _, verifyErr := store.Verify(ctx, baseline)
	baselineTreeAfter, hashErr := HashTree(ctx, baseline.LibraryPath)
	metadataAfter, metadataErr := os.ReadFile(filepath.Join(baseline.Path, baselineMetadataFile))
	completeAfter, completeErr := os.ReadFile(filepath.Join(baseline.Path, baselineCompleteFile))
	baselineUnchanged := verifyErr == nil && hashErr == nil && metadataErr == nil && completeErr == nil && baselineAfter.State == BaselineValid && baselineTreeAfter == baselineTreeBefore && string(metadataAfter) == string(metadataBefore) && string(completeAfter) == string(completeBefore)
	summary.BaselineUnchanged = &baselineUnchanged
	_ = writePhase2JSON(artifactRoot, "baseline-isolation.json", map[string]any{"unchanged": baselineUnchanged, "before": baselineTreeBefore, "after": baselineTreeAfter, "protection": baseline.Metadata.Protection})
	afterSource, sourceErr := captureUnityPhase2Source(ctx, config.ProjectPath)
	summary.SourceAfter = &afterSource
	sourceUnchanged := sourceErr == nil && source == afterSource
	summary.SourceUnchanged = &sourceUnchanged
	_ = writePhase2JSON(artifactRoot, "source-isolation.json", map[string]any{"unchanged": sourceUnchanged, "before": source, "after": afterSource})
	if !baselineUnchanged || !sourceUnchanged {
		return summary, newError(CodeOwnershipMismatch, "verify-gnf-isolation", config.ProjectPath, errors.Join(verifyErr, hashErr, metadataErr, completeErr, sourceErr))
	}

	releaseResult, err := runWorkerReleaseDurability(workerReleaseDurabilityOps{
		release: func() (WorkerMetrics, error) { return lease.Release(ctx) },
		measure: func() (Residual, error) { return measureMountedResidual(paths) },
		writeArtifact: func(artifact workerReleaseArtifact) error {
			return errors.Join(writePhase2JSON(artifactRoot, "worker-release.json", artifact), writePhase2JSON(artifactRoot, "mounted-release-residual.json", artifact.MountedResidual))
		},
		flush: func() error { return mounted.Flush(ctx) },
		beforeDetach: func() error {
			summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-worker-release", service, paths, baseline.LibraryPath, ""))
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
			status, statusErr := service.Status(ctx, config.Pool)
			_ = writePhase2JSON(artifactRoot, "durable-status.json", phase2ResultOrError(status, statusErr))
			if statusErr == nil {
				summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-durable-reattach", service, paths, baseline.LibraryPath, ""))
			}
			return status, statusErr
		},
		remove: func() (*Result, error) {
			removed, removeErr := service.Remove(ctx, config.Pool)
			_ = writePhase2JSON(artifactRoot, "pool-remove.json", phase2ResultOrError(removed, removeErr))
			return removed, removeErr
		},
	})
	workerEvidence.Release = &releaseResult.Metrics
	summary.ReleaseMountedResidual = &releaseResult.MountedResidual
	summary.ReleaseDurability = &releaseResult.Durability
	if releaseResult.Durability.DurableResidual != nil {
		summary.ReleaseDurableResidual = releaseResult.Durability.DurableResidual
	}
	summary.PoolStatus, summary.PoolRemove = releaseResult.PoolStatus, releaseResult.PoolRemove
	workerReleased = releaseResult.Durability.ReleaseSucceeded
	if releaseResult.Durability.PoolRemoveSucceeded {
		poolExists = false
	}
	if err != nil {
		preservePool = poolExists
		return summary, err
	}
	summary.Storage = append(summary.Storage, measureUnityPhase2Storage(ctx, "after-pool-remove", service, paths, "", ""))
	summary.StorageDeltas = calculateUnityPhase2StorageDeltas(summary.Storage)
	_ = writePhase2JSON(artifactRoot, "storage-timeline.json", map[string]any{"snapshots": summary.Storage, "deltas": summary.StorageDeltas})
	summary.Status, summary.Verdict, summary.Code, summary.CleanupState = "PASS", "GNF_SINGLE_WORKER_COMPATIBLE", "", "released"
	return summary, nil
}

func validateGNFConfig(ctx context.Context, requested GNFUnityConfig) (GNFUnityConfig, Paths, UnityPhase2SourceSnapshot, GNFTestSelection, error) {
	emptySource, emptySelection := UnityPhase2SourceSnapshot{}, GNFTestSelection{}
	if requested.ProjectPath == "" || !filepath.IsAbs(requested.ProjectPath) {
		return requested, Paths{}, emptySource, emptySelection, newError(CodeGNFProjectNotFound, "validate-gnf-project", requested.ProjectPath, fmt.Errorf("absolute path required"))
	}
	if info, err := os.Stat(requested.ProjectPath); err != nil || !info.IsDir() {
		return requested, Paths{}, emptySource, emptySelection, newError(CodeGNFProjectNotFound, "validate-gnf-project", requested.ProjectPath, err)
	}
	phase2, paths, err := validateUnityPhase2Config(UnityPhase2Config{Pool: requested.Pool, UnityEditorPath: requested.UnityEditorPath, FixturePath: requested.ProjectPath, ArtifactRoot: requested.ArtifactRoot, TestTimeout: requested.TestTimeout})
	requested.Pool, requested.UnityEditorPath, requested.ProjectPath, requested.ArtifactRoot, requested.TestTimeout = phase2.Pool, phase2.UnityEditorPath, phase2.FixturePath, phase2.ArtifactRoot, phase2.TestTimeout
	if requested.LocalPackagePath != "" {
		if !filepath.IsAbs(requested.LocalPackagePath) {
			return requested, paths, emptySource, emptySelection, newError(CodeGNFLocalPackageNotFound, "validate-gnf-local-package", requested.LocalPackagePath, fmt.Errorf("absolute path required"))
		}
		requested.LocalPackagePath = filepath.Clean(requested.LocalPackagePath)
	}
	if err != nil {
		return requested, paths, emptySource, emptySelection, err
	}
	source, err := captureUnityPhase2Source(ctx, requested.ProjectPath)
	if err != nil {
		return requested, paths, emptySource, emptySelection, err
	}
	if source.FixtureGitStatus != "" {
		return requested, paths, source, emptySelection, newError(CodeGNFSourceDirty, "validate-gnf-source-clean", requested.ProjectPath, fmt.Errorf("%s", source.FixtureGitStatus))
	}
	if source.UnityVersion != unityvhdxfixture.TargetUnityVersion {
		return requested, paths, source, emptySelection, newError(CodeUnityVersionMismatch, "validate-gnf-project-version", requested.ProjectPath, fmt.Errorf("project=%s editor=%s", source.UnityVersion, unityvhdxfixture.TargetUnityVersion))
	}
	selection, err := freezeGNFTestSelection(requested.ProjectPath)
	return requested, paths, source, selection, err
}

func freezeGNFTestSelection(root string) (GNFTestSelection, error) {
	selection := GNFTestSelection{FrozenAt: time.Now().UTC(), HistoricalTest: "CodexMovementSmokeTest.TestPlayer_MovesRight_InPlayMode"}
	editInventory, err := collectGNFSourceInventory(filepath.Join(root, "Assets", "Tests", "EditMode"))
	if err != nil {
		return selection, err
	}
	playInventory, err := collectGNFSourceInventory(filepath.Join(root, "Assets", "Tests", "PlayMode"))
	if err != nil {
		return selection, err
	}
	selection.EditModeInventory, selection.PlayModeInventory = editInventory, playInventory
	historicalFound := inventoryContainsSuffix(playInventory, selection.HistoricalTest)
	selection.HistoricalFound = historicalFound
	selection.EditMode = []string{"GNF.DungeonGen.Tests.WallPropValidatorTests.NullPrefab_Error"}
	if historicalFound {
		selection.Reason = "historical deterministic smoke test exists; paired with one pure EditMode validator"
		selection.PlayMode = []string{selection.HistoricalTest}
	} else {
		selection.Reason = "historical test absent on this revision; selected pure offline deterministic EditMode and PlayMode tests from source inventory"
		selection.PlayMode = []string{"DOOR_CONSENSUS_Tests.Proximity_CountsNearestExitWithinRadius"}
	}
	for _, candidate := range selection.EditMode {
		if !inventoryContainsExact(editInventory, candidate) {
			return selection, newError(CodeDeterministicTestUnavailable, "freeze-gnf-test-selection", candidate, fmt.Errorf("selected EditMode test not found in source inventory"))
		}
	}
	for _, candidate := range selection.PlayMode {
		if !inventoryContainsExact(playInventory, candidate) && !inventoryContainsSuffix(playInventory, candidate) {
			return selection, newError(CodeDeterministicTestUnavailable, "freeze-gnf-test-selection", candidate, fmt.Errorf("selected PlayMode test not found in source inventory"))
		}
	}
	return selection, nil
}

var (
	gnfNamespacePattern = regexp.MustCompile(`(?m)^\s*namespace\s+([A-Za-z_][A-Za-z0-9_.]*)`)
	gnfClassPattern     = regexp.MustCompile(`(?m)^\s*public\s+(?:sealed\s+)?class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	gnfTestPattern      = regexp.MustCompile(`(?ms)\[(?:Test|UnityTest)(?:Case)?(?:\([^\]]*\))?\]\s*(?:\[[^\]]+\]\s*)*public\s+(?:void|IEnumerator)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

func collectGNFSourceInventory(root string) ([]string, error) {
	inventory := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".cs") {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(data)
		classMatch := gnfClassPattern.FindStringSubmatch(text)
		if len(classMatch) != 2 {
			return nil
		}
		prefix := classMatch[1]
		if namespaceMatch := gnfNamespacePattern.FindStringSubmatch(text); len(namespaceMatch) == 2 {
			prefix = namespaceMatch[1] + "." + prefix
		}
		for _, method := range gnfTestPattern.FindAllStringSubmatch(text, -1) {
			inventory = append(inventory, prefix+"."+method[1])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(inventory)
	return inventory, nil
}

func inventoryContainsExact(inventory []string, candidate string) bool {
	index := sort.SearchStrings(inventory, candidate)
	return index < len(inventory) && inventory[index] == candidate
}

func inventoryContainsSuffix(inventory []string, candidate string) bool {
	for _, name := range inventory {
		if name == candidate || strings.HasSuffix(name, "."+candidate) {
			return true
		}
	}
	return false
}

func copyGNFProjectInputs(ctx context.Context, source, destination string, localPackagePath ...string) error {
	parent := filepath.Dir(destination)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() {
		return newError(CodeInvalidConfiguration, "validate-gnf-workspace-parent", parent, errors.Join(err, fmt.Errorf("workspace parent must be an existing directory")))
	}
	parentReparse, err := inspectPathReparse(parent)
	if err != nil || parentReparse {
		return newError(CodeOwnershipMismatch, "validate-gnf-workspace-parent", parent, errors.Join(err, fmt.Errorf("workspace parent must be a real directory")))
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(destination)
		}
	}()
	for _, name := range []string{"Assets", "Packages", "ProjectSettings"} {
		if err := shadow.CopyDirParallel(ctx, filepath.Join(source, name), filepath.Join(destination, name), 0); err != nil {
			return err
		}
	}
	if len(localPackagePath) > 1 {
		return newError(CodeInvalidConfiguration, "copy-gnf-local-package", destination, fmt.Errorf("at most one local package path is accepted"))
	}
	if len(localPackagePath) == 1 && localPackagePath[0] != "" {
		if err := embedGNFLocalPackage(ctx, destination, localPackagePath[0]); err != nil {
			return err
		}
	}
	succeeded = true
	return nil
}

func validateGNFLocalPackage(ctx context.Context, projectPath, configuredPath string) (*GNFLocalPackageEvidence, error) {
	manifest, err := readJSONMap(filepath.Join(projectPath, "Packages", "manifest.json"))
	if err != nil {
		return nil, newError(CodeInvalidConfiguration, "read-gnf-manifest", projectPath, err)
	}
	dependencies, _ := manifest["dependencies"].(map[string]any)
	value, exists := dependencies[gnfUnityCLIConnectorPackage].(string)
	if !exists {
		return nil, nil
	}
	if !strings.HasPrefix(strings.TrimSpace(value), "file:") {
		return nil, nil
	}
	if configuredPath == "" {
		return nil, newError(CodeGNFLocalPackageNotFound, "resolve-gnf-local-package", gnfUnityCLIConnectorPackage, fmt.Errorf("project dependency %q requires an explicit portable package path", value))
	}
	evidence, err := captureGNFLocalPackage(ctx, configuredPath)
	if err != nil {
		return nil, newError(CodeGNFLocalPackageNotFound, "validate-gnf-local-package", configuredPath, err)
	}
	if evidence.Name != gnfUnityCLIConnectorPackage {
		return nil, newError(CodeGNFLocalPackageNotFound, "validate-gnf-local-package-name", configuredPath, fmt.Errorf("package=%q expected=%q", evidence.Name, gnfUnityCLIConnectorPackage))
	}
	if evidence.GitStatus != "" {
		return nil, newError(CodeGNFLocalPackageNotFound, "validate-gnf-local-package-clean", configuredPath, fmt.Errorf("local package repository is dirty: %s", evidence.GitStatus))
	}
	return evidence, nil
}

func captureGNFLocalPackage(ctx context.Context, packagePath string) (*GNFLocalPackageEvidence, error) {
	canonical, err := canonicalExistingPath(packagePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(canonical)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(err, fmt.Errorf("local package must be a real directory"))
	}
	reparse, err := inspectPathReparse(canonical)
	if err != nil || reparse {
		return nil, errors.Join(err, fmt.Errorf("local package must not be a reparse point"))
	}
	var packageJSON struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	data, err := os.ReadFile(filepath.Join(canonical, "package.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &packageJSON); err != nil {
		return nil, err
	}
	if packageJSON.Name == "" || packageJSON.Version == "" {
		return nil, fmt.Errorf("valid package.json name and version are required")
	}
	repository, err := runGit(canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	repository = strings.TrimSpace(repository)
	revision, err := runGit(repository, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	status, err := runGit(repository, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	origin, err := runGit(repository, "remote", "get-url", "origin")
	if err != nil {
		return nil, err
	}
	tree, err := HashTree(ctx, canonical)
	if err != nil {
		return nil, err
	}
	return &GNFLocalPackageEvidence{Path: canonical, RepositoryRoot: repository, Revision: strings.TrimSpace(revision), Origin: strings.TrimSpace(origin), GitStatus: strings.TrimSpace(status), Name: packageJSON.Name, Version: packageJSON.Version, Tree: tree}, nil
}

func embedGNFLocalPackage(ctx context.Context, workspace, packagePath string) error {
	destination := filepath.Join(workspace, "Packages", gnfUnityCLIConnectorPackage)
	if _, err := os.Lstat(destination); err == nil {
		return newError(CodeOwnershipMismatch, "embed-gnf-local-package", destination, fmt.Errorf("embedded package destination already exists"))
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := shadow.CopyDirParallel(ctx, packagePath, destination, 0); err != nil {
		return err
	}
	manifestPath := filepath.Join(workspace, "Packages", "manifest.json")
	manifest, err := readJSONMap(manifestPath)
	if err != nil {
		return err
	}
	dependencies, ok := manifest["dependencies"].(map[string]any)
	if !ok {
		return fmt.Errorf("manifest dependencies object is missing")
	}
	delete(dependencies, gnfUnityCLIConnectorPackage)
	if err := writeJSONAtomic(manifestPath, manifest, 0600); err != nil {
		return err
	}
	lockPath := filepath.Join(workspace, "Packages", "packages-lock.json")
	lock, err := readJSONMap(lockPath)
	if err != nil {
		return err
	}
	lockDependencies, ok := lock["dependencies"].(map[string]any)
	if !ok {
		return fmt.Errorf("packages-lock dependencies object is missing")
	}
	entry, ok := lockDependencies[gnfUnityCLIConnectorPackage].(map[string]any)
	if !ok {
		return fmt.Errorf("packages-lock entry is missing: %s", gnfUnityCLIConnectorPackage)
	}
	entry["version"] = "file:" + gnfUnityCLIConnectorPackage
	entry["depth"] = float64(0)
	entry["source"] = "embedded"
	delete(entry, "hash")
	return writeJSONAtomic(lockPath, lock, 0600)
}

func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func prepareGNFWorkspacesRoot(artifactRoot string) (string, error) {
	artifactRoot = filepath.Clean(artifactRoot)
	if !filepath.IsAbs(artifactRoot) {
		return "", newError(CodeInvalidConfiguration, "prepare-gnf-workspaces-root", artifactRoot, fmt.Errorf("absolute artifact root required"))
	}
	artifactInfo, err := os.Lstat(artifactRoot)
	if err != nil || !artifactInfo.IsDir() {
		return "", newError(CodeInvalidConfiguration, "prepare-gnf-workspaces-root", artifactRoot, errors.Join(err, fmt.Errorf("artifact root must be an existing directory")))
	}
	artifactReparse, err := inspectPathReparse(artifactRoot)
	if err != nil || artifactReparse {
		return "", newError(CodeOwnershipMismatch, "prepare-gnf-workspaces-root", artifactRoot, errors.Join(err, fmt.Errorf("artifact root must be a real directory")))
	}
	root := filepath.Join(artifactRoot, "workspaces")
	if err := os.Mkdir(root, 0700); err != nil {
		return "", newError(CodeInvalidConfiguration, "create-gnf-workspaces-root", root, err)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", newError(CodeOwnershipMismatch, "verify-gnf-workspaces-root", root, errors.Join(err, fmt.Errorf("created workspace root is not a real directory")))
	}
	reparse, err := inspectPathReparse(root)
	if err != nil || reparse {
		return "", newError(CodeOwnershipMismatch, "verify-gnf-workspaces-root", root, errors.Join(err, fmt.Errorf("created workspace root is a reparse point")))
	}
	return root, nil
}

func runGNFSelectedTest(ctx context.Context, config GNFUnityConfig, workspace, platform string, expected []string, results, log string) (GNFUnityProcessEvidence, error) {
	evidence := GNFUnityProcessEvidence{GitLongPathsEnabled: true}
	if len(expected) == 0 {
		return evidence, newError(CodeDeterministicTestUnavailable, "run-gnf-selection", platform, fmt.Errorf("empty selection"))
	}
	executor := unityvhdxfixture.UnityExecutor{
		EditorPath: config.UnityEditorPath,
		Version:    unityvhdxfixture.TargetUnityVersion,
		Filter:     strings.Join(expected, ";"),
		Environment: map[string]string{
			"GIT_CONFIG_COUNT":   "1",
			"GIT_CONFIG_KEY_0":   "core.longpaths",
			"GIT_CONFIG_VALUE_0": "true",
		},
	}
	executor.OnStart = func(pid int, startedAt time.Time) { evidence.PID, evidence.StartedAt = pid, startedAt }
	runCtx, cancel := context.WithTimeout(ctx, config.TestTimeout)
	result, err := executor.RunTests(runCtx, workspace, platform, results, log)
	evidence.CompletedAt, evidence.TimedOut, evidence.Result = time.Now().UTC(), errors.Is(runCtx.Err(), context.DeadlineExceeded), result
	cancel()
	if err != nil {
		return evidence, err
	}
	if err := unityvhdxfixture.RequireExpectedTests(result, expected); err != nil {
		return evidence, err
	}
	if len(result.CompileErrors) != 0 {
		return evidence, fmt.Errorf("compile errors: %v", result.CompileErrors)
	}
	return evidence, nil
}

func measureGNFBudget(ctx context.Context, service *Service, paths Paths, config GNFUnityConfig, baseline *Baseline) (GNFBudgetEvidence, error) {
	evidence := GNFBudgetEvidence{SoftBudgetBytes: config.Pool.SoftBudgetBytes, WorkerReserveBytes: config.Pool.WorkerReserveBytes, BaselineLogicalBytes: baseline.Metadata.Library.LogicalBytes, OverrideUsed: config.PolicyOverride}
	used, err := newNativeWorkerStorageMeter(paths).VolumeUsedBytes(ctx)
	if err != nil {
		return evidence, err
	}
	evidence.RefsUsedBytes = used
	evidence.AvailableBytes = config.Pool.SoftBudgetBytes - used
	evidence.MinimumRequiredSoftBudgetBytes = used + config.Pool.WorkerReserveBytes
	if usage, usageErr := shadow.MeasureDirectoryUsage(baseline.LibraryPath); usageErr == nil {
		evidence.BaselineAllocatedBytes = usage.AllocatedBytes
	} else {
		return evidence, usageErr
	}
	if hostFree, hostErr := service.native.HostFreeBytes(paths.Root); hostErr == nil {
		evidence.HostFreeBytes = hostFree
	} else {
		return evidence, hostErr
	}
	if vhdx, vhdxErr := service.native.FileUsage(paths.VHDX); vhdxErr == nil {
		evidence.VHDXAllocatedBytes = vhdx.AllocatedBytes
	} else {
		return evidence, vhdxErr
	}
	evidence.ReservationPossible = evidence.AvailableBytes >= config.Pool.WorkerReserveBytes && evidence.HostFreeBytes >= config.Pool.MinimumHostFreeBytes+config.Pool.WorkerReserveBytes
	if !evidence.ReservationPossible {
		return evidence, newError(CodeStorageBudgetExceeded, "preflight-gnf-worker-budget", paths.PoolRoot, fmt.Errorf("used=%d budget=%d reserve=%d hostFree=%d", used, config.Pool.SoftBudgetBytes, config.Pool.WorkerReserveBytes, evidence.HostFreeBytes))
	}
	return evidence, nil
}

func isGNFBlockedCode(code string) bool {
	switch code {
	case CodeGNFProjectNotFound, CodeGNFSourceDirty, CodeGNFLocalPackageNotFound, CodeUnityVersionMismatch, CodeDeterministicTestUnavailable, CodeHostFreeSpaceFloor, CodeStorageBudgetExceeded:
		return true
	default:
		return false
	}
}
