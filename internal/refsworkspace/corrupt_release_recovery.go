package refsworkspace

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
)

type corruptRecoveryReceipt struct {
	SchemaVersion      int       `json:"schemaVersion"`
	Phase              string    `json:"phase"`
	UpdatedAt          time.Time `json:"updatedAt"`
	KeyDigest          string    `json:"keyDigest"`
	LeaseID            string    `json:"leaseId"`
	VHDXIdentity       string    `json:"vhdxIdentity"`
	PoolOwnershipToken string    `json:"poolOwnershipToken"`
	JournalPath        string    `json:"journalPath"`
	JournalSHA256      string    `json:"journalSha256"`
	MarkerPath         string    `json:"markerPath"`
	MarkerSHA256       string    `json:"markerSha256"`
	EvidenceZIPSHA256  string    `json:"evidenceZipSha256"`
	DiagnosisSHA256    string    `json:"diagnosisSha256"`
}

type phase2RecoverySummary struct {
	Status            string                     `json:"status"`
	Error             string                     `json:"error"`
	Worker            *UnityPhase2WorkerEvidence `json:"worker"`
	Release           *WorkerMetrics             `json:"release"`
	ReleaseResidual   *Residual                  `json:"releaseResidual"`
	SourceUnchanged   *bool                      `json:"sourceUnchanged"`
	BaselineUnchanged *bool                      `json:"baselineUnchanged"`
	Parity            *UnityPhase2Parity         `json:"parity"`
}

// RecoverCorruptReleasedWorkerResidual is restricted to the observed clean
// release whose active marker and corrupt worker journal reappeared after an
// unflushed detach. It is not general orphan or forced-termination recovery.
func (service *Service) RecoverCorruptReleasedWorkerResidual(ctx context.Context, config Config, request CorruptWorkerRecoveryRequest) (returnResult *Result, returnErr error) {
	config, paths, err := NewPaths(config)
	if err != nil {
		return nil, err
	}
	if err := validateCorruptRecoveryRequest(paths, request); err != nil {
		return nil, err
	}
	evidenceZipHash, err := verifyFileSHA256(request.EvidenceZIP, request.EvidenceZIPSHA256)
	if err != nil {
		return nil, newError(CodeOwnershipMismatch, "verify-worker-release-evidence-zip", request.EvidenceZIP, err)
	}
	diagnosisHash, err := verifyFileSHA256(request.DiagnosisPath, request.DiagnosisSHA256)
	if err != nil {
		return nil, newError(CodeOwnershipMismatch, "verify-worker-release-diagnosis", request.DiagnosisPath, err)
	}
	diagnosisInfo, err := os.Stat(request.DiagnosisPath)
	if err != nil || diagnosisInfo.Size() > 4<<20 {
		return nil, newError(CodeOwnershipMismatch, "bound-worker-release-diagnosis", request.DiagnosisPath, errors.Join(err, fmt.Errorf("diagnosis exceeds 4 MiB bound")))
	}
	diagnosisData, err := os.ReadFile(request.DiagnosisPath)
	if err != nil {
		return nil, err
	}
	var diagnosis WorkerReleaseDiagnosis
	if err := json.Unmarshal(diagnosisData, &diagnosis); err != nil || diagnosis.Classification != ReleaseResidualCorruptJournal || diagnosis.CleanupState != "released" {
		return nil, newError(CodeOwnershipMismatch, "validate-worker-release-diagnosis", request.DiagnosisPath, errors.Join(err, fmt.Errorf("classification=%s cleanup=%s", diagnosis.Classification, diagnosis.CleanupState)))
	}
	summary, acquire, err := readPhase2RecoveryEvidence(request.EvidenceZIP)
	if err != nil {
		return nil, newError(CodeOwnershipMismatch, "read-worker-release-evidence", request.EvidenceZIP, err)
	}
	if err := validatePhase2RecoveryEvidence(summary, acquire, request, paths); err != nil {
		return nil, newError(CodeOwnershipMismatch, "validate-worker-release-evidence", request.EvidenceZIP, err)
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
	if host != diagnosis.HostOwner {
		return nil, newError(CodeOwnershipMismatch, "compare-recovery-diagnosis-owner", paths.Owner, fmt.Errorf("host owner changed after diagnosis"))
	}
	if reparse, entries, err := service.inspectUnmounted(paths.Mount); err != nil || reparse != 0 || entries != 0 {
		return nil, newError(CodePoolAlreadyMounted, "validate-corrupt-recovery-pool-detached", paths.Mount, errors.Join(err, fmt.Errorf("reparse=%d entries=%d", reparse, entries)))
	}
	if running, err := service.runningProcesses([]string{"Unity", "testplay-refs-probe", "testplay-refs-unity-phase2"}); err != nil || len(running) != 0 {
		return nil, newError(CodeLeaseConflict, "validate-corrupt-recovery-processes", paths.Root, errors.Join(err, fmt.Errorf("running=%v", running)))
	}
	receipt, resume, err := openRecoveryArtifactRoot(request.ArtifactRoot)
	if err != nil {
		return nil, newError(CodeInvalidConfiguration, "prepare-corrupt-recovery-artifacts", request.ArtifactRoot, err)
	}

	mounted, err := service.native.Mount(ctx, paths.VHDX, paths.Mount, false)
	if err != nil {
		return nil, persistentMountFailure(mapNativeError("mount-corrupt-worker-release-recovery", paths.VHDX, err), paths.VHDX)
	}
	closed := false
	defer func() {
		if !closed {
			if closeErr := closeMountedBounded(mounted); closeErr != nil {
				returnErr = cleanupFailure("cleanup-corrupt-worker-release-recovery", paths.VHDX, errors.Join(returnErr, closeErr), true)
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
	if pool != diagnosis.Pool || !sameDiagnosedVolume(volume, diagnosis.Volume) {
		return nil, newError(CodeOwnershipMismatch, "compare-recovery-diagnosis-volume", volume.VolumeGUIDPath, fmt.Errorf("mounted pool or volume changed after diagnosis"))
	}
	if err := validateRecoveryBaseline(ctx, paths, request.KeyDigest); err != nil {
		return nil, err
	}
	phase := ""
	if resume {
		phase = receipt.Phase
		if err := validateRecoveryReceipt(receipt, host, request, diagnosis); err != nil {
			return nil, newError(CodeOwnershipMismatch, "validate-corrupt-recovery-receipt", filepath.Join(request.ArtifactRoot, "recovery-receipt.json"), err)
		}
	}
	journal, marker, residual, err := validateCorruptRecoveryPhase(paths, request, diagnosis, acquire.Metadata, phase)
	if err != nil {
		return nil, newError(CodeOrphanFound, "validate-corrupt-released-worker-residual", paths.PoolRoot, err)
	}
	evidence := &CorruptWorkerRecoveryEvidence{
		KeyDigest: request.KeyDigest, LeaseID: request.LeaseID, EvidenceZIPSHA256: evidenceZipHash,
		DiagnosisSHA256: diagnosisHash, Journal: journal, Marker: marker, PreconditionResidual: residual,
		JournalBackupPath: filepath.Join(request.ArtifactRoot, "worker-journal.raw"),
		MarkerBackupPath:  filepath.Join(request.ArtifactRoot, "active-use-marker.raw"),
		ReceiptPath:       filepath.Join(request.ArtifactRoot, "recovery-receipt.json"),
	}
	if !resume {
		journalData, err := os.ReadFile(journal.Path)
		if err != nil {
			return nil, err
		}
		markerData, err := os.ReadFile(marker.Path)
		if err != nil {
			return nil, err
		}
		if err := atomicfile.WriteExclusiveDurable(evidence.JournalBackupPath, journalData, 0600); err != nil {
			return nil, newError(CodeCleanupFailed, "backup-corrupt-worker-journal", evidence.JournalBackupPath, err)
		}
		if err := atomicfile.WriteExclusiveDurable(evidence.MarkerBackupPath, markerData, 0600); err != nil {
			return nil, newError(CodeCleanupFailed, "backup-stale-active-marker", evidence.MarkerBackupPath, err)
		}
		receipt = corruptRecoveryReceipt{
			SchemaVersion: 1, Phase: "captured", UpdatedAt: time.Now().UTC(), KeyDigest: request.KeyDigest, LeaseID: request.LeaseID,
			VHDXIdentity: host.VHDXIdentity, PoolOwnershipToken: host.OwnershipToken,
			JournalPath: journal.Path, JournalSHA256: journal.SHA256, MarkerPath: marker.Path, MarkerSHA256: marker.SHA256,
			EvidenceZIPSHA256: evidenceZipHash, DiagnosisSHA256: diagnosisHash,
		}
		if err := writeReceiptExclusive(evidence.ReceiptPath, receipt); err != nil {
			return nil, newError(CodeCleanupFailed, "create-corrupt-recovery-receipt", evidence.ReceiptPath, err)
		}
		phase = "captured"
	} else {
		if _, err := verifyFileSHA256(evidence.JournalBackupPath, journal.SHA256); err != nil {
			return nil, newError(CodeOwnershipMismatch, "verify-corrupt-worker-journal-backup", evidence.JournalBackupPath, err)
		}
		if _, err := verifyFileSHA256(evidence.MarkerBackupPath, marker.SHA256); err != nil {
			return nil, newError(CodeOwnershipMismatch, "verify-stale-active-marker-backup", evidence.MarkerBackupPath, err)
		}
	}
	if phase == "captured" {
		if err := removeExactHashedFile(journal.Path, journal.SHA256); err != nil {
			return nil, newError(CodeCleanupFailed, "remove-corrupt-worker-journal", journal.Path, err)
		}
		if err := updateRecoveryReceipt(evidence.ReceiptPath, &receipt, "journal-removed"); err != nil {
			return nil, err
		}
		phase = "journal-removed"
	}
	evidence.JournalRemoved = phase != "captured"
	if phase == "journal-removed" {
		if err := removeExactHashedFile(marker.Path, marker.SHA256); err != nil {
			return nil, newError(CodeCleanupFailed, "remove-stale-active-marker", marker.Path, err)
		}
		if err := updateRecoveryReceipt(evidence.ReceiptPath, &receipt, "marker-removed"); err != nil {
			return nil, err
		}
		phase = "marker-removed"
	}
	evidence.MarkerRemoved = phase != "captured" && phase != "journal-removed"
	if err := mounted.Flush(ctx); err != nil {
		return nil, newError(CodeCleanupFailed, "flush-corrupt-worker-release-recovery", volume.VolumeGUIDPath, err)
	}
	evidence.FlushSucceeded = true
	if err := updateRecoveryReceipt(evidence.ReceiptPath, &receipt, "volume-flushed"); err != nil {
		return nil, err
	}
	if err := closeMountedBounded(mounted); err != nil {
		return nil, cleanupFailure("detach-corrupt-worker-release-recovery", paths.VHDX, err, true)
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
		return nil, newError(CodeWorkerReleasePersistenceFailed, "verify-corrupt-worker-release-recovery", paths.PoolRoot, err)
	}
	evidence.DurableAbsenceVerified = true
	if err := updateRecoveryReceipt(evidence.ReceiptPath, &receipt, "durable-absence-verified"); err != nil {
		return nil, err
	}
	removed, err := service.Remove(ctx, config)
	if err != nil {
		return nil, err
	}
	evidence.PoolRemoved = true
	if err := updateRecoveryReceipt(evidence.ReceiptPath, &receipt, "pool-removed"); err != nil {
		return nil, err
	}
	removed.Operation = "recover-corrupt-released-worker-residual"
	removed.Status = "RECOVERED"
	removed.CorruptWorkerRecovery = evidence
	return removed, nil
}

func sameDiagnosedVolume(actual, diagnosed VolumeInfo) bool {
	return strings.EqualFold(actual.VolumeGUIDPath, diagnosed.VolumeGUIDPath) &&
		strings.EqualFold(actual.Filesystem, diagnosed.Filesystem) && actual.ClusterSize == diagnosed.ClusterSize &&
		actual.SupportsBlockCloning == diagnosed.SupportsBlockCloning
}

func validateCorruptRecoveryRequest(paths Paths, request CorruptWorkerRecoveryRequest) error {
	if !digestNamePattern.MatchString(request.KeyDigest) || !leaseIDPattern.MatchString(request.LeaseID) {
		return newError(CodeInvalidConfiguration, "validate-corrupt-recovery-identity", request.LeaseID, fmt.Errorf("exact key digest and lease id required"))
	}
	for name, value := range map[string]string{"evidence zip": request.EvidenceZIP, "diagnosis": request.DiagnosisPath, "artifact root": request.ArtifactRoot} {
		if value == "" || !filepath.IsAbs(value) {
			return newError(CodeInvalidConfiguration, "validate-corrupt-recovery-path", value, fmt.Errorf("absolute %s path required", name))
		}
	}
	if !digestNamePattern.MatchString(strings.ToLower(request.EvidenceZIPSHA256)) || !digestNamePattern.MatchString(strings.ToLower(request.DiagnosisSHA256)) {
		return newError(CodeInvalidConfiguration, "validate-corrupt-recovery-hash", request.EvidenceZIP, fmt.Errorf("SHA-256 values required"))
	}
	if PathWithin(paths.Root, request.EvidenceZIP) || PathWithin(paths.Root, request.DiagnosisPath) || PathWithin(paths.Root, request.ArtifactRoot) {
		return newError(CodeInvalidConfiguration, "validate-corrupt-recovery-artifact-isolation", request.ArtifactRoot, fmt.Errorf("recovery evidence must be outside the pool root"))
	}
	return nil
}

func verifyFileSHA256(path, expected string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	actual := fmt.Sprintf("%x", hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return actual, fmt.Errorf("SHA-256 mismatch: actual=%s expected=%s", actual, expected)
	}
	return actual, nil
}

func readPhase2RecoveryEvidence(path string) (phase2RecoverySummary, UnityPhase2WorkerEvidence, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return phase2RecoverySummary{}, UnityPhase2WorkerEvidence{}, err
	}
	defer archive.Close()
	var summary phase2RecoverySummary
	var acquire UnityPhase2WorkerEvidence
	foundSummary, foundAcquire := false, false
	for _, file := range archive.File {
		switch filepath.ToSlash(file.Name) {
		case "summary.json":
			if foundSummary {
				return summary, acquire, fmt.Errorf("duplicate summary.json entry")
			}
			foundSummary = true
			if err := decodeBoundedZipJSON(file, &summary); err != nil {
				return summary, acquire, err
			}
		case "worker-acquire.json":
			if foundAcquire {
				return summary, acquire, fmt.Errorf("duplicate worker-acquire.json entry")
			}
			foundAcquire = true
			if err := decodeBoundedZipJSON(file, &acquire); err != nil {
				return summary, acquire, err
			}
		}
	}
	if !foundSummary || !foundAcquire {
		return summary, acquire, fmt.Errorf("required ZIP entries missing: summary=%t acquire=%t", foundSummary, foundAcquire)
	}
	return summary, acquire, nil
}

func decodeBoundedZipJSON(file *zip.File, value any) error {
	if file.UncompressedSize64 > 4<<20 {
		return fmt.Errorf("ZIP entry too large: %s", file.Name)
	}
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	return json.NewDecoder(io.LimitReader(reader, 4<<20)).Decode(value)
}

func validatePhase2RecoveryEvidence(summary phase2RecoverySummary, acquire UnityPhase2WorkerEvidence, request CorruptWorkerRecoveryRequest, paths Paths) error {
	if summary.Status != "FAILED" || !strings.Contains(summary.Error, "verify-worker-release-residual") || summary.Release == nil || summary.Release.CleanupMs <= 0 ||
		summary.ReleaseResidual == nil || mountedResidualStatus(*summary.ReleaseResidual) != "MOUNTED_MEASURED_ZERO" ||
		summary.SourceUnchanged == nil || !*summary.SourceUnchanged || summary.BaselineUnchanged == nil || !*summary.BaselineUnchanged ||
		summary.Parity == nil || !summary.Parity.EditModeEqual || !summary.Parity.PlayModeEqual || summary.Worker == nil ||
		summary.Worker.Clone.FallbackUsed || !summary.Worker.Clone.RegularBlockCloneIOCTLAttempted || summary.Worker.Clone.ClonedBytes <= 0 || len(summary.Worker.ChangedEntries) == 0 {
		return fmt.Errorf("Phase 2 summary does not prove the observed completed release")
	}
	for name, metadata := range map[string]WorkerMetadata{"summary": summary.Worker.Metadata, "worker-acquire": acquire.Metadata} {
		if metadata.LeaseID != request.LeaseID || metadata.KeyDigest != request.KeyDigest || !recoveryOwnershipTokenPattern.MatchString(metadata.OwnershipToken) ||
			!strings.EqualFold(metadata.WorkerPath, filepath.Join(paths.Workers, request.LeaseID)) {
			return fmt.Errorf("%s worker identity mismatch", name)
		}
	}
	if summary.Worker.Metadata.OwnershipToken != acquire.Metadata.OwnershipToken || summary.Worker.Metadata.JunctionPath != acquire.Metadata.JunctionPath {
		return fmt.Errorf("worker evidence entries disagree")
	}
	return nil
}

func openRecoveryArtifactRoot(root string) (corruptRecoveryReceipt, bool, error) {
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return corruptRecoveryReceipt{}, false, fmt.Errorf("artifact root is not a real directory")
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return corruptRecoveryReceipt{}, false, err
		}
		allowed := map[string]bool{"worker-journal.raw": true, "active-use-marker.raw": true, "recovery-receipt.json": true}
		for _, entry := range entries {
			if !allowed[entry.Name()] {
				return corruptRecoveryReceipt{}, false, fmt.Errorf("unknown recovery artifact: %s", entry.Name())
			}
		}
		data, err := os.ReadFile(filepath.Join(root, "recovery-receipt.json"))
		if err != nil {
			return corruptRecoveryReceipt{}, false, err
		}
		var receipt corruptRecoveryReceipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			return receipt, false, err
		}
		return receipt, true, nil
	} else if !os.IsNotExist(err) {
		return corruptRecoveryReceipt{}, false, err
	}
	parent := filepath.Dir(root)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return corruptRecoveryReceipt{}, false, errors.Join(err, fmt.Errorf("artifact parent must exist"))
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return corruptRecoveryReceipt{}, false, err
	}
	return corruptRecoveryReceipt{}, false, nil
}

func validateCorruptRecoveryPhase(paths Paths, request CorruptWorkerRecoveryRequest, diagnosis WorkerReleaseDiagnosis, worker WorkerMetadata, phase string) (LeaseArtifactEvidence, LeaseArtifactEvidence, Residual, error) {
	residual, residualErr := measureMountedResidual(paths)
	artifacts := residualArtifacts(residualErr)
	expectedActive, expectedJournals := 1, 1
	if phase == "journal-removed" {
		expectedJournals = 0
	} else if phase == "marker-removed" || phase == "volume-flushed" || phase == "durable-absence-verified" {
		expectedActive, expectedJournals = 0, 0
	} else if phase != "" && phase != "captured" {
		return LeaseArtifactEvidence{}, LeaseArtifactEvidence{}, residual, fmt.Errorf("unsupported recovery receipt phase: %s", phase)
	}
	if residual.ActiveBaselineUses.Count != expectedActive || residual.WorkerLeaseJournals.Count != expectedJournals || residual.WorkerDirectories.Count != 0 ||
		residual.WorkerStagingDirs.Count != 0 || residual.QuarantineEntries.Count != 0 || residual.UnknownLeaseArtifacts.Count != 0 ||
		residual.UnknownWorkerArtifacts.Count != 0 || residual.ReservationLocks.Count != 0 || residual.CoordinationArtifacts.Count != 0 || len(artifacts) != expectedJournals {
		return LeaseArtifactEvidence{}, LeaseArtifactEvidence{}, residual, fmt.Errorf("unsafe residual shape")
	}
	journal := diagnosis.LeaseArtifacts[0]
	if expectedJournals == 1 {
		journal = artifacts[0]
	}
	if journal.Name != "worker-"+request.LeaseID+".json" || journal.DecodeStatus != "FAILED" || len(diagnosis.LeaseArtifacts) != 1 || journal.SHA256 != diagnosis.LeaseArtifacts[0].SHA256 {
		return journal, LeaseArtifactEvidence{}, residual, fmt.Errorf("corrupt journal evidence changed")
	}
	markerPath := filepath.Join(paths.Leases, "active-"+request.KeyDigest+"-"+request.LeaseID+".json")
	marker := *diagnosis.MarkerArtifact
	if expectedActive == 1 {
		current, err := inspectActiveUseArtifact(markerPath)
		if err != nil || current.SHA256 != diagnosis.MarkerArtifact.SHA256 || current.KeyDigest != request.KeyDigest || current.LeaseID != request.LeaseID || !recoveryOwnershipTokenPattern.MatchString(current.OwnershipToken) {
			return journal, current, residual, errors.Join(err, fmt.Errorf("active marker evidence changed"))
		}
		marker = current
	}
	for _, path := range []string{filepath.Join(paths.Workers, request.LeaseID), worker.QuarantinePath, worker.JunctionPath} {
		if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
			return journal, marker, residual, errors.Join(err, fmt.Errorf("released path still exists: %s", path))
		}
	}
	return journal, marker, residual, nil
}

func validateRecoveryReceipt(receipt corruptRecoveryReceipt, host PoolMetadata, request CorruptWorkerRecoveryRequest, diagnosis WorkerReleaseDiagnosis) error {
	if receipt.SchemaVersion != 1 || receipt.KeyDigest != request.KeyDigest || receipt.LeaseID != request.LeaseID ||
		receipt.VHDXIdentity != host.VHDXIdentity || receipt.PoolOwnershipToken != host.OwnershipToken ||
		!strings.EqualFold(receipt.EvidenceZIPSHA256, request.EvidenceZIPSHA256) || !strings.EqualFold(receipt.DiagnosisSHA256, request.DiagnosisSHA256) ||
		len(diagnosis.LeaseArtifacts) != 1 || diagnosis.MarkerArtifact == nil || receipt.JournalSHA256 != diagnosis.LeaseArtifacts[0].SHA256 || receipt.MarkerSHA256 != diagnosis.MarkerArtifact.SHA256 {
		return fmt.Errorf("recovery receipt identity or evidence mismatch")
	}
	return nil
}

func removeExactHashedFile(path, expectedSHA string) error {
	if _, err := verifyFileSHA256(path, expectedSHA); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(err, fmt.Errorf("exact file is no longer regular"))
	}
	reparse, err := inspectPathReparse(path)
	if err != nil || reparse {
		return errors.Join(err, fmt.Errorf("exact file became a reparse point"))
	}
	return os.Remove(path)
}

func writeReceiptExclusive(path string, receipt corruptRecoveryReceipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteExclusiveDurable(path, append(data, '\n'), 0600)
}

func updateRecoveryReceipt(path string, receipt *corruptRecoveryReceipt, phase string) error {
	receipt.Phase, receipt.UpdatedAt = phase, time.Now().UTC()
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteDurable(path, append(data, '\n'), 0600)
}
