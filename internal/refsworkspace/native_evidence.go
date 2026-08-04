package refsworkspace

func boolPointer(value bool) *bool       { return &value }
func int64Pointer(value int64) *int64    { return &value }
func stringPointer(value string) *string { return &value }

func newPostMountNativeEvidence(devDrive DevDriveEvidence, volume VolumeInfo) *NativeEvidence {
	evidence := &NativeEvidence{
		WindowsProvider:        WindowsProviderDevDriveVHDX,
		VolumeKind:             VolumeKindDevDrive,
		DevDrive:               &devDrive,
		Filesystem:             stringPointer(volume.Filesystem),
		ClusterSize:            int64Pointer(volume.ClusterSize),
		BlockCloneSupported:    boolPointer(volume.SupportsBlockCloning),
		LastCompletedMilestone: "private-mount-validation",
		Milestones: NativeMilestones{
			DevDriveFormat:         NativeMilestoneNotAttempted,
			PrivateMount:           NativeMilestoneMeasuredFail,
			MountedPoolReadiness:   NativeMilestoneNotMeasured,
			FilesystemValidation:   NativeMilestoneNotMeasured,
			BlockCloneCapability:   NativeMilestoneNotMeasured,
			RegularBlockCloneIOCTL: NativeMilestoneNotAttempted,
			SparseBlockCloneIOCTL:  NativeMilestoneNotAttempted,
			CoWIsolation:           NativeMilestoneNotMeasured,
			Cleanup:                NativeMilestoneNotMeasured,
		},
	}
	if devDrive.FormatAttempted {
		evidence.Milestones.DevDriveFormat = NativeMilestoneMeasuredFail
		if devDrive.FormatSucceeded && devDrive.QueryExitCode == 0 && devDrive.TemporaryDriveLetterAssigned && devDrive.TemporaryDriveLetterRemoved {
			evidence.Milestones.DevDriveFormat = NativeMilestoneMeasuredPass
		}
	}
	if devDrive.PrivateMountVerified {
		evidence.Milestones.PrivateMount = NativeMilestoneMeasuredPass
	}
	return evidence
}

func (evidence *NativeEvidence) recordMountedPoolReadiness(ready bool) {
	if evidence == nil {
		return
	}
	evidence.Milestones.MountedPoolReadiness = NativeMilestoneMeasuredFail
	if ready {
		evidence.Milestones.MountedPoolReadiness = NativeMilestoneMeasuredPass
		evidence.LastCompletedMilestone = "mounted-pool-readiness"
	}
}

func (evidence *NativeEvidence) recordVolumeCapabilityValidation(volume VolumeInfo) {
	if evidence == nil {
		return
	}
	evidence.Filesystem = stringPointer(volume.Filesystem)
	evidence.ClusterSize = int64Pointer(volume.ClusterSize)
	evidence.BlockCloneSupported = boolPointer(volume.SupportsBlockCloning)
	evidence.Milestones.FilesystemValidation = NativeMilestoneMeasuredPass
	evidence.Milestones.BlockCloneCapability = NativeMilestoneMeasuredPass
	evidence.RegularBlockCloneIOCTLAttempted = boolPointer(false)
	evidence.SparseBlockCloneIOCTLAttempted = boolPointer(false)
	evidence.LastCompletedMilestone = "volume-capability-validation"
}

func (evidence *NativeEvidence) recordCloneMetrics(metrics CloneMetrics, cloneErr error) {
	if evidence == nil {
		return
	}
	evidence.RegularBlockCloneIOCTLAttempted = boolPointer(metrics.RegularBlockCloneIOCTLAttempted)
	evidence.SparseBlockCloneIOCTLAttempted = boolPointer(metrics.SparseBlockCloneIOCTLAttempted)
	if metrics.RegularBlockCloneIOCTLAttempted {
		evidence.Milestones.RegularBlockCloneIOCTL = NativeMilestoneMeasuredFail
		if metrics.ClonedBytes > 0 {
			evidence.Milestones.RegularBlockCloneIOCTL = NativeMilestoneMeasuredPass
			evidence.LastCompletedMilestone = "regular-block-clone"
		}
	} else {
		evidence.Milestones.RegularBlockCloneIOCTL = NativeMilestoneNotAttempted
	}
	if metrics.SparseBlockCloneIOCTLAttempted {
		evidence.Milestones.SparseBlockCloneIOCTL = NativeMilestoneMeasuredFail
		if metrics.SparseClonedBytes > 0 {
			evidence.Milestones.SparseBlockCloneIOCTL = NativeMilestoneMeasuredPass
			evidence.LastCompletedMilestone = "sparse-block-clone"
		}
	} else {
		evidence.Milestones.SparseBlockCloneIOCTL = NativeMilestoneNotAttempted
	}
	if cloneErr == nil {
		evidence.Milestones.CoWIsolation = NativeMilestoneMeasuredPass
		evidence.LastCompletedMilestone = "cow-isolation"
	}
}

func (evidence *NativeEvidence) recordCleanup(state string) {
	if evidence == nil {
		return
	}
	switch state {
	case "released", "preserved":
		evidence.Milestones.Cleanup = NativeMilestoneReleased
	default:
		evidence.Milestones.Cleanup = NativeMilestoneUncertain
	}
}
