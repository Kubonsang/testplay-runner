package main

import (
	"errors"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/refsworkspace"
)

func TestErrorPayloadPreservesPostMountNativeEvidence(t *testing.T) {
	regularAttempted, sparseAttempted := false, false
	filesystem, clusterSize, cloneSupported := "ReFS", int64(4096), true
	evidence := &refsworkspace.NativeEvidence{
		WindowsProvider: refsworkspace.WindowsProviderDevDriveVHDX,
		VolumeKind:      refsworkspace.VolumeKindDevDrive,
		DevDrive: &refsworkspace.DevDriveEvidence{
			FormatAttempted: true, FormatSucceeded: true, QueryExitCode: 0,
			QueryOutput: "raw fsutil output", TemporaryDriveLetterAssigned: true,
			TemporaryDriveLetterRemoved: true, PrivateMountVerified: true,
		},
		Filesystem: &filesystem, ClusterSize: &clusterSize, BlockCloneSupported: &cloneSupported,
		LastCompletedMilestone:          "volume-capability-validation",
		RegularBlockCloneIOCTLAttempted: &regularAttempted,
		SparseBlockCloneIOCTLAttempted:  &sparseAttempted,
		Milestones: refsworkspace.NativeMilestones{
			DevDriveFormat:         refsworkspace.NativeMilestoneMeasuredPass,
			PrivateMount:           refsworkspace.NativeMilestoneMeasuredPass,
			FilesystemValidation:   refsworkspace.NativeMilestoneMeasuredPass,
			BlockCloneCapability:   refsworkspace.NativeMilestoneMeasuredPass,
			RegularBlockCloneIOCTL: refsworkspace.NativeMilestoneNotAttempted,
			SparseBlockCloneIOCTL:  refsworkspace.NativeMilestoneNotAttempted,
			CoWIsolation:           refsworkspace.NativeMilestoneNotMeasured,
			Cleanup:                refsworkspace.NativeMilestoneReleased,
		},
	}
	err := &refsworkspace.Error{
		Code: refsworkspace.CodeCloneFailed, Operation: "validate-clone-source",
		Path: `C:\original\source`, Cause: errors.New("injected"), CleanupState: "released",
		NativeEvidence: evidence,
	}
	payload := errorPayload(err)
	if payload["path"] != err.Path || payload["nativeWindowsStatus"] != "PARTIALLY_MEASURED" || payload["lastCompletedMilestone"] != evidence.LastCompletedMilestone {
		t.Fatalf("payload=%+v", payload)
	}
	devDrive, ok := payload["devDrive"].(*refsworkspace.DevDriveEvidence)
	if !ok || devDrive.QueryOutput != "raw fsutil output" {
		t.Fatalf("devDrive=%T %+v", payload["devDrive"], payload["devDrive"])
	}
	if attempted, ok := payload["regularBlockCloneIOCTLAttempted"].(*bool); !ok || *attempted {
		t.Fatalf("regular attempted=%T %+v", payload["regularBlockCloneIOCTLAttempted"], payload["regularBlockCloneIOCTLAttempted"])
	}
}

func TestErrorPayloadDoesNotInventPreMountEvidence(t *testing.T) {
	err := &refsworkspace.Error{Code: refsworkspace.CodeDevDriveUnavailable, Operation: "native-check"}
	payload := errorPayload(err)
	if payload["nativeWindowsStatus"] != "NOT MEASURED" {
		t.Fatalf("payload=%+v", payload)
	}
	if _, exists := payload["nativeEvidence"]; exists {
		t.Fatalf("pre-mount native evidence was invented: %+v", payload)
	}
}

func TestErrorPayloadPreservesSetupTransactionEvidence(t *testing.T) {
	transaction := &refsworkspace.SetupTransactionEvidence{
		PendingOwnerCreated: true, VHDXCreated: true, PoolMetadataWritten: true,
		PoolMetadataFlushed: true, PoolMetadataReadBack: true,
		InitialMount:       refsworkspace.SetupMountCycleEvidence{Attempted: true, Mounted: true, Detached: true},
		DurabilityReattach: refsworkspace.SetupMountCycleEvidence{Attempted: true, Mounted: true, MetadataVisible: false, Detached: true},
	}
	err := &refsworkspace.Error{
		Code:      refsworkspace.CodePoolPersistenceVerificationFailed,
		Operation: "verify-persistent-pool-after-reattach", Path: `C:\storage\mount\testplay\pool.json`,
		CleanupState: "released", SetupTransaction: transaction,
	}
	payload := errorPayload(err)
	if payload["setupTransaction"] != transaction {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestErrorPayloadIncludesMountedPoolReadinessAndPreservationEvidence(t *testing.T) {
	err := &refsworkspace.Error{
		Code: refsworkspace.CodePoolMountNotReady, Operation: "wait-mounted-pool-metadata",
		Path: `C:\storage\mount\testplay\pool.json`, Cause: errors.New("file not found"),
		CleanupState: "preserved", OwnerMetadataCommitted: true, OwnedVHDXPath: `C:\storage\pool.vhdx`,
		MountPath: `C:\storage\mount`, PoolMetadataPath: `C:\storage\mount\testplay\pool.json`,
		MountReadyTimeoutMs: 20000, LastObservedError: "The system cannot find the file specified.",
	}
	payload := errorPayload(err)
	if payload["cleanupState"] != "preserved" || payload["ownerMetadataCommitted"] != true || payload["manualRecoveryRequired"] != false || payload["ownedVhdxPath"] != err.OwnedVHDXPath {
		t.Fatalf("cleanup payload=%+v", payload)
	}
	if payload["mountPath"] != err.MountPath || payload["poolMetadataPath"] != err.PoolMetadataPath || payload["mountReadyTimeoutMs"] != int64(20000) || payload["lastObservedFilesystemError"] != err.LastObservedError {
		t.Fatalf("readiness payload=%+v", payload)
	}
}

func TestErrorPayloadPreservesCorruptJournalResidualEvidence(t *testing.T) {
	residual := refsworkspace.Residual{
		Status: "NOT_MEASURED",
		WorkerLeaseJournals: refsworkspace.ResidualMetric{Measured: true, Count: 1},
		Junctions: refsworkspace.ResidualMetric{Measured: false},
	}
	artifacts := []refsworkspace.LeaseArtifactEvidence{{
		Name: "worker-lease-corrupt1.json", Kind: "worker-journal", DecodeStatus: "FAILED",
		SHA256: "abc", LeadingBytesHex: "00000000", DecodeError: "invalid character NUL",
	}}
	err := &refsworkspace.Error{
		Code: refsworkspace.CodeCleanupFailed, Operation: "measure-mounted-residual",
		ResidualEvidence: &residual, LeaseArtifacts: artifacts,
	}
	payload := errorPayload(err)
	if payload["residualEvidence"] != &residual {
		t.Fatalf("payload=%+v", payload)
	}
	actual, ok := payload["leaseArtifacts"].([]refsworkspace.LeaseArtifactEvidence)
	if !ok || len(actual) != 1 || actual[0].DecodeStatus != "FAILED" {
		t.Fatalf("artifacts=%T %+v", payload["leaseArtifacts"], payload["leaseArtifacts"])
	}
}
