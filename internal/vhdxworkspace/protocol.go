package vhdxworkspace

import (
	"context"
	"time"
)

const (
	OperationHello            = "hello"
	OperationBeginParentBuild = "begin-parent-build"
	OperationCommitParent     = "commit-parent"
	OperationAbortParent      = "abort-parent"
	OperationAcquire          = "acquire"
	OperationHeartbeat        = "heartbeat"
	OperationRelease          = "release"
	OperationAttachRetained   = "attach-retained"
	OperationRemoveRetained   = "remove-retained"
	OperationStatus           = "status"
	OperationAdmit            = "admit"
	OperationGC               = "gc"
)

type Request struct {
	SchemaVersion          int               `json:"schemaVersion"`
	Operation              string            `json:"operation"`
	RequestID              string            `json:"requestId"`
	UserSID                string            `json:"userSid,omitempty"`
	RunID                  string            `json:"runId,omitempty"`
	WorkspaceID            string            `json:"workspaceId,omitempty"`
	WorkspaceRoot          string            `json:"workspaceRoot,omitempty"`
	ParentKey              *CompatibilityKey `json:"parentKey,omitempty"`
	Source                 *SourceSnapshot   `json:"sourceSnapshot,omitempty"`
	LeaseID                string            `json:"leaseId,omitempty"`
	TransactionID          string            `json:"transactionId,omitempty"`
	ClientPID              int               `json:"clientPid,omitempty"`
	UnityPID               int               `json:"unityPid,omitempty"`
	RetainChild            bool              `json:"retainChild,omitempty"`
	DryRun                 bool              `json:"dryRun,omitempty"`
	StoreMaxAllocatedBytes int64             `json:"storeMaxAllocatedBytes,omitempty"`
	MinimumHostFreeBytes   int64             `json:"minimumHostFreeBytes,omitempty"`
}

type ParentBuild struct {
	TransactionID string `json:"transactionId"`
	ParentKey     string `json:"parentKey"`
	MountPath     string `json:"mountPath"`
	State         string `json:"state"`
}

type Lease struct {
	LeaseID      string    `json:"leaseId"`
	RunID        string    `json:"runId"`
	ParentKey    string    `json:"parentKey"`
	MountPath    string    `json:"mountPath"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"createdAt"`
	Retained     bool      `json:"retained"`
	PhysicalPath string    `json:"physicalPath,omitempty"`
	VolumeGUID   string    `json:"volumeGuid,omitempty"`
}

type Metrics struct {
	ParentStatus         string   `json:"parentStatus,omitempty"`
	ParentCreated        bool     `json:"parentCreated"`
	ParentReused         bool     `json:"parentReused"`
	ParentBuildMs        int64    `json:"parentBuildMs,omitempty"`
	ParentVerifyMs       int64    `json:"parentVerifyMs,omitempty"`
	ParentVirtualBytes   int64    `json:"parentVirtualBytes,omitempty"`
	ParentAllocatedBytes int64    `json:"parentAllocatedBytes,omitempty"`
	ChildCreateMs        int64    `json:"childCreateMs,omitempty"`
	ChildAttachMs        int64    `json:"childAttachMs,omitempty"`
	ChildMountMs         int64    `json:"childMountMs,omitempty"`
	ChildReleaseMs       int64    `json:"childReleaseMs,omitempty"`
	ChildReadyBytes      int64    `json:"childReadyAllocatedBytes,omitempty"`
	ChildPeakBytes       int64    `json:"childPeakAllocatedBytes,omitempty"`
	ChildReleasedBytes   int64    `json:"childReleasedAllocatedBytes,omitempty"`
	CleanupState         string   `json:"cleanupState,omitempty"`
	Capacity             Capacity `json:"capacity"`
}

type Response struct {
	SchemaVersion int             `json:"schemaVersion"`
	RequestID     string          `json:"requestId"`
	OK            bool            `json:"ok"`
	Provider      string          `json:"provider,omitempty"`
	BrokerVersion string          `json:"brokerVersion,omitempty"`
	WorkspaceRoot string          `json:"workspaceRoot,omitempty"`
	StoreRoot     string          `json:"storeRoot,omitempty"`
	Parent        *ParentMetadata `json:"parent,omitempty"`
	ParentBuild   *ParentBuild    `json:"parentBuild,omitempty"`
	Lease         *Lease          `json:"lease,omitempty"`
	Metrics       *Metrics        `json:"metrics,omitempty"`
	Status        *Status         `json:"status,omitempty"`
	Error         *Error          `json:"error,omitempty"`
}

// Client is the unprivileged broker boundary used by the run service. A pipe
// implementation authenticates the caller in the service; DirectClient is
// intentionally limited to tests and an in-process service host.
type Client interface {
	Call(context.Context, Request) (Response, error)
}

type Status struct {
	Provider                    string   `json:"provider"`
	UserSID                     string   `json:"userSid"`
	ParentCount                 int      `json:"parentCount"`
	ActiveChildCount            int      `json:"activeChildCount"`
	RetainedChildCount          int      `json:"retainedChildCount"`
	PendingCount                int      `json:"pendingCount"`
	QuarantineCount             int      `json:"quarantineCount"`
	ParentAllocatedBytes        int64    `json:"parentAllocatedBytes"`
	ParentLogicalBytes          int64    `json:"parentLogicalBytes"`
	ActiveChildAllocatedBytes   int64    `json:"activeChildAllocatedBytes"`
	ActiveChildLogicalBytes     int64    `json:"activeChildLogicalBytes"`
	RetainedChildAllocatedBytes int64    `json:"retainedChildAllocatedBytes"`
	RetainedChildLogicalBytes   int64    `json:"retainedChildLogicalBytes"`
	QuarantineAllocatedBytes    int64    `json:"quarantineAllocatedBytes"`
	QuarantineLogicalBytes      int64    `json:"quarantineLogicalBytes"`
	ManualRecoveryRequired      bool     `json:"manualRecoveryRequired"`
	Capacity                    Capacity `json:"capacity"`
}
