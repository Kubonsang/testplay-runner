package refsworkspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var (
	digestNamePattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	workerJournalPattern    = regexp.MustCompile(`^worker-([a-z0-9][a-z0-9-]{7,63})\.json$`)
	activeUsePattern        = regexp.MustCompile(`^active-([0-9a-f]{64})-([a-z0-9][a-z0-9-]{7,63})\.json$`)
	baselineLockPattern     = regexp.MustCompile(`^baseline-([0-9a-f]{64})\.lock$`)
	baselineCoordPattern    = regexp.MustCompile(`^baseline-([0-9a-f]{64})\.coord$`)
	baselineMutationPattern = regexp.MustCompile(`^baseline-([0-9a-f]{64})\.mutation\.json$`)
	baselineStagingPattern  = regexp.MustCompile(`^\.([0-9a-f]{64})\.staging-[A-Za-z0-9._-]+$`)
	workerStagingPattern    = regexp.MustCompile(`^\.([a-z0-9][a-z0-9-]{7,63})\.staging-[A-Za-z0-9._-]+$`)
)

type leaseArtifactCounts struct {
	active, journals, creation, reservation, coordination, mutation, unknown int
}

func measureMountedResidual(paths Paths) (Residual, error) {
	residual := Residual{}
	leaseCounts, err := classifyLeaseArtifacts(paths.Leases)
	if err != nil {
		return residual, err
	}
	residual.ActiveBaselineUses = measured(leaseCounts.active)
	residual.WorkerLeaseJournals = measured(leaseCounts.journals)
	residual.BaselineCreationLocks = measured(leaseCounts.creation)
	residual.ReservationLocks = measured(leaseCounts.reservation)
	residual.BaselineCoordinationLocks = measured(leaseCounts.coordination)
	residual.BaselineMutationMarkers = measured(leaseCounts.mutation)
	residual.CoordinationArtifacts = measured(leaseCounts.reservation + leaseCounts.coordination + leaseCounts.mutation)
	residual.UnknownLeaseArtifacts = measured(leaseCounts.unknown)

	canonicalBaselines, stagingBaselines, unknownBaselines, err := classifyBaselineEntries(paths.Baselines)
	if err != nil {
		return residual, err
	}
	_ = canonicalBaselines // canonical baselines are owned payload, not residual.
	residual.BaselineStagingDirs = measured(stagingBaselines)
	residual.UnknownBaselineEntries = measured(unknownBaselines)

	workers, stagingWorkers, unknownWorkers, err := classifyWorkerEntries(paths.Workers)
	if err != nil {
		return residual, err
	}
	residual.WorkerDirectories = measured(workers)
	residual.WorkerStagingDirs = measured(stagingWorkers)
	residual.UnknownWorkerArtifacts = measured(unknownWorkers)

	if residual.QuarantineEntries.Count, err = countEntries(paths.Quarantine, "", ""); err != nil {
		return residual, err
	}
	residual.QuarantineEntries.Measured = true
	if residual.SyntheticProbeDirectories.Count, err = countEntries(paths.PoolRoot, ".block-clone-probe-", ""); err != nil {
		return residual, err
	}
	residual.SyntheticProbeDirectories.Measured = true
	residual.Junctions.Count, err = countWorkerJunctions(paths.Leases)
	if err != nil {
		return residual, err
	}
	residual.Junctions.Measured = true
	// A running binary cannot independently prove that no peer probe process
	// exists. The outer PowerShell harness owns this measurement.
	residual.ProbeProcesses = ResidualMetric{Measured: false}
	return residual, nil
}

func completePostDetachResidual(paths Paths, residual *Residual, waitDetachedSucceeded bool) error {
	reparse, entries, err := inspectUnmountedMountPath(paths.Mount)
	if err != nil {
		return err
	}
	residual.MountReparsePoints = measured(reparse)
	residual.MountDirectoryEntries = measured(entries)
	// MountedPool.Close includes the bounded WaitDetached visibility check. This
	// field is measured only when that check returned success.
	residual.AttachedDisks = ResidualMetric{Measured: waitDetachedSucceeded, Count: 0}
	_, err = os.Lstat(paths.VHDX)
	switch {
	case err == nil:
		residual.OwnedVHDXFiles = measured(1)
	case os.IsNotExist(err):
		residual.OwnedVHDXFiles = measured(0)
	default:
		return err
	}
	residual.Status = residualStatus(*residual)
	return nil
}

func residualStatus(residual Residual) string {
	metrics := []ResidualMetric{
		residual.ActiveBaselineUses, residual.WorkerLeaseJournals, residual.WorkerDirectories,
		residual.BaselineCreationLocks, residual.BaselineStagingDirs, residual.WorkerStagingDirs,
		residual.UnknownLeaseArtifacts, residual.UnknownBaselineEntries, residual.UnknownWorkerArtifacts,
		residual.QuarantineEntries, residual.ReservationLocks, residual.BaselineCoordinationLocks,
		residual.BaselineMutationMarkers, residual.CoordinationArtifacts, residual.SyntheticProbeDirectories,
		residual.MountReparsePoints, residual.MountDirectoryEntries, residual.Junctions,
		residual.AttachedDisks, residual.ProbeProcesses,
	}
	for _, metric := range metrics {
		if metric.Count != 0 {
			return "MEASURED_NONZERO"
		}
	}
	if !residual.OwnedVHDXFiles.Measured {
		return "NOT_MEASURED"
	}
	for _, metric := range metrics {
		if !metric.Measured {
			return "NOT_MEASURED"
		}
	}
	return "MEASURED_ZERO"
}

func measured(count int) ResidualMetric { return ResidualMetric{Measured: true, Count: count} }

func classifyLeaseArtifacts(leases string) (leaseArtifactCounts, error) {
	entries, err := os.ReadDir(leases)
	if os.IsNotExist(err) {
		return leaseArtifactCounts{}, nil
	}
	if err != nil {
		return leaseArtifactCounts{}, err
	}
	var counts leaseArtifactCounts
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case entry.Type().IsRegular() && workerJournalPattern.MatchString(name):
			counts.journals++
		case entry.Type().IsRegular() && activeUsePattern.MatchString(name):
			counts.active++
		case entry.IsDir() && baselineLockPattern.MatchString(name):
			counts.creation++
		case entry.IsDir() && baselineCoordPattern.MatchString(name):
			counts.coordination++
		case entry.Type().IsRegular() && baselineMutationPattern.MatchString(name):
			counts.mutation++
		case entry.IsDir() && name == ".reservation.lock":
			counts.reservation++
		default:
			counts.unknown++
		}
	}
	return counts, nil
}

func classifyBaselineEntries(root string) (canonical, staging, unknown int, returnErr error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, 0, 0, nil
	}
	if err != nil {
		return 0, 0, 0, err
	}
	for _, entry := range entries {
		switch {
		case entry.IsDir() && digestNamePattern.MatchString(entry.Name()):
			canonical++
		case entry.IsDir() && baselineStagingPattern.MatchString(entry.Name()):
			staging++
		default:
			unknown++
		}
	}
	return canonical, staging, unknown, nil
}

func classifyWorkerEntries(root string) (workers, staging, unknown int, returnErr error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, 0, 0, nil
	}
	if err != nil {
		return 0, 0, 0, err
	}
	for _, entry := range entries {
		switch {
		case entry.IsDir() && leaseIDPattern.MatchString(entry.Name()):
			workers++
		case entry.IsDir() && workerStagingPattern.MatchString(entry.Name()):
			staging++
		default:
			unknown++
		}
	}
	return workers, staging, unknown, nil
}

func countCoordinationArtifacts(leases string) (int, error) {
	counts, err := classifyLeaseArtifacts(leases)
	return counts.reservation + counts.coordination + counts.mutation, err
}

func countWorkerJunctions(leases string) (int, error) {
	entries, err := os.ReadDir(leases)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if !workerJournalPattern.MatchString(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(leases, entry.Name()))
		if err != nil {
			return 0, err
		}
		var metadata WorkerMetadata
		if err := json.Unmarshal(data, &metadata); err != nil {
			return 0, fmt.Errorf("decode %s: %w", entry.Name(), err)
		}
		if metadata.JunctionPath == "" || metadata.JunctionRemoved {
			continue
		}
		if _, err := os.Lstat(metadata.JunctionPath); err == nil {
			count++
		} else if !os.IsNotExist(err) {
			return 0, err
		}
	}
	return count, nil
}
