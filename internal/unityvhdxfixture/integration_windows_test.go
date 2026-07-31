//go:build windows && unity_vhdx_integration

package unityvhdxfixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/librarymaterializer"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/storagehelper"
	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

const (
	fixtureRootEnv  = "TESTPLAY_UNITY_VHDX_FIXTURE_ROOT"
	artifactRootEnv = "TESTPLAY_UNITY_VHDX_ARTIFACT_ROOT"
	helperPathEnv   = "TESTPLAY_STORAGE_HELPER_PATH"
	repoRootEnv     = "TESTPLAY_UNITY_VHDX_REPO_ROOT"
	countEnv        = "TESTPLAY_UNITY_VHDX_COUNT"
	reuseParentEnv  = "TESTPLAY_UNITY_VHDX_REUSE_PARENT"
	parentSizeEnv   = "TESTPLAY_UNITY_PARENT_VHDX_SIZE_GIB"
)

type sharedFixture struct {
	root            string
	artifactRoot    string
	repoRoot        string
	fixtureSource   string
	storeRoot       string
	parentPath      string
	baselineLibrary string
	parent          ParentFixture
	beforeDisks     []DiskSnapshot
	editor          UnityExecutor
	fixtureVersion  string
	seedImportMs    int64
}

type integrationDriver struct {
	shared          *sharedFixture
	runID           string
	runRoot         string
	physicalProject string
	vhdxProject     string
	childPath       string
	mountPath       string
	marker          string
	helperPath      string
	helper          *HelperClient
	lease           storagehelper.WorkspaceLease
	journalPath     string
	markerObserved  bool
	released        bool
	releaseAttempts int
	physicalLogs    []string
	vhdxLogs        []string
}

func TestUnityVHDXLibraryFixture(t *testing.T) {
	root := requiredIntegrationPath(t, fixtureRootEnv, true)
	artifactRoot := requiredIntegrationPath(t, artifactRootEnv, false)
	helperPath := requiredIntegrationPath(t, helperPathEnv, false)
	if pathsOverlap(root, artifactRoot) {
		t.Fatal("fixture root and artifact root must not contain one another")
	}
	editorPath, err := ResolveUnityEditor()
	if err != nil {
		t.Fatal(err)
	}
	elevated, err := vhdxstorage.IsElevated(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !elevated {
		t.Fatal("elevated PowerShell is required")
	}
	repoRoot := os.Getenv(repoRootEnv)
	if repoRoot == "" {
		repoRoot, err = filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatal(err)
		}
	}
	fixtureSource := filepath.Join(repoRoot, "testdata", "unity-vhdx-fixture")
	if err := ValidateFixtureSource(fixtureSource); err != nil {
		t.Fatal(err)
	}
	fixtureVersion, err := FixtureVersion(fixtureSource)
	if err != nil {
		t.Fatal(err)
	}
	editor := UnityExecutor{EditorPath: editorPath, Version: TargetUnityVersion, Marker: "seed"}
	if err := editor.ValidateVersion(context.Background(), fixtureVersion); err != nil {
		t.Fatal(err)
	}
	count := integrationCount(t)
	reuseParent := os.Getenv(reuseParentEnv) == "1"
	if count > 1 && !reuseParent {
		t.Fatal("multiple Child runs require TESTPLAY_UNITY_VHDX_REUSE_PARENT=1")
	}
	before, err := FileBackedDisks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	shared := &sharedFixture{root: root, artifactRoot: artifactRoot, repoRoot: repoRoot, fixtureSource: fixtureSource, storeRoot: filepath.Join(root, "store"), parentPath: filepath.Join(root, "store", "parents", "unity-library-parent.vhdx"), baselineLibrary: filepath.Join(root, "physical-library-baseline"), beforeDisks: before, editor: editor, fixtureVersion: fixtureVersion}
	sessionStarted := time.Now()
	if err := shared.prepareParent(context.Background(), parentSizeBytes(t)); err != nil {
		t.Fatalf("prepare Parent: %v", err)
	}
	for index := 1; index <= count; index++ {
		runStarted := time.Now()
		if index == 1 {
			runStarted = sessionStarted
		}
		runID := fmt.Sprintf("unity-vhdx-%s-%02d", time.Now().UTC().Format("20060102T150405.000000000Z"), index)
		driver := newIntegrationDriver(shared, runID, helperPath)
		evidence := NewEvidence(runID)
		evidencePath := filepath.Join(driver.runRoot, "evidence.json")
		if err := Run(context.Background(), driver, &evidence, RunOptions{EvidencePath: evidencePath, StartedAt: runStarted}); err != nil {
			t.Fatalf("run %d/%d failed: %v; evidence=%s", index, count, err, evidencePath)
		}
		if !evidence.SemanticParity || !evidence.ParentIsolation || !evidence.MountIntegrity || !evidence.CleanupPassed {
			t.Fatalf("run %d/%d incomplete: %#v", index, count, evidence)
		}
		if err := removeRunProjects(shared.root, driver.physicalProject, driver.vhdxProject); err != nil {
			t.Fatal(err)
		}
	}
	after, err := FileBackedDisks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !sameDisks(before, after) {
		t.Fatalf("File Backed Virtual disks changed: before=%v after=%v", before, after)
	}
	if err := safeRemoveFixtureRoot(root); err != nil {
		t.Fatal(err)
	}
}

func (s *sharedFixture) prepareParent(ctx context.Context, size int64) error {
	seedProject := filepath.Join(s.root, "seed-project")
	if _, err := CopyFixtureProject(ctx, s.fixtureSource, seedProject); err != nil {
		return err
	}
	seedLog := filepath.Join(s.artifactRoot, "seed-"+time.Now().UTC().Format("20060102T150405.000000000Z"), "seed-editor.log")
	parentMount := filepath.Join(seedProject, "Library")
	materializer := librarymaterializer.PhysicalCopyMaterializer{}
	parent, err := PrepareParent(ctx, s.parentPath, parentMount, size, func(libraryPath string) error {
		seedImportMs, err := s.editor.RunCompile(ctx, seedProject, seedLog)
		if err != nil {
			return err
		}
		s.seedImportMs = seedImportMs
		_, err = materializer.Materialize(ctx, librarymaterializer.Request{SourcePath: libraryPath, DestinationPath: s.baselineLibrary})
		return err
	})
	if err != nil {
		return err
	}
	s.parent = parent
	return os.RemoveAll(seedProject)
}

func newIntegrationDriver(shared *sharedFixture, runID, helperPath string) *integrationDriver {
	return &integrationDriver{shared: shared, runID: runID, runRoot: filepath.Join(shared.artifactRoot, runID), physicalProject: filepath.Join(shared.root, "physical-project-"+runID), vhdxProject: filepath.Join(shared.root, "vhdx-project-"+runID), childPath: filepath.Join(shared.storeRoot, "children", runID+".vhdx"), marker: runID, helperPath: helperPath}
}

func (d *integrationDriver) Prepare(ctx context.Context, evidence *Evidence) error {
	started := time.Now()
	if _, err := CopyFixtureProject(ctx, d.shared.fixtureSource, d.physicalProject); err != nil {
		return err
	}
	if _, err := CopyFixtureProject(ctx, d.shared.fixtureSource, d.vhdxProject); err != nil {
		return err
	}
	evidence.Metrics.FixtureCopyMs = milliseconds(time.Since(started).Milliseconds())
	materialized, err := (librarymaterializer.PhysicalCopyMaterializer{}).Materialize(ctx, librarymaterializer.Request{SourcePath: d.shared.baselineLibrary, DestinationPath: filepath.Join(d.physicalProject, "Library")})
	if err != nil {
		return err
	}
	evidence.Metrics.PhysicalLibraryMaterializeMs = milliseconds(materialized.Duration.Milliseconds())
	usage, err := shadow.MeasureDirectoryUsage(filepath.Join(d.physicalProject, "Library"))
	if err != nil {
		return err
	}
	evidence.Metrics.PhysicalLibraryLogicalBytes = milliseconds(usage.LogicalBytes)
	evidence.Metrics.PhysicalLibraryAllocatedBytes = milliseconds(usage.AllocatedBytes)
	d.mountPath = filepath.Join(d.vhdxProject, "Library")
	if err := os.MkdirAll(filepath.Dir(d.childPath), 0700); err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(d.runRoot, "physical"), filepath.Join(d.runRoot, "vhdx"), filepath.Join(d.runRoot, "helper"), filepath.Join(d.runRoot, "mount")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
	}
	if err := WriteJSON(filepath.Join(d.runRoot, "mount", "before.json"), MountSnapshot{Phase: "before", Exists: false, MountPath: d.mountPath}); err != nil {
		return err
	}
	evidence.UnityVersion = d.shared.fixtureVersion
	evidence.UnityEditorPath = d.shared.editor.EditorPath
	evidence.FixtureProjectVersion = d.shared.fixtureVersion
	evidence.SeedProjectPath = filepath.Join(d.shared.root, "seed-project")
	evidence.PhysicalProjectPath = d.physicalProject
	evidence.VHDXProjectPath = d.vhdxProject
	evidence.ParentVirtualBytes = d.shared.parent.VirtualBytes
	evidence.ParentLogicalBytes = d.shared.parent.LogicalBytes
	evidence.ParentAllocatedBytes = d.shared.parent.AllocatedBytes
	evidence.ParentHashBefore = d.shared.parent.Hash
	evidence.Metrics.ParentCreateMs = milliseconds(d.shared.parent.CreateMs)
	evidence.Metrics.ParentAttachMs = milliseconds(d.shared.parent.AttachMs)
	evidence.Metrics.ParentInitializeMs = milliseconds(d.shared.parent.InitializeMs)
	evidence.Metrics.UnitySeedImportMs = milliseconds(d.shared.seedImportMs)
	evidence.Metrics.ParentDetachMs = milliseconds(d.shared.parent.DetachMs)
	evidence.Metrics.ParentLibraryLogicalBytes = milliseconds(d.shared.parent.LibraryLogicalBytes)
	return nil
}

func (d *integrationDriver) RunPhysical(ctx context.Context, platform string) (PlatformResult, error) {
	dir := filepath.Join(d.runRoot, "physical")
	results := filepath.Join(dir, strings.ReplaceAll(platform, "_", "")+"-results.xml")
	log := filepath.Join(dir, strings.ReplaceAll(platform, "_", "")+"-editor.log")
	executor := d.shared.editor
	executor.Marker = d.marker
	result, err := executor.RunTests(ctx, d.physicalProject, platform, results, log)
	d.physicalLogs = append(d.physicalLogs, log)
	return result, err
}

func (d *integrationDriver) Acquire(ctx context.Context, evidence *Evidence) error {
	started := time.Now()
	helper, err := StartHelper(ctx, d.helperPath, filepath.Join(d.runRoot, "helper"))
	if err != nil {
		return err
	}
	d.helper = helper
	evidence.Metrics.HelperStartupMs = milliseconds(time.Since(started).Milliseconds())
	hello, err := helper.Call(storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationHello, RequestID: "hello-" + d.runID})
	if err != nil || hello.Elevated == nil || !*hello.Elevated {
		primary := fixtureError(CodeUnityRunFailed, "helper-hello", d.helperPath, fmt.Errorf("response=%#v error=%v", hello, err))
		return errors.Join(primary, d.stopHelperWithoutLease())
	}
	request := storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationAcquire, RequestID: "acquire-" + d.runID, StoreRoot: d.shared.storeRoot, WorkspaceRoot: d.vhdxProject, ParentPath: d.shared.parentPath, ChildPath: d.childPath, MountPath: d.mountPath, DeleteChildOnRelease: true}
	acquired, err := helper.Call(request)
	if err != nil || acquired.Lease == nil || acquired.Lease.State != storagehelper.StateReady {
		primary := fixtureError(CodeUnityRunFailed, "helper-acquire", d.childPath, fmt.Errorf("response=%#v error=%v", acquired, err))
		return errors.Join(primary, d.stopHelperWithoutLease())
	}
	d.lease = *acquired.Lease
	d.journalPath = filepath.Join(d.shared.storeRoot, "leases", d.lease.LeaseID+".json")
	duplicate, err := helper.Call(request)
	if err != nil || duplicate.Lease == nil || duplicate.Lease.LeaseID != acquired.Lease.LeaseID {
		primary := fixtureError(CodeUnityRunFailed, "helper-idempotency", d.childPath, fmt.Errorf("response=%#v error=%v", duplicate, err))
		return errors.Join(primary, d.Release(context.Background(), evidence))
	}
	copyAcquireMetrics(evidence, acquired)
	return nil
}

func (d *integrationDriver) CheckMount(ctx context.Context, phase string, evidence *Evidence) error {
	if phase == MountReleased {
		snapshot, err := InspectReleasedMount(d.lease)
		if writeErr := WriteJSON(filepath.Join(d.runRoot, "mount", "released.json"), snapshot); err == nil && writeErr != nil {
			return writeErr
		}
		if err != nil {
			return err
		}
		return d.updateResiduals(ctx, evidence)
	}
	snapshot, err := InspectActiveMount(ctx, d.lease, phase)
	if err != nil {
		return err
	}
	name := strings.ReplaceAll(phase, "-", "_") + ".json"
	if phase == MountReady {
		name = "ready.json"
	}
	if err := WriteJSON(filepath.Join(d.runRoot, "mount", name), snapshot); err != nil {
		return err
	}
	if phase == MountAfterEdit || phase == MountAfterPlay || phase == MountBeforeRelease {
		markerPath := filepath.Join(d.mountPath, "TestPlayVHDX", "marker.txt")
		data, readErr := os.ReadFile(markerPath)
		if readErr != nil || string(data) != d.marker {
			return fixtureError(CodeLibraryMountLost, "verify-child-marker", markerPath, fmt.Errorf("marker=%q error=%v", string(data), readErr))
		}
		d.markerObserved = true
	}
	if phase == MountAfterEdit {
		evidence.Metrics.ChildAfterEditModeLogicalBytes, evidence.Metrics.ChildAfterEditModeAllocatedBytes = FileSizes(d.childPath)
	}
	if phase == MountAfterPlay {
		evidence.Metrics.ChildAfterPlayModeLogicalBytes, evidence.Metrics.ChildAfterPlayModeAllocatedBytes = FileSizes(d.childPath)
	}
	evidence.MountIntegrity = true
	return nil
}

func (d *integrationDriver) RunVHDX(ctx context.Context, platform string) (PlatformResult, error) {
	dir := filepath.Join(d.runRoot, "vhdx")
	results := filepath.Join(dir, strings.ReplaceAll(platform, "_", "")+"-results.xml")
	log := filepath.Join(dir, strings.ReplaceAll(platform, "_", "")+"-editor.log")
	executor := d.shared.editor
	executor.Marker = d.marker
	result, err := executor.RunTests(ctx, d.vhdxProject, platform, results, log)
	d.vhdxLogs = append(d.vhdxLogs, log)
	return result, err
}

func (d *integrationDriver) Release(_ context.Context, evidence *Evidence) error {
	if d.released {
		return nil
	}
	if d.helper == nil {
		return nil
	}
	started := time.Now()
	d.releaseAttempts++
	requestID := fmt.Sprintf("release-%s-%d", d.runID, d.releaseAttempts)
	response, err := d.helper.Call(storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationRelease, RequestID: requestID, LeaseID: d.lease.LeaseID})
	if err != nil || !response.Released {
		return fixtureError(CodeCleanupFailed, "helper-release", d.childPath, fmt.Errorf("response=%#v error=%v", response, err))
	}
	evidence.Metrics.HelperReleaseMs = milliseconds(time.Since(started).Milliseconds())
	copyReleaseMetrics(evidence, response)
	journal, err := readJournal(d.journalPath)
	if err != nil || journal.State != storagehelper.StateReleased {
		return fixtureError(CodeCleanupFailed, "verify-journal", d.journalPath, fmt.Errorf("state=%s error=%v", journal.State, err))
	}
	_, err = d.helper.Call(storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationShutdown, RequestID: "shutdown-" + d.runID})
	if err != nil {
		return err
	}
	_ = d.helper.CloseInput()
	if err := d.helper.Wait(); err != nil {
		return err
	}
	d.released = true
	evidence.Reimport = ObserveReimport(append(d.physicalLogs, d.vhdxLogs...)...)
	return nil
}

func (d *integrationDriver) stopHelperWithoutLease() error {
	if d.helper == nil {
		return nil
	}
	_, callErr := d.helper.Call(storagehelper.Request{SchemaVersion: storagehelper.SchemaVersion, Operation: storagehelper.OperationShutdown, RequestID: "abort-" + d.runID})
	_ = d.helper.CloseInput()
	waitErr := d.helper.Wait()
	return errors.Join(callErr, waitErr)
}

func (d *integrationDriver) VerifyParent(ctx context.Context, evidence *Evidence) error {
	hash, err := HashFile(d.shared.parentPath)
	if err != nil {
		return err
	}
	inspectMount := filepath.Join(d.shared.root, "parent-inspect-"+d.runID)
	exists, err := InspectParentFile(ctx, d.shared.parentPath, inspectMount, filepath.Join("TestPlayVHDX", "marker.txt"))
	if err != nil {
		return err
	}
	hashAfterInspect, err := HashFile(d.shared.parentPath)
	if err != nil {
		return err
	}
	evidence.ParentHashAfter = hashAfterInspect
	physicalMarker := filepath.Join(d.physicalProject, "Library", "TestPlayVHDX", "marker.txt")
	physicalData, physicalErr := os.ReadFile(physicalMarker)
	if hash != evidence.ParentHashBefore || hashAfterInspect != evidence.ParentHashBefore || exists || !d.markerObserved || physicalErr != nil || string(physicalData) != d.marker {
		return fixtureError(CodeParentIsolationFailed, "verify-parent", d.shared.parentPath, fmt.Errorf("before=%s after=%s afterInspect=%s parentMarker=%t childMarker=%t physicalMarker=%q physicalError=%v", evidence.ParentHashBefore, hash, hashAfterInspect, exists, d.markerObserved, string(physicalData), physicalErr))
	}
	evidence.ParentIsolation = true
	return nil
}

func (d *integrationDriver) updateResiduals(ctx context.Context, evidence *Evidence) error {
	afterDisks, err := FileBackedDisks(ctx)
	if err != nil {
		return err
	}
	if !sameDisks(d.shared.beforeDisks, afterDisks) {
		evidence.ResidualDiskCount = diskDifferenceCount(d.shared.beforeDisks, afterDisks)
	}
	if _, err := os.Stat(d.mountPath); err == nil {
		evidence.ResidualMountCount = 1
	}
	if _, err := os.Stat(d.childPath); err == nil {
		evidence.ResidualChildCount = 1
	}
	evidence.ResidualJournalCount = nonReleasedJournalCount(d.shared.storeRoot)
	evidence.CleanupPassed = evidence.ResidualDiskCount == 0 && evidence.ResidualMountCount == 0 && evidence.ResidualChildCount == 0 && evidence.ResidualJournalCount == 0 && evidence.ResidualUnityProcesses == 0
	if !evidence.CleanupPassed {
		return fixtureError(CodeCleanupFailed, "verify-residuals", d.shared.root, fmt.Errorf("disk=%d mount=%d child=%d journal=%d", evidence.ResidualDiskCount, evidence.ResidualMountCount, evidence.ResidualChildCount, evidence.ResidualJournalCount))
	}
	return nil
}

func copyAcquireMetrics(evidence *Evidence, response storagehelper.Response) {
	if response.Metrics == nil {
		return
	}
	metrics := response.Metrics
	evidence.Metrics.HelperAcquireMs = metrics.AcquireWallClockMs
	evidence.Metrics.ChildCreateMs = metrics.ChildCreateMs
	evidence.Metrics.ChildAttachMs = metrics.AttachCallMs
	evidence.Metrics.VolumeReadyWaitMs = metrics.VolumeReadyWaitMs
	evidence.Metrics.MountVisibilityWaitMs = metrics.MountVisibilityWaitMs
	evidence.Metrics.ChildReadyLogicalBytes = metrics.ChildReadyLogicalBytes
	evidence.Metrics.ChildReadyAllocatedBytes = metrics.ChildReadyAllocatedBytes
}

func copyReleaseMetrics(evidence *Evidence, response storagehelper.Response) {
	if response.Metrics == nil {
		return
	}
	evidence.Metrics.DetachVisibilityWaitMs = response.Metrics.DetachVisibilityWaitMs
	evidence.Metrics.CleanupMs = response.Metrics.CleanupMs
}

func requiredIntegrationPath(t *testing.T, name string, mustBeEmpty bool) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Skipf("%s is not set", name)
	}
	if !filepath.IsAbs(value) {
		t.Fatalf("%s must be absolute", name)
	}
	value = filepath.Clean(value)
	if value == filepath.VolumeName(value)+string(os.PathSeparator) {
		t.Fatalf("%s must not be a drive root", name)
	}
	if mustBeEmpty {
		if info, err := os.Lstat(value); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("%s must be a real directory", name)
			}
			entries, err := os.ReadDir(value)
			if err != nil || len(entries) != 0 {
				t.Fatalf("%s must be empty: entries=%d error=%v", name, len(entries), err)
			}
		} else if os.IsNotExist(err) {
			if err := os.Mkdir(value, 0700); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Fatal(err)
		}
	} else if name == artifactRootEnv {
		if err := os.MkdirAll(value, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if name == fixtureRootEnv || name == artifactRootEnv {
		resolved, err := filepath.EvalSymlinks(value)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(filepath.Clean(value), filepath.Clean(resolved)) {
			t.Fatalf("%s must not be a symlink or reparse path: %s", name, value)
		}
	}
	return value
}

func pathsOverlap(left, right string) bool {
	contains := func(root, candidate string) bool {
		relative, err := filepath.Rel(root, candidate)
		if err != nil {
			return false
		}
		return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
	}
	return contains(left, right) || contains(right, left)
}

func integrationCount(t *testing.T) int {
	t.Helper()
	value := 1
	if raw := os.Getenv(countEnv); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
		value = parsed
	}
	if value != 1 && value != 5 {
		t.Fatal("integration count must be 1 or 5")
	}
	return value
}

func parentSizeBytes(t *testing.T) int64 {
	t.Helper()
	gib := int64(4)
	if raw := os.Getenv(parentSizeEnv); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 || value > 64 {
			t.Fatalf("%s must be 1..64", parentSizeEnv)
		}
		gib = value
	}
	return gib << 30
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

func nonReleasedJournalCount(storeRoot string) int {
	entries, err := os.ReadDir(filepath.Join(storeRoot, "leases"))
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		journal, err := readJournal(filepath.Join(storeRoot, "leases", entry.Name()))
		if err != nil || journal.State != storagehelper.StateReleased {
			count++
		}
	}
	return count
}

func sameDisks(left, right []DiskSnapshot) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func diskDifferenceCount(before, after []DiskSnapshot) int {
	known := map[int]bool{}
	for _, disk := range before {
		known[disk.Number] = true
	}
	count := 0
	for _, disk := range after {
		if !known[disk.Number] {
			count++
		}
	}
	return count
}

func removeRunProjects(root string, paths ...string) error {
	for _, path := range paths {
		if filepath.Dir(path) != root || (!strings.HasPrefix(filepath.Base(path), "physical-project-") && !strings.HasPrefix(filepath.Base(path), "vhdx-project-")) {
			return fmt.Errorf("unsafe run project cleanup target: %s", path)
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func safeRemoveFixtureRoot(root string) error {
	if filepath.Base(root) != "testplay-unity-vhdx-fixture" {
		return fmt.Errorf("unsafe fixture cleanup target: %s", root)
	}
	return os.RemoveAll(root)
}
