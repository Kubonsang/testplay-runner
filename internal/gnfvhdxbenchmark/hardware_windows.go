//go:build windows

package gnfvhdxbenchmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/libraryimage"
	"github.com/Kubonsang/testplay-runner/internal/librarymaterializer"
	"github.com/Kubonsang/testplay-runner/internal/mountedcopy"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/storagehelper"
	"github.com/Kubonsang/testplay-runner/internal/unity"
	"github.com/Kubonsang/testplay-runner/internal/unityvhdxfixture"
	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

const defaultGNFParentBytes int64 = 16 << 30

type hardwareSession struct {
	config          HardwareConfig
	plan            Plan
	sessionID       string
	sessionRoot     string
	sourceProject   string
	seedProject     string
	baselineLibrary string
	storeRoot       string
	parentPath      string
	parent          unityvhdxfixture.ParentFixture
	imageStore      *libraryimage.Store
	image           *libraryimage.Image
	editor          unityvhdxfixture.UnityExecutor
	unityVersion    string
	beforeDisks     []unityvhdxfixture.DiskSnapshot
	originalHash    string
	previousMarkers []string
	reference       *SemanticResult
	seed            SeedEvidence
	sourceHash      string
}

func runHardware(ctx context.Context, config HardwareConfig) (summary Summary, returnErr error) {
	if config.ParentBytes == 0 {
		config.ParentBytes = defaultGNFParentBytes
	}
	if err := ValidateRoots(config.ProjectPath, config.WorkRoot, config.ArtifactRoot); err != nil {
		return summary, err
	}
	if err := RequireEmptyOrAbsent(config.WorkRoot); err != nil {
		return summary, err
	}
	if config.Mode != ModeSmoke && config.Mode != ModeFull {
		return summary, benchmarkError(CodeInvalidInput, "validate-mode", string(config.Mode), fmt.Errorf("smoke or full required"))
	}
	for name, value := range map[string]string{"editor": config.EditorPath, "project": config.ProjectPath, "helper": config.HelperPath} {
		if !filepath.IsAbs(value) {
			return summary, benchmarkError(CodeInvalidInput, "validate-"+name, value, fmt.Errorf("absolute path required"))
		}
		if _, err := os.Stat(value); err != nil {
			return summary, benchmarkError(CodeInvalidInput, "validate-"+name, value, err)
		}
	}
	revision, clean, err := gitSourceState(ctx, config.ProjectPath)
	if err != nil {
		return summary, benchmarkError(CodeInvalidInput, "inspect-source-git", config.ProjectPath, err)
	}
	if !clean {
		return summary, benchmarkError(CodeSourceDirty, "inspect-source-git", config.ProjectPath, fmt.Errorf("GNF_ working tree is dirty"))
	}
	if config.SourceRevision == "" || revision != config.SourceRevision {
		return summary, benchmarkError(CodeInvalidInput, "validate-source-revision", config.ProjectPath, fmt.Errorf("expected=%q actual=%q", config.SourceRevision, revision))
	}
	elevated, err := vhdxstorage.IsElevated(ctx)
	if err != nil || !elevated {
		return summary, benchmarkError(CodeInvalidInput, "validate-administrator", config.WorkRoot, fmt.Errorf("elevated Windows required: %v", err))
	}
	plan, err := BuildPlan(config.Mode)
	if err != nil {
		return summary, err
	}
	if err := os.Mkdir(config.WorkRoot, 0700); err != nil {
		return summary, err
	}
	sessionID := "session-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	sessionRoot := filepath.Join(config.ArtifactRoot, sessionID)
	if err := os.MkdirAll(sessionRoot, 0700); err != nil {
		return summary, err
	}
	s := &hardwareSession{config: config, plan: plan, sessionID: sessionID, sessionRoot: sessionRoot, sourceProject: filepath.Join(config.WorkRoot, "source-project"), seedProject: filepath.Join(config.WorkRoot, "seed-project"), baselineLibrary: filepath.Join(config.WorkRoot, "physical-library-baseline"), storeRoot: filepath.Join(config.WorkRoot, "storage"), parentPath: filepath.Join(config.WorkRoot, "storage", "parents", "gnf-library-parent.vhdx"), editor: unityvhdxfixture.UnityExecutor{EditorPath: config.EditorPath, Version: unityvhdxfixture.TargetUnityVersion, Marker: "gnf-seed"}}
	summary = Summary{SchemaVersion: EvidenceSchemaVersion, SessionID: sessionID, Mode: config.Mode, SourceRevision: config.SourceRevision, UnityVersion: unityvhdxfixture.TargetUnityVersion, Selection: plan.Selection, Verdict: "BLOCKED", PerformanceVerdict: "NOT MEASURED", CreatedAt: time.Now().UTC()}
	defer func() {
		if returnErr != nil {
			_ = s.writeSummary(summary)
		}
	}()
	if err := WriteJSON(filepath.Join(sessionRoot, "manifest.json"), map[string]any{"schemaVersion": EvidenceSchemaVersion, "sessionId": sessionID, "mode": config.Mode, "sourceProject": config.ProjectPath, "sourceRevision": config.SourceRevision, "unityEditorPath": config.EditorPath, "unityVersion": unityvhdxfixture.TargetUnityVersion, "selection": plan.Selection, "concurrency": 1}); err != nil {
		return summary, err
	}
	if err := WriteJSON(filepath.Join(sessionRoot, "plan.json"), plan); err != nil {
		return summary, err
	}
	if err := s.prepare(ctx); err != nil {
		return summary, err
	}
	summary.Seed = s.seed
	for _, spec := range plan.Runs {
		if spec.Phase == PhaseCold && spec.Order == 1 {
			if err := os.RemoveAll(filepath.Join(config.WorkRoot, "legacy-cache")); err != nil {
				return summary, benchmarkError(CodeCleanupFailed, "reset-legacy-cold-state", config.WorkRoot, err)
			}
		}
		run, runErr := s.runOne(ctx, spec)
		summary.Runs = append(summary.Runs, run)
		_ = WriteJSON(filepath.Join(s.runRoot(spec), "evidence.json"), run)
		if runErr != nil {
			summary.Verdict = "FAILED"
			return summary, runErr
		}
	}
	afterHash, err := hashProjectInputs(config.ProjectPath)
	if err != nil {
		return summary, err
	}
	if afterHash != s.originalHash {
		summary.Verdict = "FAILED"
		return summary, benchmarkError(CodeContamination, "verify-original-project", config.ProjectPath, fmt.Errorf("input hash changed"))
	}
	afterRevision, afterClean, err := gitSourceState(ctx, config.ProjectPath)
	if err != nil || !afterClean || afterRevision != config.SourceRevision {
		summary.Verdict = "FAILED"
		return summary, benchmarkError(CodeContamination, "verify-original-git-state", config.ProjectPath, fmt.Errorf("revision=%q clean=%t error=%v", afterRevision, afterClean, err))
	}
	afterDisks, err := unityvhdxfixture.FileBackedDisks(ctx)
	if err != nil {
		return summary, err
	}
	if !sameDiskSnapshots(s.beforeDisks, afterDisks) {
		summary.Verdict = "FAILED"
		return summary, benchmarkError(CodeCleanupFailed, "verify-virtual-disks", config.WorkRoot, fmt.Errorf("before=%v after=%v", s.beforeDisks, afterDisks))
	}
	summary.Verdict = "COMPATIBLE"
	if config.Mode == ModeFull {
		s.calculateWarmStatistics(&summary)
		summary.PerformanceVerdict = classifyPerformance(summary)
	}
	if err := removeOwnedWorkRoot(config.WorkRoot); err != nil {
		summary.Verdict = "FAILED"
		return summary, benchmarkError(CodeCleanupFailed, "remove-work-root", config.WorkRoot, err)
	}
	if err := s.writeSummary(summary); err != nil {
		return summary, err
	}
	return summary, nil
}

func (s *hardwareSession) prepare(ctx context.Context) error {
	var err error
	s.originalHash, err = hashProjectInputs(s.config.ProjectPath)
	if err != nil {
		return err
	}
	s.beforeDisks, err = unityvhdxfixture.FileBackedDisks(ctx)
	if err != nil {
		return err
	}
	started := time.Now()
	if err = copyProjectInputs(ctx, s.config.ProjectPath, s.sourceProject); err != nil {
		return err
	}
	copyMs := time.Since(started).Milliseconds()
	s.seed.FixtureCopyMs = i64(copyMs)
	if err = copyProjectInputs(ctx, s.sourceProject, s.seedProject); err != nil {
		return err
	}
	s.unityVersion, err = unityvhdxfixture.FixtureVersion(s.sourceProject)
	if err != nil {
		return err
	}
	if s.unityVersion != unityvhdxfixture.TargetUnityVersion {
		return benchmarkError(CodeInvalidInput, "validate-project-version", s.config.ProjectPath, fmt.Errorf("expected %s, got %s", unityvhdxfixture.TargetUnityVersion, s.unityVersion))
	}
	if err = s.editor.ValidateVersion(ctx, s.unityVersion); err != nil {
		return err
	}
	seedLog := filepath.Join(s.sessionRoot, "seed", "editor.log")
	parent, err := unityvhdxfixture.PrepareParent(ctx, s.parentPath, filepath.Join(s.seedProject, "Library"), s.config.ParentBytes, func(libraryPath string) error {
		seedMs, runErr := s.editor.RunCompile(ctx, s.seedProject, seedLog)
		s.seed.UnitySeedImportMs = i64(seedMs)
		if runErr != nil {
			return runErr
		}
		copyStarted := time.Now()
		if _, copyErr := mountedcopy.Contents(ctx, libraryPath, s.baselineLibrary); copyErr != nil {
			return copyErr
		}
		s.seed.PhysicalImageMaterializeMs = i64(time.Since(copyStarted).Milliseconds())
		return ValidateWarmLibrary(s.baselineLibrary)
	})
	if err != nil {
		return err
	}
	s.parent = parent
	if err = ValidateWarmLibrary(s.baselineLibrary); err != nil {
		return err
	}
	if err = os.RemoveAll(s.seedProject); err != nil {
		return err
	}
	if err = ValidateWarmLibrary(s.baselineLibrary); err != nil {
		return err
	}
	s.seed.ParentCreateMs = i64(parent.CreateMs)
	s.seed.ParentAttachMs = i64(parent.AttachMs)
	s.seed.ParentInitializeMs = i64(parent.InitializeMs)
	s.seed.ParentDetachMs = i64(parent.DetachMs)
	s.seed.ParentVirtualBytes = i64(parent.VirtualBytes)
	s.seed.ParentLogicalBytes = i64(parent.LogicalBytes)
	s.seed.ParentAllocatedBytes = parent.AllocatedBytes
	s.seed.ParentHash = parent.Hash
	s.imageStore = libraryimage.NewStoreAt(filepath.Join(s.config.WorkRoot, "physical-image-store"))
	key, err := libraryimage.ComputeKey(s.sourceProject, s.config.EditorPath)
	if err != nil {
		return err
	}
	s.image, err = s.imageStore.Create(ctx, key, s.baselineLibrary)
	if err != nil {
		return err
	}
	s.sourceHash, err = hashProjectInputs(s.sourceProject)
	if err != nil {
		return err
	}
	return nil
}

func (s *hardwareSession) runOne(ctx context.Context, spec RunSpec) (run RunEvidence, returnErr error) {
	started := time.Now()
	run = RunEvidence{SchemaVersion: EvidenceSchemaVersion, Spec: spec, SourceRevision: s.config.SourceRevision, UnityVersion: s.unityVersion, Selection: s.plan.Selection}
	defer func() {
		run.Metrics.TotalWallClockMs = i64(time.Since(started).Milliseconds())
		run.Outliers = metricOutliers(run.Metrics)
		if returnErr != nil {
			var structured *Error
			if errors.As(returnErr, &structured) {
				run.Error = structured
			} else {
				run.Error = &Error{Code: CodeInvalidInput, Operation: "run", Cause: returnErr.Error()}
			}
		}
	}()
	var result SemanticResult
	var err error
	switch spec.Backend {
	case BackendLegacy:
		result, err = s.runLegacy(ctx, spec, &run)
	case BackendPhysical:
		result, err = s.runPhysical(ctx, spec, &run)
	case BackendVHDX:
		result, err = s.runVHDX(ctx, spec, &run)
	default:
		err = benchmarkError(CodeInvalidInput, "select-backend", string(spec.Backend), nil)
	}
	if err != nil {
		return run, err
	}
	if !run.CleanupPassed {
		return run, benchmarkError(CodeCleanupFailed, "verify-backend-cleanup", spec.ID, fmt.Errorf("backend did not confirm cleanup"))
	}
	run.Result = &result
	run.Metrics.UnityWallClockMs = i64(result.WallClockMs)
	currentSourceHash, hashErr := hashProjectInputs(s.sourceProject)
	if hashErr != nil {
		return run, hashErr
	}
	if currentSourceHash != s.sourceHash {
		return run, benchmarkError(CodeContamination, "verify-benchmark-source", s.sourceProject, fmt.Errorf("Assets, Packages, or ProjectSettings changed"))
	}
	if s.reference == nil {
		if spec.Backend != BackendLegacy || spec.Phase != PhaseGate {
			return run, benchmarkError(CodeSemanticMismatch, "establish-reference", spec.ID, fmt.Errorf("first run must be Legacy gate"))
		}
		copy := result
		s.reference = &copy
	}
	parity := CompareSemantic(*s.reference, result) == nil
	run.SemanticParity = &parity
	if !parity {
		return run, CompareSemantic(*s.reference, result)
	}
	return run, nil
}

func (s *hardwareSession) runLegacy(ctx context.Context, spec RunSpec, run *RunEvidence) (SemanticResult, error) {
	cacheRoot := filepath.Join(s.config.WorkRoot, "legacy-cache")
	opts := legacyPrepareOptions(s.sourceProject, cacheRoot)
	started := time.Now()
	ws, err := shadow.Prepare(ctx, s.sourceProject, spec.ID, opts)
	if err != nil {
		return SemanticResult{}, err
	}
	run.Metrics.WorkspacePrepareMs = i64(time.Since(started).Milliseconds())
	run.Metrics.ProjectCopyMs = i64((ws.Metrics.AssetsCopy + ws.Metrics.ProjectSettingsCopy + ws.Metrics.PackagesCopy).Milliseconds())
	run.Metrics.LibraryRestoreMs = i64(ws.Metrics.LibraryMaterialize.Milliseconds())
	defer func() {
		cleanupStarted := time.Now()
		if cleanupErr := ws.Cleanup(); cleanupErr == nil {
			run.CleanupPassed = true
		}
		run.Metrics.CleanupMs = i64(time.Since(cleanupStarted).Milliseconds())
	}()
	result, err := s.runUnity(ctx, ws.ShadowPath, spec)
	if err != nil {
		return result, err
	}
	started = time.Now()
	if err = ws.UpdateLibraryCache(ctx); err != nil {
		return result, err
	}
	run.Metrics.CacheWriteBackMs = i64(time.Since(started).Milliseconds())
	run.Metrics.CacheWritePeakBytes = i64(ws.Metrics.LegacyCacheWritePeakPhysicalBytes)
	usage, _ := shadow.MeasureDirectoryUsage(ws.ShadowPath)
	run.Metrics.LogicalBytes = i64(usage.LogicalBytes)
	run.Metrics.AllocatedBytes = i64(usage.AllocatedBytes)
	return result, nil
}

func legacyPrepareOptions(sourceProject, cacheRoot string) shadow.PrepareOptions {
	opts := shadow.PrepareOptions{
		LibraryCacheRoot: cacheRoot,
		CopyPackages:     true,
	}
	if shadow.ValidateCacheAt(sourceProject, cacheRoot) {
		opts.LibraryCacheDir = shadow.CacheLibraryDirAt(cacheRoot)
	}
	return opts
}

func (s *hardwareSession) runPhysical(ctx context.Context, spec RunSpec, run *RunEvidence) (SemanticResult, error) {
	started := time.Now()
	ws, err := shadow.Prepare(ctx, s.sourceProject, spec.ID, shadow.PrepareOptions{CopyPackages: true})
	if err != nil {
		return SemanticResult{}, err
	}
	run.Metrics.WorkspacePrepareMs = i64(time.Since(started).Milliseconds())
	run.Metrics.ProjectCopyMs = i64((ws.Metrics.AssetsCopy + ws.Metrics.ProjectSettingsCopy + ws.Metrics.PackagesCopy).Milliseconds())
	defer func() {
		cleanupStarted := time.Now()
		if cleanupErr := ws.Cleanup(); cleanupErr == nil {
			run.CleanupPassed = true
		}
		run.Metrics.CleanupMs = i64(time.Since(cleanupStarted).Milliseconds())
	}()
	verifyStarted := time.Now()
	resolution, err := s.imageStore.Verify(ctx, s.image)
	run.Metrics.ImageValidationMs = i64(time.Since(verifyStarted).Milliseconds())
	if err != nil || resolution.Status != libraryimage.StatusValid {
		return SemanticResult{}, fmt.Errorf("verify image: status=%s reason=%s error=%v", resolution.Status, resolution.Reason, err)
	}
	libraryPath := filepath.Join(ws.ShadowPath, "Library")
	if err = os.RemoveAll(libraryPath); err != nil {
		return SemanticResult{}, err
	}
	materializer := librarymaterializer.PhysicalCopyMaterializer{}
	materialized, err := materializer.Materialize(ctx, librarymaterializer.Request{SourcePath: s.image.LibraryPath, DestinationPath: libraryPath})
	if materialized != nil {
		run.Metrics.PhysicalMaterializeMs = i64(materialized.Duration.Milliseconds())
	}
	if err != nil {
		return SemanticResult{}, err
	}
	if err = ValidateWarmLibrary(libraryPath); err != nil {
		return SemanticResult{}, err
	}
	result, err := s.runUnity(ctx, ws.ShadowPath, spec)
	if err != nil {
		return result, err
	}
	usage, _ := shadow.MeasureDirectoryUsage(ws.ShadowPath)
	run.Metrics.LogicalBytes = i64(usage.LogicalBytes)
	run.Metrics.AllocatedBytes = i64(usage.AllocatedBytes)
	return result, nil
}

func (s *hardwareSession) runVHDX(ctx context.Context, spec RunSpec, run *RunEvidence) (result SemanticResult, returnErr error) {
	workspace := filepath.Join(s.config.WorkRoot, "vhdx-workspaces", spec.ID)
	started := time.Now()
	if err := copyProjectInputs(ctx, s.sourceProject, workspace); err != nil {
		return result, err
	}
	run.Metrics.WorkspacePrepareMs = i64(time.Since(started).Milliseconds())
	run.Metrics.ProjectCopyMs = run.Metrics.WorkspacePrepareMs
	childPath := filepath.Join(s.storeRoot, "children", spec.ID+".vhdx")
	mountPath := filepath.Join(workspace, "Library")
	helperDir := filepath.Join(s.runRoot(spec), "helper")
	helper, err := unityvhdxfixture.StartHelper(ctx, s.config.HelperPath, helperDir)
	if err != nil {
		return result, err
	}
	run.Metrics.HelperStartupMs = i64(helper.StartupMs())
	var lease storagehelper.WorkspaceLease
	released := false
	defer func() {
		if !released && lease.LeaseID != "" {
			_, _ = helper.Call(storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationRelease, RequestID: "emergency-release-" + spec.ID, LeaseID: lease.LeaseID})
		}
		if !released {
			_, _ = helper.Call(storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationShutdown, RequestID: "emergency-shutdown-" + spec.ID})
		}
		_ = helper.CloseInput()
		_ = helper.Wait()
		if released {
			cleanupStarted := time.Now()
			if err := os.RemoveAll(workspace); err == nil {
				run.CleanupPassed = true
			} else if returnErr == nil {
				returnErr = benchmarkError(CodeCleanupFailed, "remove-vhdx-workspace", workspace, err)
			}
			if run.Metrics.CleanupMs == nil {
				run.Metrics.CleanupMs = i64(time.Since(cleanupStarted).Milliseconds())
			}
		}
	}()
	hello, err := helper.Call(storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationHello, RequestID: "hello-" + spec.ID})
	if err != nil || hello.Elevated == nil || !*hello.Elevated {
		return result, fmt.Errorf("helper hello: response=%#v error=%v", hello, err)
	}
	request := storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationAcquire, RequestID: "acquire-" + spec.ID, StoreRoot: s.storeRoot, WorkspaceRoot: workspace, ParentPath: s.parentPath, ChildPath: childPath, MountPath: mountPath, DeleteChildOnRelease: true}
	response, err := helper.Call(request)
	if err != nil || response.Lease == nil || response.Lease.State != storagehelper.StateReady {
		return result, fmt.Errorf("helper acquire: response=%#v error=%v", response, err)
	}
	lease = *response.Lease
	journalPath := filepath.Join(s.storeRoot, "leases", lease.LeaseID+".json")
	copyAcquireRunMetrics(&run.Metrics, response)
	ready, err := unityvhdxfixture.InspectActiveMount(ctx, lease, "ready")
	if err != nil {
		return result, benchmarkError(CodeMountIntegrity, "inspect-ready", mountPath, err)
	}
	run.Mounts = append(run.Mounts, mountIdentity(ready, lease, journalPath))
	mountOK := true
	run.MountIntegrity = &mountOK
	beforeUnity, err := unityvhdxfixture.InspectActiveMount(ctx, lease, "before-unity")
	if err != nil {
		return result, benchmarkError(CodeMountIntegrity, "inspect-before-unity", mountPath, err)
	}
	run.Mounts = append(run.Mounts, mountIdentity(beforeUnity, lease, journalPath))
	markerRoot := filepath.Join(mountPath, "TestPlayGNF", "markers")
	if entries, readErr := os.ReadDir(markerRoot); readErr == nil && len(entries) > 0 {
		return result, benchmarkError(CodeContamination, "inspect-new-child", markerRoot, fmt.Errorf("new Child contains old markers"))
	} else if readErr != nil && !os.IsNotExist(readErr) {
		return result, readErr
	}
	if err = os.MkdirAll(markerRoot, 0700); err != nil {
		return result, err
	}
	marker := spec.ID
	if err = os.WriteFile(filepath.Join(markerRoot, marker+".txt"), []byte(marker), 0600); err != nil {
		return result, err
	}
	result, err = s.runUnity(ctx, workspace, spec)
	if err != nil {
		return result, err
	}
	afterUnity, err := unityvhdxfixture.InspectActiveMount(ctx, lease, "after-unity")
	if err != nil {
		return result, benchmarkError(CodeMountIntegrity, "inspect-after-unity", mountPath, err)
	}
	run.Mounts = append(run.Mounts, mountIdentity(afterUnity, lease, journalPath))
	run.Metrics.ChildAfterUnityLogical, run.Metrics.ChildAfterUnityAllocated = unityvhdxfixture.FileSizes(childPath)
	data, err := os.ReadFile(filepath.Join(markerRoot, marker+".txt"))
	if err != nil || string(data) != marker {
		return result, benchmarkError(CodeContamination, "verify-current-marker", markerRoot, fmt.Errorf("data=%q error=%v", data, err))
	}
	beforeRelease, err := unityvhdxfixture.InspectActiveMount(ctx, lease, "before-release")
	if err != nil {
		return result, benchmarkError(CodeMountIntegrity, "inspect-before-release", mountPath, err)
	}
	run.Mounts = append(run.Mounts, mountIdentity(beforeRelease, lease, journalPath))
	releaseStarted := time.Now()
	release, err := helper.Call(storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationRelease, RequestID: "release-" + spec.ID, LeaseID: lease.LeaseID})
	run.Metrics.HelperReleaseMs = i64(time.Since(releaseStarted).Milliseconds())
	if err != nil || !release.Released {
		return result, fmt.Errorf("helper release: response=%#v error=%v", release, err)
	}
	copyReleaseRunMetrics(&run.Metrics, release)
	shutdownErr := error(nil)
	_, shutdownErr = helper.Call(storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationShutdown, RequestID: "shutdown-" + spec.ID})
	_ = helper.CloseInput()
	waitErr := helper.Wait()
	if shutdownErr != nil || waitErr != nil {
		return result, errors.Join(shutdownErr, waitErr)
	}
	released = true
	releasedMount, err := unityvhdxfixture.InspectReleasedMount(lease)
	if err != nil {
		return result, err
	}
	run.Mounts = append(run.Mounts, mountIdentity(releasedMount, lease, journalPath))
	journal, err := readJournal(journalPath)
	if err != nil || journal.State != storagehelper.StateReleased {
		return result, benchmarkError(CodeCleanupFailed, "verify-journal", journalPath, fmt.Errorf("state=%q error=%v", journal.State, err))
	}
	parentHash, err := unityvhdxfixture.HashFile(s.parentPath)
	if err != nil {
		return result, err
	}
	if err = VerifyParentHash(s.parent.Hash, parentHash); err != nil {
		return result, err
	}
	inspectMount := filepath.Join(s.config.WorkRoot, "parent-inspect-"+spec.ID)
	parentMarker, err := unityvhdxfixture.InspectParentFile(ctx, s.parentPath, inspectMount, filepath.Join("TestPlayGNF", "markers", marker+".txt"))
	if err != nil {
		return result, err
	}
	afterInspect, err := unityvhdxfixture.HashFile(s.parentPath)
	if err != nil {
		return result, err
	}
	if parentMarker {
		return result, benchmarkError(CodeContamination, "inspect-parent-marker", s.parentPath, fmt.Errorf("marker visible"))
	}
	if err = VerifyParentHash(s.parent.Hash, afterInspect); err != nil {
		return result, err
	}
	isolated := true
	run.ParentIsolation = &isolated
	s.previousMarkers = append(s.previousMarkers, marker)
	if _, err = os.Stat(childPath); err == nil {
		run.ResidualChildCount = 1
	} else if !os.IsNotExist(err) {
		return result, err
	}
	if _, err = os.Stat(mountPath); err == nil {
		run.ResidualMountCount = 1
	}
	orphans, err := storagehelper.NewJournalStore().FindOrphans(s.storeRoot)
	if err != nil {
		return result, err
	}
	run.ResidualJournalCount = len(orphans)
	afterDisks, err := unityvhdxfixture.FileBackedDisks(ctx)
	if err != nil {
		return result, err
	}
	if !sameDiskSnapshots(s.beforeDisks, afterDisks) {
		run.ResidualDiskCount = 1
	}
	if run.ResidualChildCount+run.ResidualMountCount+run.ResidualJournalCount+run.ResidualDiskCount != 0 {
		return result, benchmarkError(CodeCleanupFailed, "verify-vhdx-residuals", workspace, fmt.Errorf("disk=%d mount=%d child=%d journal=%d", run.ResidualDiskCount, run.ResidualMountCount, run.ResidualChildCount, run.ResidualJournalCount))
	}
	return result, nil
}

func (s *hardwareSession) runUnity(ctx context.Context, project string, spec RunSpec) (SemanticResult, error) {
	runRoot := s.runRoot(spec)
	if err := os.MkdirAll(runRoot, 0700); err != nil {
		return SemanticResult{}, err
	}
	results := filepath.Join(runRoot, "results.xml")
	logPath := filepath.Join(runRoot, "editor.log")
	stdoutPath := filepath.Join(runRoot, "stdout.log")
	stderrPath := filepath.Join(runRoot, "stderr.log")
	stdout, _ := os.Create(stdoutPath)
	stderr, _ := os.Create(stderrPath)
	defer stdout.Close()
	defer stderr.Close()
	args := unity.BuildRunArgs(project, &unity.RunOptions{ResultsFilePath: results, TestPlatform: SelectionPlatform, Filter: SelectionFilter})
	args = append(args, "-disable-assembly-updater", "-logFile", logPath)
	started := time.Now()
	runner := &unity.ProcessRunner{UnityPath: s.config.EditorPath}
	exitCode, runErr := runner.Run(ctx, args, stdout, stderr)
	wall := time.Since(started).Milliseconds()
	if runErr != nil {
		return SemanticResult{}, runErr
	}
	return ParseSemantic(exitCode, results, logPath, wall)
}

func (s *hardwareSession) runRoot(spec RunSpec) string {
	return filepath.Join(s.sessionRoot, string(spec.Backend), spec.ID)
}

func (s *hardwareSession) calculateWarmStatistics(summary *Summary) {
	summary.WarmTotalMs = map[Backend]Statistics{}
	summary.WarmUnityMs = map[Backend]Statistics{}
	for _, backend := range baseOrder {
		var totals, unityValues []float64
		for _, run := range summary.Runs {
			if run.Spec.Phase == PhaseWarm && run.Spec.Backend == backend {
				if run.Metrics.TotalWallClockMs != nil {
					totals = append(totals, float64(*run.Metrics.TotalWallClockMs))
				}
				if run.Metrics.UnityWallClockMs != nil {
					unityValues = append(unityValues, float64(*run.Metrics.UnityWallClockMs))
				}
			}
		}
		if stats, err := CalculateStatistics(totals); err == nil {
			summary.WarmTotalMs[backend] = stats
		}
		if stats, err := CalculateStatistics(unityValues); err == nil {
			summary.WarmUnityMs[backend] = stats
		}
	}
}

func classifyPerformance(summary Summary) string {
	legacy, lok := summary.WarmTotalMs[BackendLegacy]
	physical, pok := summary.WarmTotalMs[BackendPhysical]
	vhdx, vok := summary.WarmTotalMs[BackendVHDX]
	if !lok || !pok || !vok {
		return "NOT MEASURED"
	}
	if vhdx.Median < legacy.Median && vhdx.Median < physical.Median {
		return "BENEFICIAL"
	}
	if vhdx.Median > legacy.Median && vhdx.Median > physical.Median {
		return "REGRESSION"
	}
	return "NEUTRAL"
}

func (s *hardwareSession) writeSummary(summary Summary) error {
	if err := WriteJSON(filepath.Join(s.sessionRoot, "summary.json"), summary); err != nil {
		return err
	}
	if err := WriteRunsCSV(filepath.Join(s.sessionRoot, "runs.csv"), summary.Runs); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.sessionRoot, "summary.md"), []byte(SummaryMarkdown(summary)), 0600)
}

func copyProjectInputs(ctx context.Context, source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("destination exists: %s", destination)
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(destination)
		}
	}()
	for _, name := range []string{"Assets", "Packages", "ProjectSettings"} {
		sourcePath := filepath.Join(source, name)
		destinationPath := filepath.Join(destination, name)
		if _, err := shadow.CopyDirParallelWithStats(ctx, sourcePath, destinationPath, 0); err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
	}
	succeeded = true
	return nil
}

func removeOwnedWorkRoot(path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean == filepath.VolumeName(clean)+string(os.PathSeparator) {
		return fmt.Errorf("unsafe work root: %s", path)
	}
	info, err := os.Lstat(clean)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	reparse, err := mountedcopy.IsReparsePoint(clean)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || reparse {
		return fmt.Errorf("work root is not an ordinary owned directory")
	}
	return os.RemoveAll(clean)
}

func hashProjectInputs(project string) (string, error) {
	hash := sha256.New()
	for _, rootName := range []string{"Assets", "Packages", "ProjectSettings"} {
		root := filepath.Join(project, rootName)
		var paths []string
		err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.Mode().IsRegular() {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		sort.Strings(paths)
		for _, path := range paths {
			relative, _ := filepath.Rel(project, path)
			_, _ = io.WriteString(hash, filepath.ToSlash(relative)+"\x00")
			file, err := os.Open(path)
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				return "", errors.Join(copyErr, closeErr)
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func gitSourceState(ctx context.Context, project string) (string, bool, error) {
	revisionOutput, err := exec.CommandContext(ctx, "git", "-C", project, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("rev-parse: %w: %s", err, strings.TrimSpace(string(revisionOutput)))
	}
	statusOutput, err := exec.CommandContext(ctx, "git", "-C", project, "status", "--porcelain").CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("status: %w: %s", err, strings.TrimSpace(string(statusOutput)))
	}
	return strings.TrimSpace(string(revisionOutput)), strings.TrimSpace(string(statusOutput)) == "", nil
}

func readJournal(path string) (storagehelper.Journal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return storagehelper.Journal{}, err
	}
	var journal storagehelper.Journal
	err = json.Unmarshal(data, &journal)
	return journal, err
}
func sameDiskSnapshots(left, right []unityvhdxfixture.DiskSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
func mountIdentity(snapshot unityvhdxfixture.MountSnapshot, lease storagehelper.WorkspaceLease, journal string) MountIdentity {
	number, _ := vhdxstorage.DiskNumberFromPhysicalPath(lease.PhysicalPath)
	return MountIdentity{Phase: snapshot.Phase, DiskNumber: number, VolumeGUIDPath: snapshot.VolumeGUIDPath, MountPath: snapshot.MountPath, ChildPath: lease.ChildPath, LeaseID: lease.LeaseID, JournalPath: journal, Exists: snapshot.Exists}
}
func copyAcquireRunMetrics(target *RunMetrics, response storagehelper.Response) {
	if response.Metrics == nil {
		return
	}
	m := response.Metrics
	target.HelperAcquireMs = m.AcquireWallClockMs
	target.ChildCreateMs = m.ChildCreateMs
	target.ChildOpenMs = m.ChildOpenMs
	target.ChildAttachMs = m.AttachCallMs
	target.PhysicalPathResolveMs = m.PhysicalPathResolveMs
	target.PnPDiscoveryWaitMs = m.PnPDiscoveryWaitMs
	target.VolumeReadyWaitMs = m.VolumeReadyWaitMs
	target.MountResolveMs = m.MountCallMs
	target.MountVisibilityWaitMs = m.MountVisibilityWaitMs
	target.ChildInitialLogical = m.ChildReadyLogicalBytes
	target.ChildInitialAllocated = m.ChildReadyAllocatedBytes
}
func copyReleaseRunMetrics(target *RunMetrics, response storagehelper.Response) {
	if response.Metrics == nil {
		return
	}
	m := response.Metrics
	target.ChildDetachMs = m.DetachCallMs
	target.DetachVisibilityWaitMs = m.DetachVisibilityWaitMs
	if target.CleanupMs == nil {
		target.CleanupMs = m.CleanupMs
	}
}
func metricOutliers(metrics RunMetrics) []string {
	values := map[string]*int64{"totalWallClockMs": metrics.TotalWallClockMs, "workspacePrepareMs": metrics.WorkspacePrepareMs, "unityWallClockMs": metrics.UnityWallClockMs, "cleanupMs": metrics.CleanupMs, "helperAcquireMs": metrics.HelperAcquireMs}
	var result []string
	for name, value := range values {
		if value != nil && *value >= 30000 {
			result = append(result, fmt.Sprintf("%s=%d", name, *value))
		}
	}
	sort.Strings(result)
	return result
}
func i64(value int64) *int64 { return &value }
