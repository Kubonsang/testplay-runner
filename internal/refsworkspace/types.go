package refsworkspace

import "time"

const (
	PoolSchemaVersion     = 1
	BaselineSchemaVersion = 1
	LeaseSchemaVersion    = 1
	DefaultMaximumBytes   = int64(16 << 30)
	DefaultSoftBudget     = int64(14 << 30)
	DefaultReserveBytes   = int64(2 << 30)
)

type Config struct {
	Root               string `json:"root"`
	VHDXPath           string `json:"vhdxPath,omitempty"`
	MountRoot          string `json:"mountRoot,omitempty"`
	MaximumBytes       int64  `json:"maximumBytes"`
	SoftBudgetBytes    int64  `json:"softBudgetBytes"`
	WorkerReserveBytes int64  `json:"workerReserveBytes"`
}

type Paths struct {
	Root       string `json:"root"`
	VHDX       string `json:"vhdx"`
	Owner      string `json:"owner"`
	Mount      string `json:"mount"`
	PoolRoot   string `json:"poolRoot"`
	PoolFile   string `json:"poolFile"`
	Baselines  string `json:"baselines"`
	Workers    string `json:"workers"`
	Leases     string `json:"leases"`
	Quarantine string `json:"quarantine"`
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
	SchemaVersion      int       `json:"schemaVersion"`
	Architecture       string    `json:"architecture"`
	CreatedAt          time.Time `json:"createdAt"`
	OwnershipToken     string    `json:"ownershipToken"`
	VHDXPath           string    `json:"vhdxPath"`
	VHDXIdentity       string    `json:"vhdxIdentity"`
	VolumeGUIDPath     string    `json:"volumeGuidPath"`
	Filesystem         string    `json:"filesystem"`
	ClusterSize        int64     `json:"clusterSize"`
	MaximumBytes       int64     `json:"maximumBytes"`
	SoftBudgetBytes    int64     `json:"softBudgetBytes"`
	WorkerReserveBytes int64     `json:"workerReserveBytes"`
}

type PoolMetrics struct {
	PoolSetupMs                 int64 `json:"poolSetupMs,omitempty"`
	PoolAttachMs                int64 `json:"poolAttachMs,omitempty"`
	PoolMountMs                 int64 `json:"poolMountMs,omitempty"`
	ClonedFileCount             int64 `json:"clonedFileCount,omitempty"`
	ClonedBytes                 int64 `json:"clonedBytes,omitempty"`
	PhysicalCopiedFileCount     int64 `json:"physicalCopiedFileCount,omitempty"`
	PhysicalCopiedBytes         int64 `json:"physicalCopiedBytes,omitempty"`
	TailCopiedBytes             int64 `json:"tailCopiedBytes,omitempty"`
	MetadataOnlyFileCount       int64 `json:"metadataOnlyFileCount,omitempty"`
	FailedFileCount             int64 `json:"failedFileCount,omitempty"`
	CloneTreeMs                 int64 `json:"cloneTreeMs,omitempty"`
	RefsVolumeUsedBefore        int64 `json:"refsVolumeUsedBefore,omitempty"`
	RefsVolumeUsedAfterBaseline int64 `json:"refsVolumeUsedAfterBaseline,omitempty"`
	RefsVolumeUsedAfterAcquire  int64 `json:"refsVolumeUsedAfterAcquire,omitempty"`
	RefsVolumeUsedAfterUnity    int64 `json:"refsVolumeUsedAfterUnity,omitempty"`
	RefsVolumeUsedAfterRelease  int64 `json:"refsVolumeUsedAfterRelease,omitempty"`
	HostVHDXLogicalBytes        int64 `json:"hostVhdxLogicalBytes,omitempty"`
	HostVHDXAllocatedBytes      int64 `json:"hostVhdxAllocatedBytes,omitempty"`
	HostFreeBytes               int64 `json:"hostFreeBytes,omitempty"`
	CleanupMs                   int64 `json:"cleanupMs,omitempty"`
}

type Result struct {
	SchemaVersion            string        `json:"schemaVersion"`
	Status                   string        `json:"status"`
	Operation                string        `json:"operation"`
	Architecture             string        `json:"architecture"`
	ReleasedVersionModified  bool          `json:"releasedVersionModified"`
	PhysicalImageCreated     bool          `json:"physicalImageCreated"`
	DifferencingChildCreated bool          `json:"differencingChildCreated"`
	FallbackUsed             bool          `json:"fallbackUsed"`
	BlockCloneSupported      bool          `json:"blockCloneSupported"`
	SourceUnchanged          bool          `json:"sourceUnchanged,omitempty"`
	BaselineUnchanged        bool          `json:"baselineUnchanged,omitempty"`
	Paths                    Paths         `json:"paths"`
	Volume                   VolumeInfo    `json:"volume"`
	Pool                     *PoolMetadata `json:"pool,omitempty"`
	Metrics                  PoolMetrics   `json:"metrics"`
	NativeWindowsStatus      string        `json:"nativeWindowsStatus"`
	Residual                 Residual      `json:"residual"`
}

type Residual struct {
	ActiveLeases    int `json:"activeLeases"`
	WorkerLibraries int `json:"workerLibraries"`
	Junctions       int `json:"junctions"`
	AttachedDisks   int `json:"attachedDisks"`
	ProbeProcesses  int `json:"probeProcesses"`
}

type CloneMetrics struct {
	CloneTreeMs             int64 `json:"cloneTreeMs"`
	ClonedFileCount         int64 `json:"clonedFileCount"`
	ClonedBytes             int64 `json:"clonedBytes"`
	PhysicalCopiedFileCount int64 `json:"physicalCopiedFileCount"`
	PhysicalCopiedBytes     int64 `json:"physicalCopiedBytes"`
	TailCopiedBytes         int64 `json:"tailCopiedBytes"`
	MetadataOnlyFileCount   int64 `json:"metadataOnlyFileCount"`
	FailedFileCount         int64 `json:"failedFileCount"`
	FallbackUsed            bool  `json:"fallbackUsed"`
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
