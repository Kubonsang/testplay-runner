package refsworkspace

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
)

type corruptRecoveryFixture struct {
	native      *fakePoolNative
	service     *Service
	config      Config
	paths       Paths
	key         CompatibilityKey
	leaseID     string
	journalPath string
	markerPath  string
	worker      UnityPhase2WorkerEvidence
}

func newCorruptRecoveryFixture(t *testing.T) corruptRecoveryFixture {
	t.Helper()
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	service.runningProcesses = func([]string) ([]string, error) { return nil, nil }
	service.inspectUnmounted = func(string) (int, int, error) { return 0, 0, nil }
	config := Config{Root: filepath.Join(t.TempDir(), "storage")}
	setup, err := service.Setup(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	paths := setup.Paths
	t.Cleanup(func() { _ = makeWritableTree(paths.Baselines) })
	key := testCompatibilityKey("d")
	store := NewLibraryBaselineStore(paths)
	if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("x", 8193)), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	leaseID := "lease-corrupt1"
	if _, err := store.AcquireUse(context.Background(), key, leaseID); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(paths.Leases, "active-"+key.Digest+"-"+leaseID+".json")
	journalPath := filepath.Join(paths.Leases, "worker-"+leaseID+".json")
	if err := os.WriteFile(journalPath, append([]byte{0, 0, 0}, []byte("torn")...), 0600); err != nil {
		t.Fatal(err)
	}
	metadata := WorkerMetadata{
		SchemaVersion: LeaseSchemaVersion, LeaseID: leaseID, KeyDigest: key.Digest, State: LeaseReady,
		PID: 1234, OwnershipToken: strings.Repeat("a", 64), WorkerPath: filepath.Join(paths.Workers, leaseID),
		JunctionPath: filepath.Join(t.TempDir(), "workspace", "Library"), ReservedBytes: DefaultReserveBytes,
		QuarantinePath: filepath.Join(paths.Quarantine, "worker-"+leaseID+"-aaaaaaaaaaaa"),
	}
	return corruptRecoveryFixture{
		native: native, service: service, config: config, paths: paths, key: key, leaseID: leaseID,
		journalPath: journalPath, markerPath: markerPath,
		worker: UnityPhase2WorkerEvidence{
			LeaseID: leaseID, Metadata: metadata, Metrics: WorkerMetrics{CleanupMs: 1}, JunctionVerified: true,
			Clone: CloneMetrics{ClonedBytes: 4096, RegularBlockCloneIOCTLAttempted: true}, ChangedEntries: []string{"ArtifactDB"},
		},
	}
}

func TestDiagnoseCorruptReleasedWorkerResidualReadOnly(t *testing.T) {
	fixture := newCorruptRecoveryFixture(t)
	diagnosis, err := fixture.service.DiagnoseReleasedWorkerResidual(context.Background(), fixture.config, fixture.key.Digest, fixture.leaseID)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.native.readOnlyMounts != 1 || !diagnosis.ReadOnlyAttach || diagnosis.Classification != ReleaseResidualCorruptJournal || diagnosis.CleanupState != "released" {
		t.Fatalf("diagnosis=%+v readOnly=%d", diagnosis, fixture.native.readOnlyMounts)
	}
	if len(diagnosis.LeaseArtifacts) != 1 || diagnosis.LeaseArtifacts[0].DecodeStatus != "FAILED" || diagnosis.MarkerArtifact == nil || diagnosis.MarkerArtifact.DecodeStatus != "PASS" {
		t.Fatalf("diagnosis=%+v", diagnosis)
	}
	for _, path := range []string{fixture.journalPath, fixture.markerPath, fixture.paths.VHDX} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("diagnosis modified %s: %v", path, err)
		}
	}
}

func TestRecoverCorruptReleasedWorkerResidualUsesPinnedEvidence(t *testing.T) {
	fixture := newCorruptRecoveryFixture(t)
	diagnosis, err := fixture.service.DiagnoseReleasedWorkerResidual(context.Background(), fixture.config, fixture.key.Digest, fixture.leaseID)
	if err != nil {
		t.Fatal(err)
	}
	evidenceRoot := t.TempDir()
	diagnosisPath := filepath.Join(evidenceRoot, "diagnosis.json")
	writeTestJSON(t, diagnosisPath, diagnosis)
	zipPath := filepath.Join(evidenceRoot, "phase2.zip")
	writeRecoveryEvidenceZIP(t, zipPath, fixture.worker)
	request := CorruptWorkerRecoveryRequest{
		KeyDigest: fixture.key.Digest, LeaseID: fixture.leaseID,
		EvidenceZIP: zipPath, EvidenceZIPSHA256: testFileHash(t, zipPath),
		DiagnosisPath: diagnosisPath, DiagnosisSHA256: testFileHash(t, diagnosisPath),
		ArtifactRoot: filepath.Join(evidenceRoot, "recovery"),
	}
	// Setup closes twice, diagnosis once, recovery once, Status once, Remove once.
	fixture.native.emptyMountOnCloseAt = map[int]bool{6: true}
	fixture.native.afterClose = func(count int, _ string) {
		if count == 6 {
			_ = makeWritableTree(fixture.paths.PoolRoot)
		}
	}
	result, err := fixture.service.RecoverCorruptReleasedWorkerResidual(context.Background(), fixture.config, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "RECOVERED" || result.CorruptWorkerRecovery == nil || !result.CorruptWorkerRecovery.PoolRemoved || !result.CorruptWorkerRecovery.DurableAbsenceVerified {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{fixture.paths.VHDX, fixture.paths.Owner, fixture.paths.Root} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("pool residual %s: %v", path, err)
		}
	}
	for _, name := range []string{"worker-journal.raw", "active-use-marker.raw", "recovery-receipt.json"} {
		if _, err := os.Lstat(filepath.Join(request.ArtifactRoot, name)); err != nil {
			t.Fatalf("missing recovery artifact %s: %v", name, err)
		}
	}
}

func TestRecoverCorruptReleasedWorkerResidualRejectsHashMismatchWithoutDeletion(t *testing.T) {
	fixture := newCorruptRecoveryFixture(t)
	request := CorruptWorkerRecoveryRequest{
		KeyDigest: fixture.key.Digest, LeaseID: fixture.leaseID,
		EvidenceZIP: filepath.Join(t.TempDir(), "missing.zip"), EvidenceZIPSHA256: strings.Repeat("0", 64),
		DiagnosisPath: filepath.Join(t.TempDir(), "missing.json"), DiagnosisSHA256: strings.Repeat("0", 64),
		ArtifactRoot: filepath.Join(t.TempDir(), "recovery"),
	}
	if _, err := fixture.service.RecoverCorruptReleasedWorkerResidual(context.Background(), fixture.config, request); err == nil {
		t.Fatal("hash mismatch was accepted")
	}
	for _, path := range []string{fixture.journalPath, fixture.markerPath, fixture.paths.VHDX} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("rejected recovery modified %s: %v", path, err)
		}
	}
}

func TestRecoverCorruptReleasedWorkerResidualResumesExactReceiptPhase(t *testing.T) {
	fixture := newCorruptRecoveryFixture(t)
	diagnosis, err := fixture.service.DiagnoseReleasedWorkerResidual(context.Background(), fixture.config, fixture.key.Digest, fixture.leaseID)
	if err != nil {
		t.Fatal(err)
	}
	evidenceRoot := t.TempDir()
	diagnosisPath := filepath.Join(evidenceRoot, "diagnosis.json")
	writeTestJSON(t, diagnosisPath, diagnosis)
	zipPath := filepath.Join(evidenceRoot, "phase2.zip")
	writeRecoveryEvidenceZIP(t, zipPath, fixture.worker)
	artifactRoot := filepath.Join(evidenceRoot, "recovery")
	if err := os.Mkdir(artifactRoot, 0700); err != nil {
		t.Fatal(err)
	}
	journalData, _ := os.ReadFile(fixture.journalPath)
	markerData, _ := os.ReadFile(fixture.markerPath)
	if err := atomicfile.WriteExclusiveDurable(filepath.Join(artifactRoot, "worker-journal.raw"), journalData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.WriteExclusiveDurable(filepath.Join(artifactRoot, "active-use-marker.raw"), markerData, 0600); err != nil {
		t.Fatal(err)
	}
	receipt := corruptRecoveryReceipt{
		SchemaVersion: 1, Phase: "journal-removed", KeyDigest: fixture.key.Digest, LeaseID: fixture.leaseID,
		VHDXIdentity: diagnosis.HostOwner.VHDXIdentity, PoolOwnershipToken: diagnosis.HostOwner.OwnershipToken,
		JournalPath: fixture.journalPath, JournalSHA256: diagnosis.LeaseArtifacts[0].SHA256,
		MarkerPath: fixture.markerPath, MarkerSHA256: diagnosis.MarkerArtifact.SHA256,
		EvidenceZIPSHA256: testFileHash(t, zipPath), DiagnosisSHA256: testFileHash(t, diagnosisPath),
	}
	if err := writeReceiptExclusive(filepath.Join(artifactRoot, "recovery-receipt.json"), receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.journalPath); err != nil {
		t.Fatal(err)
	}
	request := CorruptWorkerRecoveryRequest{
		KeyDigest: fixture.key.Digest, LeaseID: fixture.leaseID,
		EvidenceZIP: zipPath, EvidenceZIPSHA256: receipt.EvidenceZIPSHA256,
		DiagnosisPath: diagnosisPath, DiagnosisSHA256: receipt.DiagnosisSHA256, ArtifactRoot: artifactRoot,
	}
	fixture.native.emptyMountOnCloseAt = map[int]bool{6: true}
	fixture.native.afterClose = func(count int, _ string) {
		if count == 6 {
			_ = makeWritableTree(fixture.paths.PoolRoot)
		}
	}
	result, err := fixture.service.RecoverCorruptReleasedWorkerResidual(context.Background(), fixture.config, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.CorruptWorkerRecovery == nil || !result.CorruptWorkerRecovery.MarkerRemoved || !result.CorruptWorkerRecovery.PoolRemoved {
		t.Fatalf("result=%+v", result)
	}
}

func writeRecoveryEvidenceZIP(t *testing.T, path string, worker UnityPhase2WorkerEvidence) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	zero := fullyMeasuredResidual()
	zero.Status = ""
	trueValue := true
	summary := phase2RecoverySummary{
		Status: "FAILED", Error: "cleanup-failed: verify-worker-release-residual: status=; baseline-in-use",
		Worker: &worker, Release: &WorkerMetrics{CleanupMs: 144}, ReleaseResidual: &zero,
		SourceUnchanged: &trueValue, BaselineUnchanged: &trueValue,
		Parity: &UnityPhase2Parity{EditModeEqual: true, PlayModeEqual: true, ExactTestSets: true},
	}
	for name, value := range map[string]any{"summary.json": summary, "worker-acquire.json": worker} {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(entry).Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func testFileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
