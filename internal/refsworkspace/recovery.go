package refsworkspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var recoverySyntheticProbePattern = regexp.MustCompile(`^\.block-clone-probe-[0-9a-f]{12}$`)

// RecoverIncompleteSetup is the only operation allowed to delete a precisely
// identified VHDX whose host ownership record exists but whose in-volume
// transaction never committed. It intentionally does not repair pool.json.
func (service *Service) RecoverIncompleteSetup(ctx context.Context, config Config) (returnResult *Result, returnErr error) {
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
	metadataPath, ownerCommitted, err := recoveryMetadataPath(paths)
	if err != nil {
		return nil, err
	}
	metadata, err := service.readMetadata(metadataPath)
	if err != nil {
		return nil, err
	}
	if err := service.validateHostOwnership(paths, metadata); err != nil {
		return nil, err
	}

	mounted, err := service.native.Mount(ctx, paths.VHDX, paths.Mount, false)
	if err != nil {
		return nil, persistentMountFailure(mapNativeError("mount-incomplete-pool-for-recovery", paths.VHDX, err), paths.VHDX)
	}
	closed := false
	deletionStarted := false
	defer func() {
		if !closed {
			if closeErr := closeMountedBounded(mounted); closeErr != nil {
				returnErr = cleanupFailure("cleanup-incomplete-pool-recovery", paths.VHDX, errors.Join(returnErr, closeErr), ownerCommitted)
			} else {
				closed = true
			}
		}
		if returnErr != nil && ErrorCode(returnErr) != CodeCleanupFailed {
			state := "preserved"
			if deletionStarted {
				state = "uncertain"
			}
			returnErr = errorWithCleanupEvidence(returnErr, state, paths.VHDX, ownerCommitted)
		}
	}()

	volume := mounted.Volume()
	if err := validateVolume(volume); err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSuffix(metadata.VolumeGUIDPath, `\`), strings.TrimSuffix(volume.VolumeGUIDPath, `\`)) ||
		!strings.EqualFold(metadata.Filesystem, volume.Filesystem) || metadata.ClusterSize != volume.ClusterSize {
		return nil, newError(CodeOwnershipMismatch, "validate-incomplete-pool-volume-identity", paths.Mount, fmt.Errorf("host owner and mounted volume identity differ"))
	}
	if err := validateIncompletePoolLayout(paths); err != nil {
		return nil, newError(CodeIncompleteSetup, "validate-incomplete-setup-recovery", paths.PoolRoot, err)
	}

	devDrive := mounted.DevDriveEvidence()
	closeStarted := time.Now()
	if err := closeMountedBounded(mounted); err != nil {
		return nil, cleanupFailure("detach-incomplete-pool-before-recovery", paths.VHDX, err, ownerCommitted)
	}
	closed = true
	identity, err := service.native.FileIdentity(paths.VHDX)
	if err != nil || identity != metadata.VHDXIdentity {
		return nil, newError(CodeOwnershipMismatch, "revalidate-incomplete-vhdx-before-recovery", paths.VHDX, errors.Join(err, fmt.Errorf("identity=%q expected=%q", identity, metadata.VHDXIdentity)))
	}

	deletionStarted = true
	if err := service.native.RemoveVHDX(paths.VHDX); err != nil {
		return nil, newError(CodeCleanupFailed, "remove-incomplete-vhdx", paths.VHDX, err)
	}
	if err := removeExactRecoveryMetadata(metadataPath, metadata); err != nil {
		return nil, newError(CodeCleanupFailed, "remove-incomplete-owner", metadataPath, err)
	}
	for _, path := range []string{paths.Mount, paths.Root} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, newError(CodeCleanupFailed, "remove-incomplete-pool-path", path, err)
		}
	}

	result := baseResult("recover-incomplete-setup", paths, volume)
	result.Status = "RECOVERED"
	result.DevDrive = devDrive
	result.BlockCloneSupported = volume.SupportsBlockCloning
	result.Metrics.CleanupMs = time.Since(closeStarted).Milliseconds()
	result.NativeWindowsStatus = "MEASURED"
	result.Residual = zeroRecoveryResidual()
	return result, nil
}

func recoveryMetadataPath(paths Paths) (string, bool, error) {
	_, ownerErr := os.Lstat(paths.Owner)
	_, pendingErr := os.Lstat(paths.PendingOwner)
	ownerExists := ownerErr == nil
	pendingExists := pendingErr == nil
	if ownerExists && pendingExists {
		return "", false, newError(CodeOwnershipMismatch, "select-incomplete-owner", paths.Root, fmt.Errorf("authoritative and pending owner records both exist"))
	}
	if ownerExists {
		return paths.Owner, true, nil
	}
	if pendingExists {
		return paths.PendingOwner, false, nil
	}
	if ownerErr != nil && !os.IsNotExist(ownerErr) {
		return "", false, ownerErr
	}
	if pendingErr != nil && !os.IsNotExist(pendingErr) {
		return "", false, pendingErr
	}
	return "", false, newError(CodePoolNotFound, "select-incomplete-owner", paths.Root, fmt.Errorf("no ownership record"))
}

func validateIncompletePoolLayout(paths Paths) error {
	if _, err := os.Lstat(paths.PoolFile); err == nil {
		return fmt.Errorf("pool.json exists; normal remove is required")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := verifyRequiredDirectories(paths); err != nil {
		return err
	}
	for _, path := range []string{paths.Baselines, paths.Workers, paths.Leases, paths.Quarantine} {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return fmt.Errorf("managed directory is not empty: %s", path)
		}
	}
	rootEntries, err := os.ReadDir(paths.Mount)
	if err != nil {
		return err
	}
	for _, entry := range rootEntries {
		switch strings.ToLower(entry.Name()) {
		case "testplay", "system volume information", "$recycle.bin":
		default:
			return fmt.Errorf("unknown volume-root entry: %s", entry.Name())
		}
	}
	testplayEntries, err := os.ReadDir(paths.PoolRoot)
	if err != nil {
		return err
	}
	for _, entry := range testplayEntries {
		switch entry.Name() {
		case "baselines", "workers", "leases", "quarantine":
			continue
		}
		if !recoverySyntheticProbePattern.MatchString(entry.Name()) {
			return fmt.Errorf("unknown testplay entry: %s", entry.Name())
		}
		if err := validateRecoverySyntheticProbe(filepath.Join(paths.PoolRoot, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func verifyRequiredDirectories(paths Paths) error {
	for _, path := range []string{paths.PoolRoot, paths.Baselines, paths.Workers, paths.Leases, paths.Quarantine} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("required path is not a real directory: %s", path)
		}
		reparse, err := inspectPathReparse(path)
		if err != nil || reparse {
			return errors.Join(err, fmt.Errorf("required path is a reparse point: %s", path))
		}
	}
	return nil
}

func validateRecoverySyntheticProbe(root string) error {
	allowed := map[string]bool{
		".": true, "source": true, "destination": true,
		filepath.Join("source", "payload.bin"):      true,
		filepath.Join("source", "sparse.bin"):       true,
		filepath.Join("destination", "payload.bin"): true,
		filepath.Join("destination", "sparse.bin"):  true,
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || !allowed[relative] {
			return errors.Join(err, fmt.Errorf("unexpected synthetic probe entry: %s", path))
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		reparse, err := inspectPathReparse(path)
		if err != nil || reparse || info.Mode()&os.ModeSymlink != 0 {
			return errors.Join(err, fmt.Errorf("synthetic probe entry is a reparse point: %s", path))
		}
		if relative == "source" || relative == "destination" || relative == "." {
			if !info.IsDir() {
				return fmt.Errorf("synthetic probe directory is not a directory: %s", path)
			}
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("synthetic probe payload is not a regular file: %s", path)
		}
		return nil
	})
}

func removeExactRecoveryMetadata(path string, expected PoolMetadata) error {
	actual, err := readPoolMetadata(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("ownership metadata changed before recovery deletion")
	}
	return os.Remove(path)
}

func zeroRecoveryResidual() Residual {
	zero := ResidualMetric{Measured: true, Count: 0}
	return Residual{
		Status:             "MEASURED_ZERO",
		ActiveBaselineUses: zero, WorkerLeaseJournals: zero, WorkerDirectories: zero,
		BaselineCreationLocks: zero, BaselineStagingDirs: zero, WorkerStagingDirs: zero,
		UnknownLeaseArtifacts: zero, UnknownBaselineEntries: zero, UnknownWorkerArtifacts: zero,
		QuarantineEntries: zero, ReservationLocks: zero, BaselineCoordinationLocks: zero,
		BaselineMutationMarkers: zero, CoordinationArtifacts: zero, SyntheticProbeDirectories: zero,
		MountReparsePoints: zero, MountDirectoryEntries: zero, Junctions: zero, AttachedDisks: zero,
		ProbeProcesses: ResidualMetric{Measured: false, Count: 0}, OwnedVHDXFiles: zero,
	}
}
