package refsworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakePoolNative struct {
	platform    string
	elevated    bool
	ensureErr   error
	createErr   error
	mountErr    error
	removeErr   error
	closeErr    error
	volume      VolumeInfo
	closeCount  int
	hostFree    int64
	hostFreeErr error
}

type fakeMountedPool struct {
	native *fakePoolNative
	volume VolumeInfo
}

func (pool *fakeMountedPool) Volume() VolumeInfo { return pool.volume }
func (pool *fakeMountedPool) Metrics() NativeMountMetrics {
	return NativeMountMetrics{AttachMs: 2, MountMs: 3}
}
func (pool *fakeMountedPool) Close(context.Context) error {
	pool.native.closeCount++
	return pool.native.closeErr
}

func (native *fakePoolNative) Platform() string                         { return native.platform }
func (native *fakePoolNative) EnsureAvailable() error                   { return native.ensureErr }
func (native *fakePoolNative) IsElevated(context.Context) (bool, error) { return native.elevated, nil }
func (native *fakePoolNative) CreateDynamic(path string, _ int64) error {
	if native.createErr != nil {
		return native.createErr
	}
	return os.WriteFile(path, []byte("dynamic-vhdx"), 0600)
}
func (native *fakePoolNative) Mount(_ context.Context, vhdx, mount string, _ bool) (MountedPool, error) {
	if native.mountErr != nil {
		return nil, native.mountErr
	}
	if _, err := os.Stat(vhdx); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(mount, 0700); err != nil {
		return nil, err
	}
	return &fakeMountedPool{native: native, volume: native.volume}, nil
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
func (native *fakePoolNative) RemoveVHDX(path string) error {
	if native.removeErr != nil {
		return native.removeErr
	}
	return os.Remove(path)
}

func newFakePoolNative() *fakePoolNative {
	return &fakePoolNative{
		platform: "windows",
		elevated: true,
		hostFree: 100 << 30,
		volume: VolumeInfo{
			VolumeGUIDPath:       `\\?\Volume{test}\`,
			Filesystem:           "ReFS",
			ClusterSize:          4096,
			TotalBytes:           16 << 30,
			FreeBytes:            15 << 30,
			UsedBytes:            1 << 30,
			SupportsBlockCloning: true,
		},
	}
}

func TestPoolSetupEnforcesHostFreeFloorBeforeWrites(t *testing.T) {
	native := newFakePoolNative()
	native.hostFree = DefaultMinimumHostFreeBytes + DefaultVHDXOverheadReserveBytes + DefaultInitialPoolAllocationBytes - 1
	root := filepath.Join(t.TempDir(), "storage")
	if _, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: root}); ErrorCode(err) != CodeHostFreeSpaceFloor {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("insufficient setup wrote root: %v", err)
	}
	native.hostFree = DefaultMinimumHostFreeBytes + DefaultVHDXOverheadReserveBytes + DefaultInitialPoolAllocationBytes
	if _, err := NewService(native, copyClaimingCloner{}).Setup(context.Background(), Config{Root: root}); err != nil {
		t.Fatalf("exact floor failed: %v", err)
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
	if result.Status != "PASS" || result.Architecture != "Managed ReFS Library Pool" || result.ReleasedVersionModified || result.PhysicalImageCreated || result.DifferencingChildCreated || result.FallbackUsed || !result.BlockCloneSupported || !result.SourceUnchanged {
		t.Fatalf("result=%+v", result)
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
	probe, err := service.Probe(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Metrics.PoolAttachMs != 2 || probe.Metrics.PoolMountMs != 3 || probe.Metrics.ClonedBytes <= 8192 {
		t.Fatalf("probe metrics=%+v", probe.Metrics)
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
		{name: "format failure", mutate: func(native *fakePoolNative) { native.mountErr = errors.New("ReFS format unavailable") }, cloner: copyClaimingCloner{}, want: CodeReFSFormatUnavailable},
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
