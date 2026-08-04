package refsworkspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResidualClassifiesKnownAndUnknownArtifacts(t *testing.T) {
	paths := testPoolPaths(t)
	digest := strings.Repeat("a", 64)
	for _, name := range []string{"worker-lease-known1.json", "active-" + digest + "-lease-known1.json", "baseline-" + digest + ".mutation.json"} {
		if err := os.WriteFile(filepath.Join(paths.Leases, name), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"baseline-" + digest + ".lock", "baseline-" + digest + ".coord", ".reservation.lock"} {
		if err := os.Mkdir(filepath.Join(paths.Leases, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(paths.Leases, "foreign.tmp"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(paths.Baselines, digest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(paths.Baselines, "."+digest+".staging-token"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Baselines, "foreign"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(paths.Workers, "lease-known1"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(paths.Workers, ".lease-known2.staging-token"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Workers, "foreign"), nil, 0600); err != nil {
		t.Fatal(err)
	}

	residual, err := measureMountedResidual(paths)
	if err != nil {
		t.Fatal(err)
	}
	for name, metric := range map[string]ResidualMetric{
		"active": residual.ActiveBaselineUses, "journal": residual.WorkerLeaseJournals,
		"creation": residual.BaselineCreationLocks, "coord": residual.BaselineCoordinationLocks,
		"mutation": residual.BaselineMutationMarkers, "reservation": residual.ReservationLocks,
		"baseline staging": residual.BaselineStagingDirs, "worker staging": residual.WorkerStagingDirs,
		"worker": residual.WorkerDirectories, "unknown lease": residual.UnknownLeaseArtifacts,
		"unknown baseline": residual.UnknownBaselineEntries, "unknown worker": residual.UnknownWorkerArtifacts,
	} {
		if !metric.Measured || metric.Count != 1 {
			t.Fatalf("%s=%+v residual=%+v", name, metric, residual)
		}
	}
	if residual.ProbeProcesses.Measured {
		t.Fatalf("binary process evidence was fabricated: %+v", residual.ProbeProcesses)
	}
}

func TestResidualKnownCanonicalEntriesAreNotUnknown(t *testing.T) {
	paths := testPoolPaths(t)
	digest := strings.Repeat("b", 64)
	if err := os.Mkdir(filepath.Join(paths.Baselines, digest), 0700); err != nil {
		t.Fatal(err)
	}
	residual, err := measureMountedResidual(paths)
	if err != nil {
		t.Fatal(err)
	}
	if residual.UnknownLeaseArtifacts.Count != 0 || residual.UnknownBaselineEntries.Count != 0 || residual.UnknownWorkerArtifacts.Count != 0 {
		t.Fatalf("residual=%+v", residual)
	}
}

func TestResidualStatusZeroNonzeroAndUnmeasured(t *testing.T) {
	zero := fullyMeasuredResidual()
	if got := residualStatus(zero); got != "MEASURED_ZERO" {
		t.Fatalf("zero=%s", got)
	}
	nonzero := zero
	nonzero.BaselineStagingDirs.Count = 1
	if got := residualStatus(nonzero); got != "MEASURED_NONZERO" {
		t.Fatalf("nonzero=%s", got)
	}
	unmeasured := zero
	unmeasured.ProbeProcesses.Measured = false
	if got := residualStatus(unmeasured); got != "NOT_MEASURED" {
		t.Fatalf("unmeasured=%s", got)
	}
	unmeasured.UnknownLeaseArtifacts.Count = 1
	if got := residualStatus(unmeasured); got != "MEASURED_NONZERO" {
		t.Fatalf("nonzero must win over unmeasured: %s", got)
	}
	ownedUnmeasured := zero
	ownedUnmeasured.OwnedVHDXFiles.Measured = false
	if got := residualStatus(ownedUnmeasured); got != "NOT_MEASURED" {
		t.Fatalf("owned VHDX unmeasured=%s", got)
	}
}

func fullyMeasuredResidual() Residual {
	zero := measured(0)
	return Residual{
		ActiveBaselineUses: zero, WorkerLeaseJournals: zero, WorkerDirectories: zero,
		BaselineCreationLocks: zero, BaselineStagingDirs: zero, WorkerStagingDirs: zero,
		UnknownLeaseArtifacts: zero, UnknownBaselineEntries: zero, UnknownWorkerArtifacts: zero,
		QuarantineEntries: zero, ReservationLocks: zero, BaselineCoordinationLocks: zero,
		BaselineMutationMarkers: zero, CoordinationArtifacts: zero, SyntheticProbeDirectories: zero,
		MountReparsePoints: zero, MountDirectoryEntries: zero, Junctions: zero,
		AttachedDisks: zero, ProbeProcesses: zero, OwnedVHDXFiles: zero,
	}
}
