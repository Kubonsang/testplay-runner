package refsworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type MountedPool interface {
	Volume() VolumeInfo
	Metrics() NativeMountMetrics
	Close(context.Context) error
}

type NativeMountMetrics struct {
	AttachMs int64 `json:"attachMs"`
	MountMs  int64 `json:"mountMs"`
}

type PoolNative interface {
	Platform() string
	EnsureAvailable() error
	IsElevated(context.Context) (bool, error)
	CreateDynamic(path string, maximumBytes int64) error
	Mount(context.Context, string, string, bool) (MountedPool, error)
	FileIdentity(path string) (string, error)
	FileUsage(path string) (FileUsage, error)
	HostFreeBytes(path string) (int64, error)
	RemoveVHDX(path string) error
}

type Service struct {
	native    PoolNative
	cloner    TreeCloner
	now       func() time.Time
	removeAll func(string) error
}

func NewService(native PoolNative, cloner TreeCloner) *Service {
	return &Service{native: native, cloner: cloner, now: time.Now, removeAll: os.RemoveAll}
}

func NewNativeService() *Service {
	return NewService(newPoolNative(), NewNativeTreeCloner())
}

func (service *Service) Setup(ctx context.Context, config Config) (returnResult *Result, returnErr error) {
	started := time.Now()
	config, paths, err := NewPaths(config)
	if err != nil {
		return nil, err
	}
	if err := validateOwnedPaths(paths); err != nil {
		return nil, err
	}
	if err := service.checkNative(ctx); err != nil {
		return nil, err
	}
	hostMeasurementPath, _, err := nearestExistingAncestor(paths.Root)
	if err != nil {
		return nil, newError(CodeHostFreeSpaceFloor, "find-host-volume-before-setup", paths.Root, err)
	}
	hostFreeBefore, err := service.native.HostFreeBytes(hostMeasurementPath)
	if err != nil {
		return nil, newError(CodeHostFreeSpaceFloor, "measure-host-free-before-setup", hostMeasurementPath, err)
	}
	requiredHostFree, ok := checkedSumInt64(config.MinimumHostFreeBytes, config.MaximumBytes, config.VHDXOverheadReserveBytes)
	if !ok {
		return nil, newError(CodeHostFreeSpaceFloor, "validate-host-free-before-setup", hostMeasurementPath, fmt.Errorf("host free requirement overflows"))
	}
	if hostFreeBefore < requiredHostFree {
		return nil, newError(CodeHostFreeSpaceFloor, "validate-host-free-before-setup", hostMeasurementPath, fmt.Errorf("hostFree=%d required=%d", hostFreeBefore, requiredHostFree))
	}
	if err := prepareSetupRoot(paths); err != nil {
		return nil, err
	}
	createdVHDX := false
	committedOwner := false
	cleanupSafe := true
	defer func() {
		if !committedOwner && cleanupSafe {
			if createdVHDX {
				if cleanupErr := service.native.RemoveVHDX(paths.VHDX); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
					returnErr = errors.Join(returnErr, cleanupFailureState("remove-partial-vhdx", paths.VHDX, paths.VHDX, cleanupErr, false, "failed"))
				}
			}
			for _, cleanupPath := range []string{paths.Mount, paths.Root} {
				if cleanupErr := os.Remove(cleanupPath); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
					returnErr = errors.Join(returnErr, cleanupFailureState("remove-partial-setup-path", cleanupPath, paths.VHDX, cleanupErr, false, "failed"))
				}
			}
		}
		if returnErr != nil && ErrorCode(returnErr) != CodeCleanupFailed {
			state := "preserved"
			if cleanupSafe {
				state = "released"
			}
			returnErr = errorWithCleanupEvidence(returnErr, state, paths.VHDX, committedOwner)
		}
	}()
	if err := service.native.CreateDynamic(paths.VHDX, config.MaximumBytes); err != nil {
		return nil, mapNativeError("create-dynamic-vhdx", paths.VHDX, err)
	}
	createdVHDX = true
	cleanupSafe = false
	mounted, err := service.native.Mount(ctx, paths.VHDX, paths.Mount, true)
	if err != nil {
		mapped := mapNativeError("initialize-refs-volume", paths.VHDX, err)
		var evidence *Error
		if errors.As(mapped, &evidence) && evidence.CleanupState == "released" {
			cleanupSafe = true
		}
		return nil, mapped
	}
	mountMetrics := mounted.Metrics()
	closed := false
	defer func() {
		if !closed {
			if closeErr := closeMountedBounded(mounted); closeErr == nil {
				cleanupSafe = true
			} else {
				returnErr = cleanupFailure("cleanup-mounted-setup", paths.VHDX, errors.Join(returnErr, closeErr), committedOwner)
			}
		}
	}()
	volume := mounted.Volume()
	if err := validateVolume(volume); err != nil {
		return nil, err
	}
	for _, path := range []string{paths.PoolRoot, paths.Baselines, paths.Workers, paths.Leases, paths.Quarantine} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, newError(CodePoolCorrupt, "create-pool-layout", path, err)
		}
	}
	cloneMetrics, sourceUnchanged, err := service.syntheticCloneProbe(ctx, paths, volume.ClusterSize)
	if err != nil {
		return nil, err
	}
	identity, err := service.native.FileIdentity(paths.VHDX)
	if err != nil {
		return nil, newError(CodePoolCorrupt, "identify-vhdx", paths.VHDX, err)
	}
	token, err := randomToken()
	if err != nil {
		return nil, newError(CodePoolCorrupt, "pool-token", paths.Root, err)
	}
	metadata := PoolMetadata{
		SchemaVersion:            PoolSchemaVersion,
		Architecture:             "Managed ReFS Library Pool",
		CreatedAt:                service.now().UTC(),
		OwnershipToken:           token,
		VHDXPath:                 paths.VHDX,
		VHDXIdentity:             identity,
		VolumeGUIDPath:           volume.VolumeGUIDPath,
		Filesystem:               volume.Filesystem,
		ClusterSize:              volume.ClusterSize,
		MaximumBytes:             config.MaximumBytes,
		SoftBudgetBytes:          config.SoftBudgetBytes,
		WorkerReserveBytes:       config.WorkerReserveBytes,
		MinimumHostFreeBytes:     config.MinimumHostFreeBytes,
		VHDXOverheadReserveBytes: config.VHDXOverheadReserveBytes,
	}
	if err := writeJSONAtomic(paths.PoolFile, metadata, 0600); err != nil {
		return nil, newError(CodePoolCorrupt, "write-pool-metadata", paths.PoolFile, err)
	}
	if err := writeJSONAtomic(paths.Owner, metadata, 0600); err != nil {
		return nil, newError(CodePoolCorrupt, "write-owner-metadata", paths.Owner, err)
	}
	committedOwner = true
	residual, err := measureMountedResidual(paths)
	if err != nil {
		return nil, newError(CodeCleanupFailed, "measure-mounted-residual", paths.PoolRoot, err)
	}
	closeStarted := time.Now()
	closeErr := closeMountedBounded(mounted)
	closed = true
	if closeErr != nil {
		return nil, cleanupFailure("detach-pool-after-setup", paths.VHDX, closeErr, true)
	}
	cleanupSafe = true
	usage, _ := service.native.FileUsage(paths.VHDX)
	hostFree, _ := service.native.HostFreeBytes(paths.Root)
	result := baseResult("setup", paths, volume)
	result.Status = "PASS"
	result.Pool = &metadata
	result.BlockCloneSupported = true
	result.SourceUnchanged = sourceUnchanged
	result.BaselineUnchanged = true
	result.Metrics = poolMetricsFromClone(cloneMetrics)
	result.Metrics.PoolSetupMs = time.Since(started).Milliseconds()
	result.Metrics.PoolAttachMs = mountMetrics.AttachMs
	result.Metrics.PoolMountMs = mountMetrics.MountMs
	result.Metrics.RefsVolumeUsedBefore = volume.UsedBytes
	result.Metrics.HostVHDXLogicalBytes = usage.LogicalBytes
	result.Metrics.HostVHDXAllocatedBytes = usage.AllocatedBytes
	result.Metrics.HostFreeBytes = hostFree
	result.Metrics.CleanupMs = time.Since(closeStarted).Milliseconds()
	result.NativeWindowsStatus = "MEASURED"
	if err := completePostDetachResidual(paths, &residual, true); err != nil {
		return nil, newError(CodeCleanupFailed, "measure-post-detach-residual", paths.Mount, err)
	}
	result.Residual = residual
	return result, nil
}

func (service *Service) Status(ctx context.Context, config Config) (*Result, error) {
	return service.inspect(ctx, config, false)
}

func (service *Service) Probe(ctx context.Context, config Config) (*Result, error) {
	return service.inspect(ctx, config, true)
}

func (service *Service) inspect(ctx context.Context, config Config, runProbe bool) (returnResult *Result, returnErr error) {
	config, paths, err := NewPaths(config)
	if err != nil {
		return nil, err
	}
	if err := service.checkNative(ctx); err != nil {
		return nil, err
	}
	if err := validateExistingPoolPaths(paths); err != nil {
		return nil, err
	}
	hostMetadata, err := readPoolMetadata(paths.Owner)
	if err != nil {
		return nil, err
	}
	if err := service.validateHostOwnership(paths, hostMetadata); err != nil {
		return nil, err
	}
	mounted, err := service.native.Mount(ctx, paths.VHDX, paths.Mount, false)
	if err != nil {
		return nil, mapNativeError("mount-existing-pool", paths.VHDX, err)
	}
	closed := false
	defer func() {
		if !closed {
			if closeErr := closeMountedBounded(mounted); closeErr != nil {
				returnErr = cleanupFailure("cleanup-mounted-inspection", paths.VHDX, errors.Join(returnErr, closeErr), true)
			}
		}
	}()
	volume := mounted.Volume()
	if err := validateVolume(volume); err != nil {
		return nil, err
	}
	poolMetadata, err := readPoolMetadata(paths.PoolFile)
	if err != nil {
		return nil, err
	}
	if err := comparePoolIdentity(paths, hostMetadata, poolMetadata, volume); err != nil {
		return nil, err
	}
	result := baseResult("status", paths, volume)
	result.Status = "READY"
	result.Pool = &poolMetadata
	result.BlockCloneSupported = volume.SupportsBlockCloning
	result.Metrics.PoolAttachMs = mounted.Metrics().AttachMs
	result.Metrics.PoolMountMs = mounted.Metrics().MountMs
	result.Metrics.RefsVolumeUsedBefore = volume.UsedBytes
	if runProbe {
		result.Operation = "probe"
		result.Status = "PASS"
		cloneMetrics, sourceUnchanged, err := service.syntheticCloneProbe(ctx, paths, volume.ClusterSize)
		if err != nil {
			return nil, err
		}
		result.SourceUnchanged = sourceUnchanged
		result.BaselineUnchanged = true
		clonePoolMetrics := poolMetricsFromClone(cloneMetrics)
		clonePoolMetrics.PoolAttachMs = result.Metrics.PoolAttachMs
		clonePoolMetrics.PoolMountMs = result.Metrics.PoolMountMs
		clonePoolMetrics.RefsVolumeUsedBefore = result.Metrics.RefsVolumeUsedBefore
		result.Metrics = clonePoolMetrics
	}
	residual, err := measureMountedResidual(paths)
	if err != nil {
		return nil, newError(CodeCleanupFailed, "measure-mounted-residual", paths.PoolRoot, err)
	}
	closeStarted := time.Now()
	closeErr := closeMountedBounded(mounted)
	closed = true
	if closeErr != nil {
		return nil, cleanupFailure("detach-pool-after-inspection", paths.VHDX, closeErr, true)
	}
	usage, _ := service.native.FileUsage(paths.VHDX)
	hostFree, _ := service.native.HostFreeBytes(paths.Root)
	result.Metrics.HostVHDXLogicalBytes = usage.LogicalBytes
	result.Metrics.HostVHDXAllocatedBytes = usage.AllocatedBytes
	result.Metrics.HostFreeBytes = hostFree
	result.Metrics.CleanupMs = time.Since(closeStarted).Milliseconds()
	result.NativeWindowsStatus = "MEASURED"
	if err := completePostDetachResidual(paths, &residual, true); err != nil {
		return nil, newError(CodeCleanupFailed, "measure-post-detach-residual", paths.Mount, err)
	}
	result.Residual = residual
	return result, nil
}

func (service *Service) Remove(ctx context.Context, config Config) (returnResult *Result, returnErr error) {
	config, paths, err := NewPaths(config)
	if err != nil {
		return nil, err
	}
	if err := service.checkNative(ctx); err != nil {
		return nil, err
	}
	if err := validateExistingPoolPaths(paths); err != nil {
		return nil, err
	}
	hostMetadata, err := readPoolMetadata(paths.Owner)
	if err != nil {
		return nil, err
	}
	if err := service.validateHostOwnership(paths, hostMetadata); err != nil {
		return nil, err
	}
	mounted, err := service.native.Mount(ctx, paths.VHDX, paths.Mount, false)
	if err != nil {
		return nil, mapNativeError("mount-pool-for-remove", paths.VHDX, err)
	}
	closed := false
	defer func() {
		if !closed {
			if closeErr := closeMountedBounded(mounted); closeErr != nil {
				returnErr = cleanupFailure("cleanup-mounted-remove", paths.VHDX, errors.Join(returnErr, closeErr), true)
			}
		}
	}()
	volume := mounted.Volume()
	poolMetadata, readErr := readPoolMetadata(paths.PoolFile)
	identityErr := comparePoolIdentity(paths, hostMetadata, poolMetadata, volume)
	if readErr != nil || identityErr != nil {
		return nil, errors.Join(readErr, identityErr)
	}
	if active, err := countEntries(paths.Leases, "active-", ".json"); err != nil || active != 0 {
		if err == nil {
			err = fmt.Errorf("%d active baseline references", active)
		}
		return nil, newError(CodeBaselineInUse, "remove-pool", paths.Leases, err)
	}
	if journals, err := countEntries(paths.Leases, "worker-", ".json"); err != nil || journals != 0 {
		if err == nil {
			err = fmt.Errorf("%d worker lease journals remain", journals)
		}
		return nil, newError(CodeLeaseConflict, "remove-pool", paths.Leases, err)
	}
	if artifacts, err := countCoordinationArtifacts(paths.Leases); err != nil || artifacts != 0 {
		if err == nil {
			err = fmt.Errorf("%d coordination artifacts remain", artifacts)
		}
		return nil, newError(CodeOrphanFound, "remove-pool", paths.Leases, err)
	}
	if workers, err := countDirectories(paths.Workers); err != nil || workers != 0 {
		if err == nil {
			err = fmt.Errorf("%d worker directories remain", workers)
		}
		return nil, newError(CodeLeaseConflict, "remove-pool", paths.Workers, err)
	}
	if probes, err := countEntries(paths.PoolRoot, ".block-clone-probe-", ""); err != nil || probes != 0 {
		if err == nil {
			err = fmt.Errorf("%d synthetic probe directories remain", probes)
		}
		return nil, newError(CodeCleanupFailed, "remove-pool", paths.PoolRoot, err)
	}
	residual, err := measureMountedResidual(paths)
	if err != nil {
		return nil, newError(CodeCleanupFailed, "measure-mounted-residual", paths.PoolRoot, err)
	}
	for name, metric := range map[string]ResidualMetric{
		"baseline creation locks": residual.BaselineCreationLocks,
		"baseline staging":        residual.BaselineStagingDirs,
		"worker staging":          residual.WorkerStagingDirs,
		"unknown lease":           residual.UnknownLeaseArtifacts,
		"unknown baseline":        residual.UnknownBaselineEntries,
		"unknown worker":          residual.UnknownWorkerArtifacts,
		"quarantine":              residual.QuarantineEntries,
	} {
		if metric.Count != 0 {
			return nil, newError(CodeOrphanFound, "remove-pool", paths.PoolRoot, fmt.Errorf("%s residual=%d", name, metric.Count))
		}
	}
	started := time.Now()
	closeErr := closeMountedBounded(mounted)
	closed = true
	if closeErr != nil {
		return nil, cleanupFailure("detach-pool-before-remove", paths.VHDX, closeErr, true)
	}
	identity, err := service.native.FileIdentity(paths.VHDX)
	if err != nil || identity != hostMetadata.VHDXIdentity {
		return nil, newError(CodeOwnershipMismatch, "revalidate-vhdx-before-remove", paths.VHDX, errors.Join(err, fmt.Errorf("identity=%q expected=%q", identity, hostMetadata.VHDXIdentity)))
	}
	if err := service.native.RemoveVHDX(paths.VHDX); err != nil {
		return nil, newError(CodeCleanupFailed, "remove-vhdx", paths.VHDX, err)
	}
	if err := os.Remove(paths.Owner); err != nil {
		return nil, newError(CodeCleanupFailed, "remove-owner", paths.Owner, err)
	}
	if err := os.Remove(paths.Mount); err != nil && !os.IsNotExist(err) {
		return nil, newError(CodeCleanupFailed, "remove-mount-directory", paths.Mount, err)
	}
	if err := os.Remove(paths.Root); err != nil && !os.IsNotExist(err) {
		return nil, newError(CodeCleanupFailed, "remove-storage-root", paths.Root, err)
	}
	result := baseResult("remove", paths, volume)
	result.Status = "PASS"
	result.BlockCloneSupported = volume.SupportsBlockCloning
	result.Metrics.CleanupMs = time.Since(started).Milliseconds()
	result.NativeWindowsStatus = "MEASURED"
	if err := completePostDetachResidual(paths, &residual, true); err != nil {
		return nil, newError(CodeCleanupFailed, "measure-post-remove-residual", paths.Root, err)
	}
	result.Residual = residual
	return result, nil
}

const cleanupTimeout = 20 * time.Second

func closeMountedBounded(mounted MountedPool) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	return mounted.Close(cleanupCtx)
}

func cleanupFailure(operation, path string, cause error, ownerCommitted bool) error {
	return cleanupFailureState(operation, path, path, cause, ownerCommitted, "uncertain")
}

func cleanupFailureState(operation, path, ownedVHDX string, cause error, ownerCommitted bool, state string) error {
	return &Error{Code: CodeCleanupFailed, Operation: operation, Path: path, Cause: cause, CleanupState: state, OwnerMetadataCommitted: ownerCommitted, OwnedVHDXPath: ownedVHDX, ManualRecoveryRequired: state != "released"}
}

func (service *Service) checkNative(ctx context.Context) error {
	if service == nil || service.native == nil || service.cloner == nil {
		return newError(CodeInvalidConfiguration, "native-service", "", fmt.Errorf("native dependencies are missing"))
	}
	if err := ctx.Err(); err != nil {
		return cancelled("native-check", "", err)
	}
	if service.native.Platform() != "windows" {
		return newError(CodeUnsupportedPlatform, "native-check", service.native.Platform(), nil)
	}
	if err := service.native.EnsureAvailable(); err != nil {
		return mapNativeError("native-check", "", err)
	}
	elevated, err := service.native.IsElevated(ctx)
	if err != nil {
		return newError(CodeNotElevated, "elevation-check", "", err)
	}
	if !elevated {
		return newError(CodeNotElevated, "elevation-check", "", nil)
	}
	return nil
}

func (service *Service) syntheticCloneProbe(ctx context.Context, paths Paths, clusterSize int64) (metrics CloneMetrics, unchanged bool, returnErr error) {
	if clusterSize <= 0 {
		return CloneMetrics{}, false, newError(CodePoolCorrupt, "synthetic-clone", paths.PoolRoot, fmt.Errorf("invalid cluster size"))
	}
	token, err := randomToken()
	if err != nil {
		return CloneMetrics{}, false, newError(CodeCloneFailed, "synthetic-token", paths.PoolRoot, err)
	}
	probeRoot := filepath.Join(paths.PoolRoot, ".block-clone-probe-"+token[:12])
	sourceRoot := filepath.Join(probeRoot, "source")
	destinationRoot := filepath.Join(probeRoot, "destination")
	if err := os.MkdirAll(sourceRoot, 0700); err != nil {
		return CloneMetrics{}, false, newError(CodeCloneFailed, "create-synthetic-probe", sourceRoot, err)
	}
	defer func() {
		if cleanupErr := service.removeAll(probeRoot); cleanupErr != nil {
			returnErr = errors.Join(returnErr, cleanupFailureState("cleanup-synthetic-probe", probeRoot, paths.VHDX, cleanupErr, false, "failed"))
		}
	}()
	payload := make([]byte, clusterSize*2+137)
	for index := range payload {
		payload[index] = byte((index*31 + 17) % 251)
	}
	sourceFile := filepath.Join(sourceRoot, "payload.bin")
	if err := os.WriteFile(sourceFile, payload, 0600); err != nil {
		return CloneMetrics{}, false, newError(CodeCloneFailed, "write-synthetic-source", sourceFile, err)
	}
	sparseSource := filepath.Join(sourceRoot, "sparse.bin")
	if err := createSyntheticSparseFile(sparseSource, clusterSize); err != nil {
		return CloneMetrics{}, false, newError(CodeCloneFailed, "write-synthetic-sparse-source", sparseSource, err)
	}
	sparseBefore, err := hashFileContext(ctx, sparseSource)
	if err != nil {
		return CloneMetrics{}, false, newError(CodeCloneFailed, "hash-synthetic-sparse-source", sparseSource, err)
	}
	sourceBefore, err := hashFileContext(ctx, sourceFile)
	if err != nil {
		return CloneMetrics{}, false, newError(CodeCloneFailed, "hash-synthetic-source", sourceFile, err)
	}
	metrics, err = service.cloner.CloneTree(ctx, sourceRoot, destinationRoot, clusterSize)
	if err != nil {
		return metrics, false, mapNativeError("synthetic-block-clone", destinationRoot, err)
	}
	if err := ValidateCloneMetrics(metrics); err != nil {
		return metrics, false, err
	}
	destinationFile := filepath.Join(destinationRoot, "payload.bin")
	destinationData, err := os.ReadFile(destinationFile)
	if err != nil || !bytes.Equal(payload, destinationData) {
		return metrics, false, newError(CodeCloneFailed, "verify-synthetic-destination", destinationFile, err)
	}
	file, err := os.OpenFile(destinationFile, os.O_WRONLY, 0)
	if err != nil {
		return metrics, false, newError(CodeCloneFailed, "open-synthetic-destination", destinationFile, err)
	}
	_, writeErr := file.WriteAt([]byte("worker-private-write"), 0)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return metrics, false, newError(CodeCloneFailed, "mutate-synthetic-destination", destinationFile, errors.Join(writeErr, closeErr))
	}
	sourceAfter, err := hashFileContext(ctx, sourceFile)
	if err != nil {
		return metrics, false, newError(CodeCloneFailed, "rehash-synthetic-source", sourceFile, err)
	}
	unchanged = sourceBefore == sourceAfter
	if !unchanged {
		return metrics, false, newError(CodeCloneFailed, "verify-allocate-on-write", sourceFile, fmt.Errorf("source changed after destination mutation"))
	}
	sparseDestination := filepath.Join(destinationRoot, "sparse.bin")
	sparseAfterClone, err := hashFileContext(ctx, sparseDestination)
	if err != nil || sparseAfterClone != sparseBefore {
		return metrics, false, newError(CodeCloneFailed, "verify-synthetic-sparse-destination", sparseDestination, err)
	}
	sparseFile, err := os.OpenFile(sparseDestination, os.O_WRONLY, 0)
	if err != nil {
		return metrics, false, newError(CodeCloneFailed, "open-synthetic-sparse-destination", sparseDestination, err)
	}
	_, sparseWriteErr := sparseFile.WriteAt([]byte("private"), clusterSize)
	sparseCloseErr := sparseFile.Close()
	if sparseWriteErr != nil || sparseCloseErr != nil {
		return metrics, false, newError(CodeCloneFailed, "mutate-synthetic-sparse-destination", sparseDestination, errors.Join(sparseWriteErr, sparseCloseErr))
	}
	sparseSourceAfter, err := hashFileContext(ctx, sparseSource)
	if err != nil || sparseSourceAfter != sparseBefore {
		return metrics, false, newError(CodeCloneFailed, "verify-sparse-allocate-on-write", sparseSource, err)
	}
	return metrics, true, nil
}

func prepareSetupRoot(paths Paths) error {
	if info, err := os.Lstat(paths.Root); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return newError(CodeOwnershipMismatch, "validate-existing-root", paths.Root, fmt.Errorf("root is not a real directory"))
		}
		if reparse, inspectErr := inspectPathReparse(paths.Root); inspectErr != nil || reparse {
			return newError(CodeOwnershipMismatch, "validate-existing-root", paths.Root, errors.Join(inspectErr, fmt.Errorf("root is a reparse point")))
		}
		entries, err := os.ReadDir(paths.Root)
		if err != nil || len(entries) != 0 {
			return newError(CodePoolCorrupt, "validate-existing-root", paths.Root, errors.Join(err, fmt.Errorf("pool root must be empty")))
		}
	} else if os.IsNotExist(err) {
		if _, err := PrepareOwnedRoot(paths.Root); err != nil {
			return err
		}
	} else {
		return newError(CodePoolCorrupt, "stat-root", paths.Root, err)
	}
	if err := os.Mkdir(paths.Mount, 0700); err != nil {
		return newError(CodePoolCorrupt, "create-mount", paths.Mount, err)
	}
	if reparse, err := inspectPathReparse(paths.Mount); err != nil || reparse {
		return newError(CodeOwnershipMismatch, "validate-mount", paths.Mount, errors.Join(err, fmt.Errorf("mount is a reparse point")))
	}
	return nil
}

func validateExistingPoolPaths(paths Paths) error {
	for _, path := range []string{paths.Root, paths.Mount} {
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return newError(CodePoolNotFound, "validate-existing-pool-path", path, err)
			}
			return newError(CodePoolCorrupt, "validate-existing-pool-path", path, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return newError(CodeOwnershipMismatch, "validate-existing-pool-path", path, fmt.Errorf("path is not a real directory"))
		}
		reparse, err := inspectPathReparse(path)
		if err != nil || reparse {
			return newError(CodeOwnershipMismatch, "validate-existing-pool-path", path, errors.Join(err, fmt.Errorf("path is a reparse point")))
		}
	}
	return nil
}

func (service *Service) validateHostOwnership(paths Paths, metadata PoolMetadata) error {
	if err := validateOwnedPaths(paths); err != nil {
		return err
	}
	if metadata.SchemaVersion != PoolSchemaVersion || metadata.Architecture != managedReFSArchitecture || metadata.OwnershipToken == "" || filepath.Clean(metadata.VHDXPath) != paths.VHDX || metadata.MinimumHostFreeBytes <= 0 || metadata.VHDXOverheadReserveBytes < 0 || metadata.MaximumBytes <= 0 || metadata.SoftBudgetBytes <= 0 || metadata.WorkerReserveBytes <= 0 || metadata.ClusterSize <= 0 || !strings.EqualFold(metadata.Filesystem, "ReFS") {
		return newError(CodePoolCorrupt, "validate-owner-metadata", paths.Owner, fmt.Errorf("owner metadata does not match requested pool"))
	}
	identity, err := service.native.FileIdentity(paths.VHDX)
	if err != nil {
		if os.IsNotExist(err) {
			return newError(CodePoolNotFound, "identify-vhdx", paths.VHDX, err)
		}
		return newError(CodePoolCorrupt, "identify-vhdx", paths.VHDX, err)
	}
	if identity != metadata.VHDXIdentity {
		return newError(CodeOwnershipMismatch, "validate-vhdx-identity", paths.VHDX, fmt.Errorf("identity=%q expected=%q", identity, metadata.VHDXIdentity))
	}
	return nil
}

func comparePoolIdentity(paths Paths, host, pool PoolMetadata, volume VolumeInfo) error {
	if filepath.Clean(host.VHDXPath) != paths.VHDX || filepath.Clean(pool.VHDXPath) != paths.VHDX {
		return newError(CodePoolCorrupt, "compare-pool-identity", paths.PoolFile, fmt.Errorf("metadata VHDX path mismatch"))
	}
	if _, err := BuildVerifiedPoolPolicy(host, pool, volume); err != nil {
		return newError(CodePoolCorrupt, "compare-pool-identity", paths.PoolFile, err)
	}
	return nil
}

func readPoolMetadata(path string) (PoolMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PoolMetadata{}, newError(CodePoolNotFound, "read-pool-metadata", path, err)
		}
		return PoolMetadata{}, newError(CodePoolCorrupt, "read-pool-metadata", path, err)
	}
	var metadata PoolMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return PoolMetadata{}, newError(CodePoolCorrupt, "decode-pool-metadata", path, err)
	}
	return metadata, nil
}

func validateVolume(volume VolumeInfo) error {
	if !strings.EqualFold(volume.Filesystem, "ReFS") {
		return newError(CodePoolCorrupt, "validate-filesystem", volume.VolumeGUIDPath, fmt.Errorf("filesystem=%q", volume.Filesystem))
	}
	if volume.ClusterSize <= 0 || volume.ClusterSize&(volume.ClusterSize-1) != 0 {
		return newError(CodePoolCorrupt, "validate-cluster-size", volume.VolumeGUIDPath, fmt.Errorf("clusterSize=%d", volume.ClusterSize))
	}
	if !volume.SupportsBlockCloning {
		return newError(CodeBlockCloneUnavailable, "validate-volume-capabilities", volume.VolumeGUIDPath, nil)
	}
	return nil
}

func baseResult(operation string, paths Paths, volume VolumeInfo) *Result {
	return &Result{
		SchemaVersion:            "2",
		Operation:                operation,
		Architecture:             managedReFSArchitecture,
		ReleasedVersionModified:  false,
		PhysicalImageCreated:     false,
		DifferencingChildCreated: false,
		FallbackUsed:             false,
		Paths:                    paths,
		Volume:                   volume,
	}
}

func poolMetricsFromClone(clone CloneMetrics) PoolMetrics {
	return PoolMetrics{
		ClonedFileCount:            clone.ClonedFileCount,
		ClonedBytes:                clone.ClonedBytes,
		PhysicalCopiedFileCount:    clone.PhysicalCopiedFileCount,
		PhysicalCopiedBytes:        clone.PhysicalCopiedBytes,
		TailCopiedBytes:            clone.TailCopiedBytes,
		MetadataOnlyFileCount:      clone.MetadataOnlyFileCount,
		FailedFileCount:            clone.FailedFileCount,
		CloneTreeMs:                clone.CloneTreeMs,
		SparseFileCount:            clone.SparseFileCount,
		SparseLogicalBytes:         clone.SparseLogicalBytes,
		SparseAllocatedSourceBytes: clone.SparseAllocatedSourceBytes,
		SparseClonedBytes:          clone.SparseClonedBytes,
		SparseHoleBytes:            clone.SparseHoleBytes,
	}
}

func countEntries(root, prefix, suffix string) (int, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), suffix) {
			count++
		}
	}
	return count, nil
}

func countDirectories(root string) (int, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			count++
		}
	}
	return count, nil
}

func mapNativeError(operation, path string, err error) error {
	if err == nil {
		return nil
	}
	code := ErrorCode(err)
	if code != "unknown" {
		return err
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "refs") && strings.Contains(lower, "format"):
		code = CodeReFSFormatUnavailable
	case strings.Contains(lower, "already attached") || strings.Contains(lower, "already mounted"):
		code = CodePoolAlreadyMounted
	case strings.Contains(lower, "disk full") || strings.Contains(lower, "not enough space"):
		code = CodeDiskFull
	default:
		code = CodePoolCorrupt
	}
	return newError(code, operation, path, err)
}
