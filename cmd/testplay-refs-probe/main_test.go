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
