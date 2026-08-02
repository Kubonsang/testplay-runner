// Package vhdxprobe contains a standalone Windows differencing-VHDX research
// probe. It is intentionally disconnected from the public CLI and workspace
// backends.
package vhdxprobe

const (
	DefaultParentVirtualBytes int64 = 512 << 20
	DefaultPayloadBytes       int64 = 64 << 20
)

// Config defines one isolated probe execution.
type Config struct {
	Root               string
	ParentVirtualBytes int64
	PayloadBytes       int64
}

// Paths contains every probe-owned path.
type Paths struct {
	Root      string `json:"root"`
	Operation string `json:"operation"`
	Parent    string `json:"parent"`
	ChildA    string `json:"childA"`
	ChildB    string `json:"childB"`
	Mounts    string `json:"mounts"`
}

// StorageMetrics records virtual and physical file sizes observed by the probe.
type StorageMetrics struct {
	ParentVirtualBytes          int64 `json:"parentVirtualBytes"`
	ParentFileBytes             int64 `json:"parentFileBytes"`
	ChildInitialFileBytes       int64 `json:"childInitialFileBytes"`
	ChildAfterAttachFileBytes   int64 `json:"childAfterAttachFileBytes"`
	ChildAfterMutationFileBytes int64 `json:"childAfterMutationFileBytes"`
	ChildAfterReattachFileBytes int64 `json:"childAfterReattachFileBytes"`
	LogicalPayloadBytes         int64 `json:"logicalPayloadBytes"`
}

// Durations records the elapsed time of each externally meaningful stage.
type Durations struct {
	ParentCreateMs     int64 `json:"parentCreateMs"`
	ParentAttachMs     int64 `json:"parentAttachMs"`
	ParentInitializeMs int64 `json:"parentInitializeMs"`
	ParentSeedMs       int64 `json:"parentSeedMs"`
	ParentDetachMs     int64 `json:"parentDetachMs"`
	ChildCreateMs      int64 `json:"childCreateMs"`
	ChildAttachMs      int64 `json:"childAttachMs"`
	MountResolveMs     int64 `json:"mountResolveMs"`
	MutationMs         int64 `json:"mutationMs"`
	ChildDetachMs      int64 `json:"childDetachMs"`
	CleanupMs          int64 `json:"cleanupMs"`
}

// Result is the machine-readable evidence from one completed probe.
type Result struct {
	OperationID               string         `json:"operationId"`
	Paths                     Paths          `json:"paths"`
	ParentPhysicalPath        string         `json:"parentPhysicalPath,omitempty"`
	ChildAPhysicalPath        string         `json:"childAPhysicalPath,omitempty"`
	ChildBPhysicalPath        string         `json:"childBPhysicalPath,omitempty"`
	ParentHashBefore          string         `json:"parentHashBefore,omitempty"`
	ParentHashAfter           string         `json:"parentHashAfter,omitempty"`
	BaselinePayloadHash       string         `json:"baselinePayloadHash,omitempty"`
	ParentIsolationPassed     bool           `json:"parentIsolationPassed"`
	SiblingIsolationPassed    bool           `json:"siblingIsolationPassed"`
	ReattachPersistencePassed bool           `json:"reattachPersistencePassed"`
	CleanupPassed             bool           `json:"cleanupPassed"`
	Metrics                   StorageMetrics `json:"metrics"`
	Durations                 Durations      `json:"durations"`
}
