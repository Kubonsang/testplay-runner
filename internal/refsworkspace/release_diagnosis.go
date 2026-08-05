package refsworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const ReleaseResidualCorruptJournal = "RELEASE_NAMESPACE_RESURRECTED_WITH_CORRUPT_JOURNAL"

type WorkerReleaseDiagnosis struct {
	SchemaVersion   int                     `json:"schemaVersion"`
	Status          string                  `json:"status"`
	Operation       string                  `json:"operation"`
	Classification  string                  `json:"classification"`
	ReadOnlyAttach  bool                    `json:"readOnlyAttach"`
	Paths           Paths                   `json:"paths"`
	HostOwner       PoolMetadata            `json:"hostOwner"`
	Pool            PoolMetadata            `json:"pool"`
	Volume          VolumeInfo              `json:"volume"`
	DevDrive        DevDriveEvidence        `json:"devDrive"`
	Residual        Residual                `json:"residual"`
	LeaseArtifacts  []LeaseArtifactEvidence `json:"leaseArtifacts"`
	MarkerArtifact  *LeaseArtifactEvidence  `json:"markerArtifact,omitempty"`
	InspectionError string                  `json:"inspectionError,omitempty"`
	CleanupState    string                  `json:"cleanupState"`
}

// DiagnoseReleasedWorkerResidual performs a read-only attach and records
// byte-safe evidence. Corrupt JSON is classified, not treated as a reason to
// discard the rest of the residual measurement.
func (service *Service) DiagnoseReleasedWorkerResidual(ctx context.Context, config Config, keyDigest, leaseID string) (diagnosis *WorkerReleaseDiagnosis, returnErr error) {
	if !digestNamePattern.MatchString(keyDigest) || !leaseIDPattern.MatchString(leaseID) {
		return nil, newError(CodeInvalidConfiguration, "validate-worker-release-diagnosis", leaseID, fmt.Errorf("exact key digest and lease id required"))
	}
	config, paths, err := NewPaths(config)
	if err != nil {
		return nil, err
	}
	if err := service.checkNative(ctx); err != nil {
		return nil, err
	}
	if err := rejectPendingOwner(paths); err != nil {
		return nil, err
	}
	if err := validateExistingPoolPaths(paths); err != nil {
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
		return nil, newError(CodePoolAlreadyMounted, "validate-diagnosis-pool-detached", paths.Mount, errors.Join(err, fmt.Errorf("reparse=%d entries=%d", reparse, entries)))
	}
	if running, err := service.runningProcesses([]string{"Unity", "testplay-refs-probe", "testplay-refs-unity-phase2"}); err != nil || len(running) != 0 {
		return nil, newError(CodeLeaseConflict, "validate-diagnosis-processes", paths.Root, errors.Join(err, fmt.Errorf("running=%v", running)))
	}
	mounted, err := service.native.MountReadOnly(ctx, paths.VHDX, paths.Mount)
	if err != nil {
		return nil, persistentMountFailure(mapNativeError("mount-worker-release-diagnosis-read-only", paths.VHDX, err), paths.VHDX)
	}
	closed := false
	defer func() {
		if !closed {
			if closeErr := closeMountedBounded(mounted); closeErr != nil {
				returnErr = cleanupFailure("cleanup-worker-release-diagnosis", paths.VHDX, errors.Join(returnErr, closeErr), true)
			} else {
				closed = true
			}
		}
		if diagnosis != nil {
			if returnErr == nil {
				diagnosis.CleanupState = "released"
			} else {
				diagnosis.CleanupState = "uncertain"
			}
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
	residual, residualErr := measureMountedResidual(paths)
	artifacts := residualArtifacts(residualErr)
	for index := range artifacts {
		if identity, identityErr := service.native.FileIdentity(artifacts[index].Path); identityErr == nil {
			artifacts[index].FileIdentity = identity
		}
	}
	markerPath := filepath.Join(paths.Leases, "active-"+keyDigest+"-"+leaseID+".json")
	markerEvidence, markerErr := inspectActiveUseArtifact(markerPath)
	if markerErr == nil {
		if identity, identityErr := service.native.FileIdentity(markerPath); identityErr == nil {
			markerEvidence.FileIdentity = identity
		}
	}
	diagnosis = &WorkerReleaseDiagnosis{
		SchemaVersion: 1, Status: "PASS", Operation: "diagnose-worker-release-residual", ReadOnlyAttach: true,
		Paths: paths, HostOwner: host, Pool: pool, Volume: volume, DevDrive: mounted.DevDriveEvidence(),
		Residual: residual, LeaseArtifacts: artifacts,
	}
	if markerErr == nil {
		diagnosis.MarkerArtifact = &markerEvidence
	}
	if residualErr != nil {
		diagnosis.InspectionError = residualErr.Error()
	}
	if markerErr != nil {
		diagnosis.InspectionError = errors.Join(residualErr, markerErr).Error()
	}
	diagnosis.Classification = classifyWorkerReleaseDiagnosis(*diagnosis, keyDigest, leaseID)
	if err := closeMountedBounded(mounted); err != nil {
		return diagnosis, cleanupFailure("detach-worker-release-diagnosis", paths.VHDX, err, true)
	}
	closed = true
	diagnosis.CleanupState = "released"
	return diagnosis, nil
}

func inspectActiveUseArtifact(path string) (LeaseArtifactEvidence, error) {
	evidence := LeaseArtifactEvidence{Name: filepath.Base(path), Path: path, Kind: "active-use", DecodeStatus: "NOT_ATTEMPTED"}
	info, err := os.Lstat(path)
	if err != nil {
		return evidence, err
	}
	evidence.Size, evidence.Mode = info.Size(), info.Mode().String()
	reparse, reparseErr := inspectPathReparse(path)
	evidence.ReparsePoint = reparse
	if reparseErr != nil || reparse || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return evidence, errors.Join(reparseErr, fmt.Errorf("active marker is not a real non-reparse file"))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return evidence, err
	}
	digest := sha256.Sum256(data)
	evidence.SHA256 = fmt.Sprintf("%x", digest[:])
	leading := data
	if len(leading) > 64 {
		leading = leading[:64]
	}
	evidence.LeadingBytesHex = fmt.Sprintf("%x", leading)
	var marker activeUse
	if err := json.Unmarshal(data, &marker); err != nil {
		evidence.DecodeStatus, evidence.DecodeError = "FAILED", err.Error()
		return evidence, err
	}
	evidence.DecodeStatus = "PASS"
	evidence.KeyDigest, evidence.LeaseID, evidence.OwnershipToken = marker.KeyDigest, marker.LeaseID, marker.OwnershipToken
	return evidence, nil
}

func classifyWorkerReleaseDiagnosis(diagnosis WorkerReleaseDiagnosis, keyDigest, leaseID string) string {
	if diagnosis.Residual.ActiveBaselineUses.Count != 1 || diagnosis.Residual.WorkerLeaseJournals.Count != 1 ||
		diagnosis.Residual.WorkerDirectories.Count != 0 || diagnosis.Residual.WorkerStagingDirs.Count != 0 ||
		diagnosis.Residual.QuarantineEntries.Count != 0 || diagnosis.Residual.UnknownLeaseArtifacts.Count != 0 ||
		diagnosis.Residual.UnknownWorkerArtifacts.Count != 0 || diagnosis.Residual.ReservationLocks.Count != 0 ||
		diagnosis.Residual.CoordinationArtifacts.Count != 0 || diagnosis.Residual.BaselineStagingDirs.Count != 0 ||
		diagnosis.Residual.UnknownBaselineEntries.Count != 0 || len(diagnosis.LeaseArtifacts) != 1 ||
		diagnosis.LeaseArtifacts[0].Name != "worker-"+leaseID+".json" || diagnosis.LeaseArtifacts[0].DecodeStatus != "FAILED" ||
		diagnosis.MarkerArtifact == nil || diagnosis.MarkerArtifact.DecodeStatus != "PASS" ||
		diagnosis.MarkerArtifact.KeyDigest != keyDigest || diagnosis.MarkerArtifact.LeaseID != leaseID ||
		!recoveryOwnershipTokenPattern.MatchString(diagnosis.MarkerArtifact.OwnershipToken) {
		return "OTHER"
	}
	return ReleaseResidualCorruptJournal
}
