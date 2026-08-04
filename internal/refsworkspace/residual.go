package refsworkspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func measureMountedResidual(paths Paths) (Residual, error) {
	residual := Residual{}
	var err error
	if residual.ActiveBaselineUses.Count, err = countEntries(paths.Leases, "active-", ".json"); err != nil {
		return residual, err
	}
	residual.ActiveBaselineUses.Measured = true
	if residual.WorkerLeaseJournals.Count, err = countEntries(paths.Leases, "worker-", ".json"); err != nil {
		return residual, err
	}
	residual.WorkerLeaseJournals.Measured = true
	if residual.WorkerDirectories.Count, err = countDirectories(paths.Workers); err != nil {
		return residual, err
	}
	residual.WorkerDirectories.Measured = true
	if residual.QuarantineEntries.Count, err = countEntries(paths.Quarantine, "", ""); err != nil {
		return residual, err
	}
	residual.QuarantineEntries.Measured = true
	if residual.CoordinationArtifacts.Count, err = countCoordinationArtifacts(paths.Leases); err != nil {
		return residual, err
	}
	residual.CoordinationArtifacts.Measured = true
	if residual.SyntheticProbeDirectories.Count, err = countEntries(paths.PoolRoot, ".block-clone-probe-", ""); err != nil {
		return residual, err
	}
	residual.SyntheticProbeDirectories.Measured = true
	residual.Junctions.Count, err = countWorkerJunctions(paths.Leases)
	if err != nil {
		return residual, err
	}
	residual.Junctions.Measured = true
	residual.ProbeProcesses = ResidualMetric{Measured: true, Count: 0}
	return residual, nil
}

func completePostDetachResidual(paths Paths, residual *Residual, attachedMeasured bool) error {
	reparse, entries, err := inspectUnmountedMountPath(paths.Mount)
	if err != nil {
		return err
	}
	residual.MountReparsePoints = ResidualMetric{Measured: true, Count: reparse}
	residual.MountDirectoryEntries = ResidualMetric{Measured: true, Count: entries}
	residual.AttachedDisks = ResidualMetric{Measured: attachedMeasured, Count: 0}
	_, err = os.Lstat(paths.VHDX)
	switch {
	case err == nil:
		residual.OwnedVHDXFiles = ResidualMetric{Measured: true, Count: 1}
	case os.IsNotExist(err):
		residual.OwnedVHDXFiles = ResidualMetric{Measured: true, Count: 0}
	default:
		return err
	}
	residual.Status = residualStatus(*residual)
	return nil
}

func residualStatus(residual Residual) string {
	metrics := []ResidualMetric{residual.ActiveBaselineUses, residual.WorkerLeaseJournals, residual.WorkerDirectories, residual.QuarantineEntries, residual.CoordinationArtifacts, residual.SyntheticProbeDirectories, residual.MountReparsePoints, residual.MountDirectoryEntries, residual.Junctions, residual.AttachedDisks, residual.ProbeProcesses, residual.OwnedVHDXFiles}
	for _, metric := range metrics {
		if !metric.Measured {
			return "NOT_MEASURED"
		}
	}
	for index, metric := range metrics {
		if index == len(metrics)-1 {
			continue
		}
		if metric.Count != 0 {
			return "MEASURED_NONZERO"
		}
	}
	return "MEASURED_ZERO"
}

func countCoordinationArtifacts(leases string) (int, error) {
	entries, err := os.ReadDir(leases)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		name := entry.Name()
		if name == ".reservation.lock" || strings.HasSuffix(name, ".coord") || strings.HasSuffix(name, ".mutation.json") {
			count++
		}
	}
	return count, nil
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
		if !strings.HasPrefix(entry.Name(), "worker-") || !strings.HasSuffix(entry.Name(), ".json") {
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
