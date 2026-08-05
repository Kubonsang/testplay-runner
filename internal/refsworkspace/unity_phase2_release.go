package refsworkspace

import (
	"errors"
	"fmt"
)

type WorkerReleaseDurabilityEvidence struct {
	ReleaseAttempted         bool      `json:"releaseAttempted"`
	ReleaseSucceeded         bool      `json:"releaseSucceeded"`
	FlushAttempted           bool      `json:"flushAttempted"`
	FlushSucceeded           bool      `json:"flushSucceeded"`
	DetachAttempted          bool      `json:"detachAttempted"`
	DetachSucceeded          bool      `json:"detachSucceeded"`
	DurableReattachAttempted bool      `json:"durableReattachAttempted"`
	DurableReattachSucceeded bool      `json:"durableReattachSucceeded"`
	DurableResidual          *Residual `json:"durableResidual,omitempty"`
	PoolRemoveAttempted      bool      `json:"poolRemoveAttempted"`
	PoolRemoveSucceeded      bool      `json:"poolRemoveSucceeded"`
}

type workerReleaseArtifact struct {
	Metrics         WorkerMetrics                   `json:"metrics"`
	MountedResidual Residual                        `json:"mountedResidual"`
	Durability      WorkerReleaseDurabilityEvidence `json:"durability"`
}

type workerReleaseDurabilityResult struct {
	Metrics         WorkerMetrics
	MountedResidual Residual
	Durability      WorkerReleaseDurabilityEvidence
	PoolStatus      *Result
	PoolRemove      *Result
}

type workerReleaseDurabilityOps struct {
	release       func() (WorkerMetrics, error)
	measure       func() (Residual, error)
	writeArtifact func(workerReleaseArtifact) error
	flush         func() error
	beforeDetach  func() error
	detach        func() error
	status        func() (*Result, error)
	remove        func() (*Result, error)
}

func runWorkerReleaseDurability(ops workerReleaseDurabilityOps) (workerReleaseDurabilityResult, error) {
	var result workerReleaseDurabilityResult
	result.Durability.ReleaseAttempted = true
	releaseMetrics, releaseErr := ops.release()
	result.Metrics = releaseMetrics
	result.Durability.ReleaseSucceeded = releaseErr == nil

	mountedResidual, residualErr := ops.measure()
	result.MountedResidual = mountedResidual
	artifact := func() workerReleaseArtifact {
		return workerReleaseArtifact{Metrics: result.Metrics, MountedResidual: result.MountedResidual, Durability: result.Durability}
	}
	firstWriteErr := ops.writeArtifact(artifact())

	result.Durability.FlushAttempted = true
	flushErr := ops.flush()
	result.Durability.FlushSucceeded = flushErr == nil
	secondWriteErr := ops.writeArtifact(artifact())
	if err := errors.Join(releaseErr, residualErr, firstWriteErr, flushErr, secondWriteErr); err != nil {
		return result, err
	}
	if err := validateMountedReleaseResidual(result.MountedResidual); err != nil {
		return result, newError(CodeCleanupFailed, "verify-worker-release-mounted-residual", "", err)
	}
	if ops.beforeDetach != nil {
		if err := ops.beforeDetach(); err != nil {
			return result, err
		}
	}

	result.Durability.DetachAttempted = true
	detachErr := ops.detach()
	result.Durability.DetachSucceeded = detachErr == nil
	detachWriteErr := ops.writeArtifact(artifact())
	if err := errors.Join(detachErr, detachWriteErr); err != nil {
		return result, err
	}

	result.Durability.DurableReattachAttempted = true
	status, statusErr := ops.status()
	result.PoolStatus = status
	if status != nil {
		residual := status.Residual
		result.Durability.DurableResidual = &residual
	}
	result.Durability.DurableReattachSucceeded = statusErr == nil
	statusWriteErr := ops.writeArtifact(artifact())
	if err := errors.Join(statusErr, statusWriteErr); err != nil {
		return result, err
	}
	if err := validateDurableReleaseStatus(status); err != nil {
		return result, newError(CodeWorkerReleasePersistenceFailed, "verify-worker-release-after-reattach", status.Paths.PoolRoot, err)
	}

	result.Durability.PoolRemoveAttempted = true
	removed, removeErr := ops.remove()
	result.PoolRemove = removed
	result.Durability.PoolRemoveSucceeded = removeErr == nil
	removeWriteErr := ops.writeArtifact(artifact())
	return result, errors.Join(removeErr, removeWriteErr)
}

func validateDurableReleaseStatus(status *Result) error {
	if status == nil {
		return fmt.Errorf("status result is nil")
	}
	if status.Status != "READY" {
		return fmt.Errorf("pool status=%s", status.Status)
	}
	residual := status.Residual
	if residual.Status != "MEASURED_ZERO" {
		return fmt.Errorf("residual status=%s", residual.Status)
	}
	for name, metric := range map[string]ResidualMetric{
		"active baseline uses":  residual.ActiveBaselineUses,
		"worker lease journals": residual.WorkerLeaseJournals,
		"worker directories":    residual.WorkerDirectories,
		"worker staging":        residual.WorkerStagingDirs,
		"quarantine":            residual.QuarantineEntries,
		"reservations":          residual.ReservationLocks,
		"coordination":          residual.CoordinationArtifacts,
		"junctions":             residual.Junctions,
		"unknown lease":         residual.UnknownLeaseArtifacts,
		"unknown worker":        residual.UnknownWorkerArtifacts,
	} {
		if !metric.Measured || metric.Count != 0 {
			return fmt.Errorf("%s measured=%t count=%d", name, metric.Measured, metric.Count)
		}
	}
	if !residual.OwnedVHDXFiles.Measured || residual.OwnedVHDXFiles.Count != 1 {
		return fmt.Errorf("owned VHDX measured=%t count=%d", residual.OwnedVHDXFiles.Measured, residual.OwnedVHDXFiles.Count)
	}
	return nil
}
