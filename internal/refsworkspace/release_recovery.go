package refsworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var recoveryOwnershipTokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// RecoverReleasedWorkerResidual removes only the exact stale active-use marker
// left by a fully released worker. It is intentionally narrower than general
// worker, process-termination, or reboot recovery.
func (service *Service) RecoverReleasedWorkerResidual(ctx context.Context, config Config, keyDigest, leaseID string) (returnResult *Result, returnErr error) {
	if !digestNamePattern.MatchString(keyDigest) {
		return nil, newError(CodeInvalidConfiguration, "validate-released-worker-recovery-key", keyDigest, fmt.Errorf("64 lowercase hex characters required"))
	}
	if !leaseIDPattern.MatchString(leaseID) {
		return nil, newError(CodeInvalidConfiguration, "validate-released-worker-recovery-lease", leaseID, fmt.Errorf("invalid lease id"))
	}
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
	if _, err := os.Lstat(paths.PendingOwner); err == nil {
		return nil, newError(CodeIncompleteSetup, "recover-released-worker-residual", paths.PendingOwner, fmt.Errorf("pending owner exists"))
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	host, err := service.readMetadata(paths.Owner)
	if err != nil {
		return nil, err
	}
	if err := service.validateHostOwnership(paths, host); err != nil {
		return nil, err
	}
	if reparse, entries, err := service.inspectUnmounted(paths.Mount); err != nil || reparse != 0 || entries != 0 {
		return nil, newError(CodePoolAlreadyMounted, "validate-recovery-pool-detached", paths.Mount, errors.Join(err, fmt.Errorf("reparse=%d entries=%d", reparse, entries)))
	}
	if running, err := service.runningProcesses([]string{"Unity", "testplay-refs-probe", "testplay-refs-unity-phase2"}); err != nil || len(running) != 0 {
		return nil, newError(CodeLeaseConflict, "validate-recovery-processes", paths.Root, errors.Join(err, fmt.Errorf("running=%v", running)))
	}

	mounted, err := service.native.Mount(ctx, paths.VHDX, paths.Mount, false)
	if err != nil {
		return nil, persistentMountFailure(mapNativeError("mount-released-worker-residual", paths.VHDX, err), paths.VHDX)
	}
	closed := false
	defer func() {
		if !closed {
			if closeErr := closeMountedBounded(mounted); closeErr != nil {
				returnErr = cleanupFailure("cleanup-released-worker-recovery", paths.VHDX, errors.Join(returnErr, closeErr), true)
			} else {
				closed = true
			}
		}
		if returnErr != nil && ErrorCode(returnErr) != CodeCleanupFailed {
			returnErr = errorWithCleanupEvidence(returnErr, "preserved", paths.VHDX, true)
		}
	}()

	volume := mounted.Volume()
	if err := validateVolume(volume); err != nil {
		return nil, err
	}
	if _, err := mounted.WaitReady(ctx, paths, host); err != nil {
		return nil, err
	}
	pool, err := service.readMetadata(paths.PoolFile)
	if err != nil {
		return nil, err
	}
	if err := service.compareIdentity(paths, host, pool, volume); err != nil {
		return nil, err
	}
	if err := validateRecoveryBaseline(ctx, paths, keyDigest); err != nil {
		return nil, err
	}

	markerPath, marker, residual, err := validateReleasedWorkerResidualShape(paths, keyDigest, leaseID)
	if err != nil {
		return nil, newError(CodeOrphanFound, "validate-released-worker-residual", paths.PoolRoot, err)
	}
	evidence := &ReleasedWorkerRecoveryEvidence{KeyDigest: keyDigest, LeaseID: leaseID, MarkerPath: markerPath, PreconditionResidual: residual}
	if err := removeExactActiveUseMarker(markerPath, marker); err != nil {
		return nil, newError(CodeCleanupFailed, "remove-released-worker-active-use", markerPath, err)
	}
	evidence.MarkerRemoved = true
	evidence.FlushAttempted = true
	if err := mounted.Flush(ctx); err != nil {
		return nil, newError(CodeCleanupFailed, "flush-released-worker-recovery", volume.VolumeGUIDPath, err)
	}
	evidence.FlushSucceeded = true
	if err := closeMountedBounded(mounted); err != nil {
		return nil, cleanupFailure("detach-released-worker-recovery", paths.VHDX, err, true)
	}
	closed = true
	evidence.FirstDetachSucceeded = true
	evidence.DurableReattachAttempted = true

	status, err := service.Status(ctx, config)
	if status != nil {
		durable := status.Residual
		evidence.DurableResidual = &durable
	}
	if err != nil {
		return nil, err
	}
	if err := validateDurableReleaseStatus(status); err != nil {
		return nil, newError(CodeWorkerReleasePersistenceFailed, "verify-recovered-marker-after-reattach", markerPath, err)
	}
	evidence.DurableAbsenceVerified = true

	removed, err := service.Remove(ctx, config)
	if err != nil {
		return nil, err
	}
	evidence.PoolRemoved = true
	removed.Operation = "recover-released-worker-residual"
	removed.Status = "RECOVERED"
	removed.ReleasedWorkerRecovery = evidence
	return removed, nil
}

func validateRecoveryBaseline(ctx context.Context, paths Paths, keyDigest string) error {
	baselinePath := filepath.Join(paths.Baselines, keyDigest)
	data, err := os.ReadFile(filepath.Join(baselinePath, baselineMetadataFile))
	if err != nil {
		return newError(CodeBaselineCorrupt, "read-recovery-baseline", baselinePath, err)
	}
	var metadata BaselineMetadata
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.Key.Digest != keyDigest {
		return newError(CodeBaselineCorrupt, "decode-recovery-baseline", baselinePath, errors.Join(err, fmt.Errorf("key digest mismatch")))
	}
	resolution, _, err := NewLibraryBaselineStore(paths).Verify(ctx, &Baseline{Path: baselinePath, LibraryPath: filepath.Join(baselinePath, "Library"), Metadata: metadata})
	if err != nil || resolution.State != BaselineValid || resolution.Baseline == nil {
		return newError(CodeBaselineCorrupt, "verify-recovery-baseline", baselinePath, errors.Join(err, fmt.Errorf("state=%s", resolution.State)))
	}
	return nil
}

func validateReleasedWorkerResidualShape(paths Paths, keyDigest, leaseID string) (string, activeUse, Residual, error) {
	residual, err := measureMountedResidual(paths)
	if err != nil {
		return "", activeUse{}, residual, err
	}
	if residual.Status != "MOUNTED_MEASURED_NONZERO" || residual.ActiveBaselineUses.Count != 1 {
		return "", activeUse{}, residual, fmt.Errorf("expected exactly one active marker; status=%s active=%d", residual.Status, residual.ActiveBaselineUses.Count)
	}
	for name, metric := range map[string]ResidualMetric{
		"worker journals": residual.WorkerLeaseJournals, "workers": residual.WorkerDirectories,
		"worker staging": residual.WorkerStagingDirs, "quarantine": residual.QuarantineEntries,
		"unknown leases": residual.UnknownLeaseArtifacts, "unknown workers": residual.UnknownWorkerArtifacts,
		"reservations": residual.ReservationLocks, "coordination": residual.CoordinationArtifacts,
		"junctions": residual.Junctions, "baseline staging": residual.BaselineStagingDirs,
		"unknown baselines": residual.UnknownBaselineEntries, "synthetic probes": residual.SyntheticProbeDirectories,
	} {
		if !metric.Measured || metric.Count != 0 {
			return "", activeUse{}, residual, fmt.Errorf("%s measured=%t count=%d", name, metric.Measured, metric.Count)
		}
	}
	markerPath := filepath.Join(paths.Leases, "active-"+keyDigest+"-"+leaseID+".json")
	info, err := os.Lstat(markerPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", activeUse{}, residual, errors.Join(err, fmt.Errorf("marker is not a regular file"))
	}
	reparse, err := inspectPathReparse(markerPath)
	if err != nil || reparse {
		return "", activeUse{}, residual, errors.Join(err, fmt.Errorf("marker is a reparse point"))
	}
	data, err := os.ReadFile(markerPath)
	var marker activeUse
	if err != nil || json.Unmarshal(data, &marker) != nil || marker.SchemaVersion != LeaseSchemaVersion || marker.KeyDigest != keyDigest || marker.LeaseID != leaseID || !recoveryOwnershipTokenPattern.MatchString(marker.OwnershipToken) {
		return "", activeUse{}, residual, errors.Join(err, fmt.Errorf("marker JSON identity is invalid"))
	}
	return markerPath, marker, residual, nil
}

func removeExactActiveUseMarker(path string, expected activeUse) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var actual activeUse
	if err := json.Unmarshal(data, &actual); err != nil || actual != expected {
		return errors.Join(err, fmt.Errorf("active-use ownership changed before deletion"))
	}
	return os.Remove(path)
}
