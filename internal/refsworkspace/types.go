package refsworkspace

import "time"

const (
	PoolSchemaVersion               = 2
	BaselineSchemaVersion           = 2
	LeaseSchemaVersion              = 2
	DefaultMaximumBytes             = int64(64 << 30)
	DefaultSoftBudget               = int64(14 << 30)
	DefaultReserveBytes             = int64(2 << 30)
	DefaultMinimumHostFreeBytes     = int64(30 << 30)
	DefaultVHDXOverheadReserveBytes = int64(2 << 30)
	MinimumDevDriveVHDXBytes        = int64(50 << 30)
	WindowsProviderDevDriveVHDX     = "dev-drive-vhdx"
	VolumeKindDevDrive              = "Dev Drive"
)

type Config struct {
	Root                     string `json:"root"`
	VHDXPath                 string `json:"vhdxPath,omitempty"`
	MountRoot                string `json:"mountRoot,omitempty"`
	MaximumBytes             int64  `json:"maximumBytes"`
	SoftBudgetBytes          int64  `json:"softBudgetBytes"`
	WorkerReserveBytes       int64  `json:"workerReserveBytes"`
	MinimumHostFreeBytes     int64  `json:"minimumHostFreeBytes"`
	VHDXOverheadReserveBytes int64  `json:"vhdxOverheadReserveBytes"`
}

type Paths struct {
	Root         string `json:"root"`
	VHDX         string `json:"vhdx"`
	Owner        string `json:"owner"`
	PendingOwner string `json:"pendingOwner"`
	Mount        string `json:"mount"`
	PoolRoot     string `json:"poolRoot"`
	PoolFile     string `json:"poolFile"`
	Baselines    string `json:"baselines"`
	Workers      string `json:"workers"`
	Leases       string `json:"leases"`
	Quarantine   string `json:"quarantine"`
}

type VolumeInfo struct {
	VolumeGUIDPath       string `json:"volumeGuidPath"`
	Filesystem           string `json:"filesystem"`
	ClusterSize          int64  `json:"clusterSize"`
	TotalBytes           int64  `json:"totalBytes,omitempty"`
	FreeBytes            int64  `json:"freeBytes,omitempty"`
	UsedBytes            int64  `json:"usedBytes,omitempty"`
	SupportsBlockCloning bool   `json:"supportsBlockCloning"`
}

type FileUsage struct {
	LogicalBytes   int64 `json:"logicalBytes"`
	AllocatedBytes int64 `json:"allocatedBytes"`
}

type PoolMetadata struct {
	SchemaVersion            int       `json:"schemaVersion"`
	Architecture             string    `json:"architecture"`
	WindowsProvider          string    `json:"windowsProvider"`
	VolumeKind               string    `json:"volumeKind"`
	CreatedAt                time.Time `json:"createdAt"`
	OwnershipToken           string    `json:"ownershipToken"`
	VHDXPath                 string    `json:"vhdxPath"`
	VHDXIdentity             string    `json:"vhdxIdentity"`
	VolumeGUIDPath           string    `json:"volumeGuidPath"`
	Filesystem               string    `json:"filesystem"`
	ClusterSize              int64     `json:"clusterSize"`
	MaximumBytes             int64     `json:"maximumBytes"`
	SoftBudgetBytes          int64     `json:"softBudgetBytes"`
	WorkerReserveBytes       int64     `json:"workerReserveBytes"`
	MinimumHostFreeBytes     int64     `json:"minimumHostFreeBytes"`
	VHDXOverheadReserveBytes int64     `json:"vhdxOverheadReserveBytes"`
}

// SetupMountCycleEvidence keeps the first formatted mount distinct from the
// persistence-proof reattach. A false value means the milestone was measured
// and failed only when the enclosing cycle was attempted.
type SetupMountCycleEvidence struct {
	Attempted       bool               `json:"attempted"`
	Mounted         bool               `json:"mounted"`
	Metrics         NativeMountMetrics `json:"metrics"`
	DevDrive        *DevDriveEvidence  `json:"devDrive,omitempty"`
	Volume          *VolumeInfo        `json:"volume,omitempty"`
	ReadinessMs     int64              `json:"readinessMs,omitempty"`
	MetadataVisible bool               `json:"metadataVisible"`
	LayoutVerified  bool               `json:"layoutVerified"`
	Detached        bool               `json:"detached"`
}

// SetupTransactionEvidence describes the commit protocol. It is emitted on
// both successful setup and post-create failures so artifacts show exactly
// which durability boundary was reached.
type SetupTransactionEvidence struct {
	PendingOwnerCreated         bool                    `json:"pendingOwnerCreated"`
	VHDXCreated                 bool                    `json:"vhdxCreated"`
	PoolMetadataWritten         bool                    `json:"poolMetadataWritten"`
	PoolMetadataFlushed         bool                    `json:"poolMetadataFlushed"`
	PoolMetadataReadBack        bool                    `json:"poolMetadataReadBack"`
	VolumeFlushed               bool                    `json:"volumeFlushed"`
	InitialMount                SetupMountCycleEvidence `json:"initialMount"`
	DurabilityReattach          SetupMountCycleEvidence `json:"durabilityReattach"`
	VHDXIdentityRevalidated     bool                    `json:"vhdxIdentityRevalidated"`
	DurabilityVerified          bool                    `json:"durabilityVerified"`
	AuthoritativeOwnerCommitted bool                    `json:"authoritativeOwnerCommitted"`
}

// DevDriveEvidence records only structural observations made by the Windows
// provider. QueryOutput is the unmodified fsutil payload returned by Windows.
type DevDriveEvidence struct {
	FormatAttempted              bool   `json:"formatAttempted"`
	FormatSucceeded              bool   `json:"formatSucceeded"`
	QueryExitCode                int    `json:"queryExitCode"`
	QueryOutput                  string `json:"queryOutput"`
	TemporaryDriveLetterAssigned bool   `json:"temporaryDriveLetterAssigned"`
	TemporaryDriveLetterRemoved  bool   `json:"temporaryDriveLetterRemoved"`
	PrivateMountVerified         bool   `json:"privateMountVerified"`
}

const (
	NativeMilestoneNotAttempted = "NOT_ATTEMPTED"
	NativeMilestoneNotMeasured  = "NOT_MEASURED"
	NativeMilestoneMeasuredPass = "MEASURED_PASS"
	NativeMilestoneMeasuredFail = "MEASURED_FAIL"
	NativeMilestoneReleased     = "MEASURED_RELEASED"
	NativeMilestoneUncertain    = "MEASURED_UNCERTAIN"
)

type NativeMilestones struct {
	DevDriveFormat         string `json:"devDriveFormat"`
	PrivateMount           string `json:"privateMount"`
	MountedPoolReadiness   string `json:"mountedPoolReadiness"`
	FilesystemValidation   string `json:"filesystemValidation"`
	BlockCloneCapability   string `json:"blockCloneCapability"`
	RegularBlockCloneIOCTL string `json:"regularBlockCloneIOCTL"`
	SparseBlockCloneIOCTL  string `json:"sparseBlockCloneIOCTL"`
	CoWIsolation           string `json:"cowIsolation"`
	Cleanup                string `json:"cleanup"`
}

// NativeEvidence is optional on errors. Pointer fields distinguish an actual
// false/zero measurement from a milestone that was never reached.
type NativeEvidence struct {
	WindowsProvider                 string            `json:"windowsProvider"`
	VolumeKind                      string            `json:"volumeKind"`
	DevDrive                        *DevDriveEvidence `json:"devDrive,omitempty"`
	Filesystem                      *string           `json:"filesystem,omitempty"`
	ClusterSize                     *int64            `json:"clusterSize,omitempty"`
	BlockCloneSupported             *bool             `json:"blockCloneSupported,omitempty"`
	LastCompletedMilestone          string            `json:"lastCompletedMilestone,omitempty"`
	RegularBlockCloneIOCTLAttempted *bool             `json:"regularBlockCloneIOCTLAttempted,omitempty"`
	SparseBlockCloneIOCTLAttempted  *bool             `json:"sparseBlockCloneIOCTLAttempted,omitempty"`
	Milestones                      NativeMilestones  `json:"nativeMilestones"`
}

// PoolPolicy is the storage policy accepted only after the host metadata,
// mounted-volume metadata, and in-volume metadata have been cross-checked.
// Per-run requests cannot weaken these limits.
type PoolPolicy struct {
	MaximumBytes             int64 `json:"maximumBytes"`
	SoftBudgetBytes          int64 `json:"softBudgetBytes"`
	WorkerReserveBytes       int64 `json:"workerReserveBytes"`
	MinimumHostFreeBytes     int64 `json:"minimumHostFreeBytes"`
	VHDXOverheadReserveBytes int64 `json:"vhdxOverheadReserveBytes"`
	ClusterSize              int64 `json:"clusterSize"`
}

type PoolMetrics struct {
	PoolSetupMs                     int64 `json:"poolSetupMs,omitempty"`
	PoolAttachMs                    int64 `json:"poolAttachMs,omitempty"`
	PoolMountMs                     int64 `json:"poolMountMs,omitempty"`
	PoolReadinessMs                 int64 `json:"poolReadinessMs,omitempty"`
	ClonedFileCount                 int64 `json:"clonedFileCount,omitempty"`
	ClonedBytes                     int64 `json:"clonedBytes,omitempty"`
	PhysicalCopiedFileCount         int64 `json:"physicalCopiedFileCount,omitempty"`
	PhysicalCopiedBytes             int64 `json:"physicalCopiedBytes,omitempty"`
	TailCopiedBytes                 int64 `json:"tailCopiedBytes,omitempty"`
	MetadataOnlyFileCount           int64 `json:"metadataOnlyFileCount,omitempty"`
	FailedFileCount                 int64 `json:"failedFileCount,omitempty"`
	CloneTreeMs                     int64 `json:"cloneTreeMs,omitempty"`
	RefsVolumeUsedBefore            int64 `json:"refsVolumeUsedBefore,omitempty"`
	RefsVolumeUsedAfterBaseline     int64 `json:"refsVolumeUsedAfterBaseline,omitempty"`
	RefsVolumeUsedAfterAcquire      int64 `json:"refsVolumeUsedAfterAcquire,omitempty"`
	RefsVolumeUsedAfterUnity        int64 `json:"refsVolumeUsedAfterUnity,omitempty"`
	RefsVolumeUsedAfterRelease      int64 `json:"refsVolumeUsedAfterRelease,omitempty"`
	HostVHDXLogicalBytes            int64 `json:"hostVhdxLogicalBytes,omitempty"`
	HostVHDXAllocatedBytes          int64 `json:"hostVhdxAllocatedBytes,omitempty"`
	HostFreeBytes                   int64 `json:"hostFreeBytes,omitempty"`
	CleanupMs                       int64 `json:"cleanupMs,omitempty"`
	SparseFileCount                 int64 `json:"sparseFileCount,omitempty"`
	SparseLogicalBytes              int64 `json:"sparseLogicalBytes,omitempty"`
	SparseAllocatedSourceBytes      int64 `json:"sparseAllocatedSourceBytes,omitempty"`
	SparseClonedBytes               int64 `json:"sparseClonedBytes,omitempty"`
	SparseHoleBytes                 int64 `json:"sparseHoleBytes,omitempty"`
	RegularBlockCloneIOCTLAttempted bool  `json:"regularBlockCloneIOCTLAttempted,omitempty"`
	SparseBlockCloneIOCTLAttempted  bool  `json:"sparseBlockCloneIOCTLAttempted,omitempty"`
}

type Result struct {
	SchemaVersion            string                    `json:"schemaVersion"`
	Status                   string                    `json:"status"`
	Operation                string                    `json:"operation"`
	Architecture             string                    `json:"architecture"`
	WindowsProvider          string                    `json:"windowsProvider"`
	VolumeKind               string                    `json:"volumeKind"`
	DevDrive                 DevDriveEvidence          `json:"devDrive"`
	NativeEvidence           *NativeEvidence           `json:"nativeEvidence,omitempty"`
	SetupTransaction         *SetupTransactionEvidence `json:"setupTransaction,omitempty"`
	ReleasedVersionModified  bool                      `json:"releasedVersionModified"`
	PhysicalImageCreated     bool                      `json:"physicalImageCreated"`
	DifferencingChildCreated bool                      `json:"differencingChildCreated"`
	FallbackUsed             bool                      `json:"fallbackUsed"`
	BlockCloneSupported      bool                      `json:"blockCloneSupported"`
	SourceUnchanged          bool                      `json:"sourceUnchanged,omitempty"`
	BaselineUnchanged        bool                      `json:"baselineUnchanged,omitempty"`
	Paths                    Paths                     `json:"paths"`
	Volume                   VolumeInfo                `json:"volume"`
	Pool                     *PoolMetadata             `json:"pool,omitempty"`
	Metrics                  PoolMetrics               `json:"metrics"`
	NativeWindowsStatus      string                    `json:"nativeWindowsStatus"`
	Residual                 Residual                  `json:"residual"`
}

type ResidualMetric struct {
	Measured bool `json:"measured"`
	Count    int  `json:"count"`
}

type Residual struct {
	Status                    string         `json:"status"`
	ActiveBaselineUses        ResidualMetric `json:"activeBaselineUses"`
	WorkerLeaseJournals       ResidualMetric `json:"workerLeaseJournals"`
	WorkerDirectories         ResidualMetric `json:"workerDirectories"`
	BaselineCreationLocks     ResidualMetric `json:"baselineCreationLocks"`
	BaselineStagingDirs       ResidualMetric `json:"baselineStagingDirs"`
	WorkerStagingDirs         ResidualMetric `json:"workerStagingDirs"`
	UnknownLeaseArtifacts     ResidualMetric `json:"unknownLeaseArtifacts"`
	UnknownBaselineEntries    ResidualMetric `json:"unknownBaselineEntries"`
	UnknownWorkerArtifacts    ResidualMetric `json:"unknownWorkerArtifacts"`
	QuarantineEntries         ResidualMetric `json:"quarantineEntries"`
	ReservationLocks          ResidualMetric `json:"reservationLocks"`
	BaselineCoordinationLocks ResidualMetric `json:"baselineCoordinationLocks"`
	BaselineMutationMarkers   ResidualMetric `json:"baselineMutationMarkers"`
	CoordinationArtifacts     ResidualMetric `json:"coordinationArtifacts"`
	SyntheticProbeDirectories ResidualMetric `json:"syntheticProbeDirectories"`
	MountReparsePoints        ResidualMetric `json:"mountReparsePoints"`
	MountDirectoryEntries     ResidualMetric `json:"mountDirectoryEntries"`
	Junctions                 ResidualMetric `json:"junctions"`
	AttachedDisks             ResidualMetric `json:"attachedDisks"`
	ProbeProcesses            ResidualMetric `json:"probeProcesses"`
	OwnedVHDXFiles            ResidualMetric `json:"ownedVhdxFiles"`
}

type CloneMetrics struct {
	CloneTreeMs                     int64 `json:"cloneTreeMs"`
	ClonedFileCount                 int64 `json:"clonedFileCount"`
	ClonedBytes                     int64 `json:"clonedBytes"`
	PhysicalCopiedFileCount         int64 `json:"physicalCopiedFileCount"`
	PhysicalCopiedBytes             int64 `json:"physicalCopiedBytes"`
	TailCopiedBytes                 int64 `json:"tailCopiedBytes"`
	MetadataOnlyFileCount           int64 `json:"metadataOnlyFileCount"`
	FailedFileCount                 int64 `json:"failedFileCount"`
	FallbackUsed                    bool  `json:"fallbackUsed"`
	SparseFileCount                 int64 `json:"sparseFileCount"`
	SparseLogicalBytes              int64 `json:"sparseLogicalBytes"`
	SparseAllocatedSourceBytes      int64 `json:"sparseAllocatedSourceBytes"`
	SparseClonedBytes               int64 `json:"sparseClonedBytes"`
	SparseHoleBytes                 int64 `json:"sparseHoleBytes"`
	RegularBlockCloneIOCTLAttempted bool  `json:"regularBlockCloneIOCTLAttempted"`
	SparseBlockCloneIOCTLAttempted  bool  `json:"sparseBlockCloneIOCTLAttempted"`
}

type LeaseState string

const (
	LeaseRequested   LeaseState = "requested"
	LeaseCloning     LeaseState = "cloning"
	LeaseReady       LeaseState = "ready"
	LeaseRunning     LeaseState = "running"
	LeaseReleasing   LeaseState = "releasing"
	LeaseReleased    LeaseState = "released"
	LeaseQuarantined LeaseState = "quarantined"
	LeaseUnknown     LeaseState = "unknown"
)
