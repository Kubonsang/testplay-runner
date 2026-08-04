package refsworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakePoolNative struct {
	platform          string
	elevated          bool
	ensureErr         error
	createErr         error
	mountErr          error
	removeErr         error
	closeErr          error
	volume            VolumeInfo
	closeCount        int
	createCount       int
	removeCount       int
	mountInitializes  []bool
	hostFree          int64
	hostFreeErr       error
	hostFilesystem    string
	emptyMountOnClose bool
	waitReady         func(context.Context, Paths, PoolMetadata, VolumeInfo) (time.Duration, error)
	events            *[]string
}

type fakeMountedPool struct {
	native *fakePoolNative
	volume VolumeInfo
	mount  string
}

func (pool *fakeMountedPool) Volume() VolumeInfo { return pool.volume }
func (pool *fakeMountedPool) Metrics() NativeMountMetrics {
	return NativeMountMetrics{AttachMs: 2, MountMs: 3}
}
func (pool *fakeMountedPool) DevDriveEvidence() DevDriveEvidence {
	initialized := len(pool.native.mountInitializes) != 0 && pool.native.mountInitializes[len(pool.native.mountInitializes)-1]
	return DevDriveEvidence{
		FormatAttempted: initialized, FormatSucceeded: initialized, QueryExitCode: 0,
		QueryOutput:                  "Developer volumes are enabled.",
		TemporaryDriveLetterAssigned: initialized, TemporaryDriveLetterRemoved: initialized,
		PrivateMountVerified: true,
	}
}
func (pool *fakeMountedPool) WaitReady(ctx context.Context, paths Paths, expected PoolMetadata) (time.Duration, error) {
	if pool.native.events != nil {
		*pool.native.events = append(*pool.native.events, "readiness-wait")
	}
	if pool.native.waitReady != nil {
		return pool.native.waitReady(ctx, paths, expected, pool.volume)
	}
	return waitForMountedPoolReady(ctx, paths, expected, pool.volume, fakeMountedPoolReadinessInspector{mount: paths.Mount}, mountedPoolReadinessOptions{Timeout: 250 * time.Millisecond, PollInterval: time.Millisecond})
}
func (pool *fakeMountedPool) Close(context.Context) error {
	pool.native.closeCount++
	if pool.native.emptyMountOnClose {
		entries, err := os.ReadDir(pool.mount)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := os.RemoveAll(filepath.Join(pool.mount, entry.Name())); err != nil {
				return err
			}
		}
	}
	return pool.native.closeErr
}

func (native *fakePoolNative) Platform() string                         { return native.platform }
func (native *fakePoolNative) EnsureAvailable(context.Context) error    { return native.ensureErr }
func (native *fakePoolNative) IsElevated(context.Context) (bool, error) { return native.elevated, nil }
func (native *fakePoolNative) CreateDynamic(path string, _ int64) error {
	if native.createErr != nil {
		return native.createErr
	}
	native.createCount++
	return os.WriteFile(path, []byte("dynamic-vhdx"), 0600)
}
func (native *fakePoolNative) Mount(_ context.Context, vhdx, mount string, initialize bool) (MountedPool, error) {
	if native.mountErr != nil {
		return nil, native.mountErr
	}
	native.mountInitializes = append(native.mountInitializes, initialize)
	if native.events != nil {
		*native.events = append(*native.events, "mount-called", "mount-returned")
	}
	if _, err := os.Stat(vhdx); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(mount, 0700); err != nil {
		return nil, err
	}
	return &fakeMountedPool{native: native, volume: native.volume, mount: mount}, nil
}

type fakeMountedPoolReadinessInspector struct{ mount string }

func (inspector fakeMountedPoolReadinessInspector) Lstat(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}
func (inspector fakeMountedPoolReadinessInspector) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
func (inspector fakeMountedPoolReadinessInspector) IsReparsePoint(path string) (bool, error) {
	return strings.EqualFold(filepath.Clean(path), filepath.Clean(inspector.mount)), nil
}
func (native *fakePoolNative) FileIdentity(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return "fake-volume:fake-file", nil
}
func (native *fakePoolNative) FileUsage(path string) (FileUsage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileUsage{}, err
	}
	return FileUsage{LogicalBytes: info.Size(), AllocatedBytes: info.Size()}, nil
}
func (native *fakePoolNative) HostFreeBytes(string) (int64, error) {
	if native.hostFreeErr != nil {
		return 0, native.hostFreeErr
	}
	return native.hostFree, nil
}
func (native *fakePoolNative) HostFilesystem(string) (string, error) {
	return native.hostFilesystem, nil
}
func (native *fakePoolNative) RemoveVHDX(path string) error {
	if native.removeErr != nil {
		return native.removeErr
	}
	native.removeCount++
	return os.Remove(path)
}

func newFakePoolNative() *fakePoolNative {
	return &fakePoolNative{
		platform:       "windows",
		elevated:       true,
		hostFree:       128 << 30,
		hostFilesystem: "NTFS",
		volume: VolumeInfo{
			VolumeGUIDPath:       `\\?\Volume{test}\`,
			Filesystem:           "ReFS",
			ClusterSize:          4096,
			TotalBytes:           64 << 30,
			FreeBytes:            63 << 30,
			UsedBytes:            1 << 30,
			SupportsBlockCloning: true,
		},
	}
}

func TestPoolSetupEnforcesHostFreeFloorBeforeWrites(t *testing.T) {
	native := newFakePoolNative()
	required := DefaultMinimumHostFreeBytes + DefaultMaximumBytes + DefaultVHDXOverheadReserveBytes
	native.hostFree = required - 1
	root := filepath.Join(t.TempDir(), "storage")
	if _, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: root}); ErrorCode(err) != CodeHostFreeSpaceFloor {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("insufficient setup wrote root: %v", err)
	}
	native.hostFree = required
	if _, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: root}); err != nil {
		t.Fatalf("exact floor failed: %v", err)
	}
}

func TestPoolSetupAcceptsHostFreeAboveFullMaximumReservation(t *testing.T) {
	native := newFakePoolNative()
	native.hostFree = DefaultMinimumHostFreeBytes + DefaultMaximumBytes + DefaultVHDXOverheadReserveBytes + 1
	if _, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: filepath.Join(t.TempDir(), "storage")}); err != nil {
		t.Fatal(err)
	}
}

func TestPoolSetupCreatesCompletelyMissingDefaultParents(t *testing.T) {
	native := newFakePoolNative()
	root := filepath.Join(t.TempDir(), "TestPlay", "Storage")
	result, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "PASS" {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{filepath.Dir(result.Paths.Root), result.Paths.Root} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unsafe fresh-install path %q: %v", path, err)
		}
	}
}

func TestPoolSetupRejectsHostFloorIntegerOverflow(t *testing.T) {
	native := newFakePoolNative()
	maximum := int64(math.MaxInt64 / 512 * 512)
	_, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{
		Root: filepath.Join(t.TempDir(), "storage"), MaximumBytes: maximum,
		SoftBudgetBytes: 8 << 30, WorkerReserveBytes: 1 << 30,
		MinimumHostFreeBytes: 1 << 30, VHDXOverheadReserveBytes: 1,
	})
	if ErrorCode(err) != CodeHostFreeSpaceFloor {
		t.Fatalf("err=%v", err)
	}
}

func TestPoolSetupHostFreeMeasurementFailure(t *testing.T) {
	native := newFakePoolNative()
	native.hostFreeErr = errors.New("measurement failed")
	root := filepath.Join(t.TempDir(), "storage")
	if _, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: root}); ErrorCode(err) != CodeHostFreeSpaceFloor {
		t.Fatalf("err=%v", err)
	}
}

func TestPoolSetupAndStatusStructuredEvidence(t *testing.T) {
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	root := filepath.Join(t.TempDir(), "storage")
	config := Config{Root: root}
	result, err := service.Setup(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "PASS" || result.Architecture != "Managed ReFS Library Pool" || result.WindowsProvider != WindowsProviderDevDriveVHDX || result.VolumeKind != VolumeKindDevDrive || result.ReleasedVersionModified || result.PhysicalImageCreated || result.DifferencingChildCreated || result.FallbackUsed || !result.BlockCloneSupported || !result.SourceUnchanged {
		t.Fatalf("result=%+v", result)
	}
	if !result.DevDrive.FormatAttempted || !result.DevDrive.FormatSucceeded || !result.DevDrive.TemporaryDriveLetterAssigned || !result.DevDrive.TemporaryDriveLetterRemoved || !result.DevDrive.PrivateMountVerified || result.DevDrive.QueryExitCode != 0 {
		t.Fatalf("devDrive=%+v", result.DevDrive)
	}
	if result.Metrics.ClonedBytes <= 8192 || result.Metrics.TailCopiedBytes < 137 {
		t.Fatalf("metrics=%+v", result.Metrics)
	}
	status, err := service.Status(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "READY" || status.Pool == nil || status.Pool.OwnershipToken == "" || status.Volume.Filesystem != "ReFS" {
		t.Fatalf("status=%+v", status)
	}
	if status.DevDrive.FormatAttempted || !status.DevDrive.PrivateMountVerified || status.DevDrive.QueryExitCode != 0 {
		t.Fatalf("status devDrive=%+v", status.DevDrive)
	}
	probe, err := service.Probe(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Metrics.PoolAttachMs != 2 || probe.Metrics.PoolMountMs != 3 || probe.Metrics.ClonedBytes <= 8192 {
		t.Fatalf("probe metrics=%+v", probe.Metrics)
	}
}

func TestPersistentOperationsUseMountedPoolReadinessOrdering(t *testing.T) {
	for _, operation := range []string{"status", "probe", "remove"} {
		t.Run(operation, func(t *testing.T) {
			native := newFakePoolNative()
			service := NewService(native, copyClaimingCloner{})
			config := Config{Root: filepath.Join(t.TempDir(), "storage")}
			setup, err := service.Setup(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			var events []string
			native.events = &events
			readMetadata := service.readMetadata
			service.readMetadata = func(path string) (PoolMetadata, error) {
				if strings.EqualFold(filepath.Clean(path), filepath.Clean(setup.Paths.Owner)) {
					events = append(events, "host-owner-read")
				} else if strings.EqualFold(filepath.Clean(path), filepath.Clean(setup.Paths.PoolFile)) {
					events = append(events, "pool-metadata-read")
				}
				return readMetadata(path)
			}
			compareIdentity := service.compareIdentity
			service.compareIdentity = func(paths Paths, host, pool PoolMetadata, volume VolumeInfo) error {
				events = append(events, "identity-comparison")
				return compareIdentity(paths, host, pool, volume)
			}
			if operation == "remove" {
				native.emptyMountOnClose = true
			}
			switch operation {
			case "status":
				_, err = service.Status(context.Background(), config)
			case "probe":
				_, err = service.Probe(context.Background(), config)
			case "remove":
				_, err = service.Remove(context.Background(), config)
			}
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"host-owner-read", "mount-called", "mount-returned", "readiness-wait", "pool-metadata-read", "identity-comparison"}
			if strings.Join(events[:min(len(events), len(want))], "|") != strings.Join(want, "|") {
				t.Fatalf("events=%v want prefix=%v", events, want)
			}
		})
	}
}

func TestPoolSetupRejectsNonNTFSHostBeforeWrites(t *testing.T) {
	native := newFakePoolNative()
	native.hostFilesystem = "exFAT"
	root := filepath.Join(t.TempDir(), "storage")
	if _, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: root}); ErrorCode(err) != CodeDevDriveUnavailable {
		t.Fatalf("err=%v", err)
	}
	if native.createCount != 0 {
		t.Fatalf("non-NTFS host created %d VHDX files", native.createCount)
	}
}

func TestPoolSetupPersistsVHDXAndStatusDoesNotReformat(t *testing.T) {
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	config := Config{Root: filepath.Join(t.TempDir(), "storage")}
	setup, err := service.Setup(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(setup.Paths.VHDX); err != nil {
		t.Fatalf("setup did not preserve VHDX: %v", err)
	}
	if native.createCount != 1 || native.removeCount != 0 || len(native.mountInitializes) != 1 || !native.mountInitializes[0] {
		t.Fatalf("after setup create=%d remove=%d mounts=%v", native.createCount, native.removeCount, native.mountInitializes)
	}
	if _, err := service.Status(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if native.createCount != 1 || native.removeCount != 0 || len(native.mountInitializes) != 2 || native.mountInitializes[1] {
		t.Fatalf("status recreated, reformatted, or removed the pool: create=%d remove=%d mounts=%v", native.createCount, native.removeCount, native.mountInitializes)
	}
}

type postMountFailureCloner struct {
	request CloneRequest
}

func (cloner *postMountFailureCloner) CloneTree(_ context.Context, request CloneRequest) (CloneMetrics, error) {
	cloner.request = request
	return CloneMetrics{}, newError(CodeCloneFailed, "validate-clone-source", request.Source, errors.New("injected post-mount path validation failure"))
}

func TestPoolPostMountCloneFailurePreservesNativeEvidence(t *testing.T) {
	native := newFakePoolNative()
	cloner := &postMountFailureCloner{}
	config := Config{Root: filepath.Join(t.TempDir(), "storage")}
	_, err := NewService(native, cloner).Setup(context.Background(), config)
	var cloneErr *Error
	if !errors.As(err, &cloneErr) || cloneErr.Code != CodeCloneFailed {
		t.Fatalf("err=%v", err)
	}
	if cloneErr.CleanupState != "released" || cloneErr.ManualRecoveryRequired || cloneErr.NativeEvidence == nil {
		t.Fatalf("error=%+v", cloneErr)
	}
	evidence := cloneErr.NativeEvidence
	if evidence.DevDrive == nil || !evidence.DevDrive.FormatAttempted || !evidence.DevDrive.FormatSucceeded || evidence.DevDrive.QueryExitCode != 0 || evidence.DevDrive.QueryOutput != "Developer volumes are enabled." || !evidence.DevDrive.TemporaryDriveLetterAssigned || !evidence.DevDrive.TemporaryDriveLetterRemoved || !evidence.DevDrive.PrivateMountVerified {
		t.Fatalf("devDrive=%+v", evidence.DevDrive)
	}
	if evidence.Filesystem == nil || *evidence.Filesystem != "ReFS" || evidence.ClusterSize == nil || *evidence.ClusterSize != 4096 || evidence.BlockCloneSupported == nil || !*evidence.BlockCloneSupported {
		t.Fatalf("volume evidence=%+v", evidence)
	}
	if evidence.LastCompletedMilestone != "volume-capability-validation" || evidence.RegularBlockCloneIOCTLAttempted == nil || *evidence.RegularBlockCloneIOCTLAttempted || evidence.SparseBlockCloneIOCTLAttempted == nil || *evidence.SparseBlockCloneIOCTLAttempted {
		t.Fatalf("attempt evidence=%+v", evidence)
	}
	if evidence.Milestones.RegularBlockCloneIOCTL != NativeMilestoneNotAttempted || evidence.Milestones.SparseBlockCloneIOCTL != NativeMilestoneNotAttempted || evidence.Milestones.Cleanup != NativeMilestoneReleased {
		t.Fatalf("milestones=%+v", evidence.Milestones)
	}
	_, paths, pathsErr := NewPaths(config)
	if pathsErr != nil {
		t.Fatal(pathsErr)
	}
	if cloner.request.TrustedRoot != paths.PoolRoot || !PathWithin(cloner.request.TrustedRoot, cloner.request.Source) || !PathWithin(cloner.request.TrustedRoot, cloner.request.Destination) {
		t.Fatalf("production clone request=%+v paths=%+v", cloner.request, paths)
	}
	if _, statErr := os.Stat(paths.VHDX); !os.IsNotExist(statErr) {
		t.Fatalf("released failure retained partial VHDX: %v", statErr)
	}
}

func TestPoolPreMountFailureDoesNotInventNativeEvidence(t *testing.T) {
	native := newFakePoolNative()
	native.mountErr = errors.New("AttachVirtualDisk failed")
	_, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: filepath.Join(t.TempDir(), "storage")})
	var probeErr *Error
	if !errors.As(err, &probeErr) {
		t.Fatal(err)
	}
	if probeErr.NativeEvidence != nil {
		t.Fatalf("pre-mount evidence was invented: %+v", probeErr.NativeEvidence)
	}
}

func TestProbeReadinessTimeoutDetachesAndPreservesPersistentPoolEvidence(t *testing.T) {
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	config := Config{Root: filepath.Join(t.TempDir(), "storage")}
	setup, err := service.Setup(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	native.waitReady = func(_ context.Context, paths Paths, _ PoolMetadata, _ VolumeInfo) (time.Duration, error) {
		return 20 * time.Second, newMountedPoolNotReadyError(paths, 20*time.Second, os.ErrNotExist)
	}
	closeBefore := native.closeCount
	_, err = service.Probe(context.Background(), config)
	var probeErr *Error
	if !errors.As(err, &probeErr) || probeErr.Code != CodePoolMountNotReady {
		t.Fatalf("err=%v", err)
	}
	if probeErr.CleanupState != "preserved" || !probeErr.OwnerMetadataCommitted || probeErr.OwnedVHDXPath != setup.Paths.VHDX || probeErr.ManualRecoveryRequired {
		t.Fatalf("evidence=%+v", probeErr)
	}
	if native.closeCount != closeBefore+1 {
		t.Fatalf("closeCount=%d before=%d", native.closeCount, closeBefore)
	}
	if _, statErr := os.Stat(setup.Paths.VHDX); statErr != nil {
		t.Fatalf("persistent VHDX was not preserved: %v", statErr)
	}
	if probeErr.NativeEvidence == nil || probeErr.NativeEvidence.Milestones.MountedPoolReadiness != NativeMilestoneMeasuredFail || probeErr.NativeEvidence.Milestones.Cleanup != NativeMilestoneReleased {
		t.Fatalf("nativeEvidence=%+v", probeErr.NativeEvidence)
	}
}

func TestProbeReadinessTimeoutDetachFailureRequiresManualRecovery(t *testing.T) {
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	config := Config{Root: filepath.Join(t.TempDir(), "storage")}
	if _, err := service.Setup(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	native.waitReady = func(_ context.Context, paths Paths, _ PoolMetadata, _ VolumeInfo) (time.Duration, error) {
		return 20 * time.Second, newMountedPoolNotReadyError(paths, 20*time.Second, os.ErrNotExist)
	}
	native.closeErr = errors.New("detach visibility timeout")
	_, err := service.Probe(context.Background(), config)
	var probeErr *Error
	if ErrorCode(err) != CodeCleanupFailed || !errors.As(err, &probeErr) || probeErr.CleanupState != "uncertain" || !probeErr.OwnerMetadataCommitted || !probeErr.ManualRecoveryRequired {
		t.Fatalf("err=%v evidence=%+v", err, probeErr)
	}
}

func TestPoolRemoveIsTheOnlyLifecycleThatDeletesPersistentVHDX(t *testing.T) {
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	config := Config{Root: filepath.Join(t.TempDir(), "storage")}
	setup, err := service.Setup(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Probe(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if native.removeCount != 0 {
		t.Fatalf("setup/probe deleted the persistent VHDX %d times", native.removeCount)
	}
	native.emptyMountOnClose = true
	removed, err := service.Remove(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if native.removeCount != 1 {
		t.Fatalf("remove count=%d, want 1", native.removeCount)
	}
	if _, err := os.Stat(setup.Paths.VHDX); !os.IsNotExist(err) {
		t.Fatalf("explicit remove left VHDX: %v", err)
	}
	if removed.Residual.OwnedVHDXFiles.Measured != true || removed.Residual.OwnedVHDXFiles.Count != 0 {
		t.Fatalf("remove residual=%+v", removed.Residual.OwnedVHDXFiles)
	}
}

func TestPoolSetupNativeSeamsReturnStableCodes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakePoolNative)
		cloner TreeCloner
		want   string
	}{
		{name: "not elevated", mutate: func(native *fakePoolNative) { native.elevated = false }, cloner: copyClaimingCloner{}, want: CodeNotElevated},
		{name: "create failure", mutate: func(native *fakePoolNative) { native.createErr = errors.New("CreateVirtualDisk failed") }, cloner: copyClaimingCloner{}, want: CodePoolCorrupt},
		{name: "attach failure", mutate: func(native *fakePoolNative) { native.mountErr = errors.New("AttachVirtualDisk failed") }, cloner: copyClaimingCloner{}, want: CodePoolCorrupt},
		{name: "format failure", mutate: func(native *fakePoolNative) { native.mountErr = errors.New("dev-drive-format-failed") }, cloner: copyClaimingCloner{}, want: CodeDevDriveFormatFailed},
		{name: "block clone unsupported", mutate: func(*fakePoolNative) {}, cloner: copyClaimingCloner{fail: newError(CodeBlockCloneUnavailable, "FSCTL_DUPLICATE_EXTENTS_TO_FILE", "", nil)}, want: CodeBlockCloneUnavailable},
		{name: "filesystem mismatch", mutate: func(native *fakePoolNative) { native.volume.Filesystem = "NTFS" }, cloner: copyClaimingCloner{}, want: CodePoolCorrupt},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			native := newFakePoolNative()
			test.mutate(native)
			service := NewService(native, test.cloner)
			_, err := service.Setup(context.Background(), Config{Root: filepath.Join(t.TempDir(), "storage")})
			if code := ErrorCode(err); code != test.want {
				t.Fatalf("code=%s want=%s err=%v", code, test.want, err)
			}
		})
	}
}

func TestPoolSetupReportsSafePartialCleanupEvidence(t *testing.T) {
	native := newFakePoolNative()
	native.createErr = errors.New("create failed")
	root := filepath.Join(t.TempDir(), "storage")
	_, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: root})
	var probeErr *Error
	if !errors.As(err, &probeErr) || probeErr.CleanupState != "released" || probeErr.OwnerMetadataCommitted || probeErr.ManualRecoveryRequired || probeErr.OwnedVHDXPath == "" {
		t.Fatalf("err=%v evidence=%+v", err, probeErr)
	}
}

func TestPoolSetupPreservesOwnedVHDXWhenDetachIsUncertain(t *testing.T) {
	native := newFakePoolNative()
	native.closeErr = errors.New("detach visibility timeout")
	root := filepath.Join(t.TempDir(), "storage")
	_, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: root})
	if ErrorCode(err) != CodeCleanupFailed {
		t.Fatalf("err=%v", err)
	}
	var probeErr *Error
	if !errors.As(err, &probeErr) || probeErr.CleanupState != "uncertain" || !probeErr.OwnerMetadataCommitted || !probeErr.ManualRecoveryRequired || probeErr.OwnedVHDXPath == "" {
		t.Fatalf("cleanup evidence=%+v", probeErr)
	}
	_, paths, pathsErr := NewPaths(Config{Root: root})
	if pathsErr != nil {
		t.Fatal(pathsErr)
	}
	if _, statErr := os.Stat(paths.VHDX); statErr != nil {
		t.Fatalf("uncertain cleanup deleted VHDX: %v", statErr)
	}
	if _, statErr := os.Stat(paths.Owner); statErr != nil {
		t.Fatalf("uncertain cleanup deleted ownership evidence: %v", statErr)
	}
}

func TestPoolInspectionJoinsPrimaryAndCleanupErrors(t *testing.T) {
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	root := filepath.Join(t.TempDir(), "storage")
	config := Config{Root: root}
	if _, err := service.Setup(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	_, paths, err := NewPaths(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PoolFile, []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	native.closeErr = errors.New("detach visibility timeout")
	_, err = service.Status(context.Background(), config)
	var probeErr *Error
	if ErrorCode(err) != CodeCleanupFailed || !errors.As(err, &probeErr) || probeErr.CleanupState != "uncertain" || !probeErr.ManualRecoveryRequired {
		t.Fatalf("err=%v evidence=%+v", err, probeErr)
	}
	if !strings.Contains(err.Error(), "invalid character") || !strings.Contains(err.Error(), "detach visibility timeout") {
		t.Fatalf("joined error lost evidence: %v", err)
	}
}

func TestPoolSyntheticCleanupFailureIsReportedAndEvidencePreserved(t *testing.T) {
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	service.removeAll = func(string) error { return errors.New("injected cleanup failure") }
	root := filepath.Join(t.TempDir(), "storage")
	_, err := service.Setup(context.Background(), Config{Root: root})
	if ErrorCode(err) != CodeCleanupFailed {
		t.Fatalf("err=%v", err)
	}
	_, paths, pathsErr := NewPaths(Config{Root: root})
	if pathsErr != nil {
		t.Fatal(pathsErr)
	}
	if count, countErr := countEntries(paths.PoolRoot, ".block-clone-probe-", ""); countErr != nil || count != 1 {
		t.Fatalf("probe evidence count=%d err=%v", count, countErr)
	}
}

func TestResidualJSONDistinguishesUnmeasuredFromZero(t *testing.T) {
	data, err := json.Marshal(Residual{ActiveBaselineUses: ResidualMetric{Measured: true, Count: 0}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded Residual
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.ActiveBaselineUses.Measured || decoded.WorkerDirectories.Measured {
		t.Fatalf("residual=%s", data)
	}
}

func TestPoolSetupRejectsUnsupportedPlatformBeforeWrites(t *testing.T) {
	native := newFakePoolNative()
	native.platform = runtime.GOOS
	if native.platform == "windows" {
		native.platform = "linux"
	}
	root := filepath.Join(t.TempDir(), "storage")
	_, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: root})
	if ErrorCode(err) != CodeUnsupportedPlatform {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("unsupported setup wrote root: %v", statErr)
	}
}
