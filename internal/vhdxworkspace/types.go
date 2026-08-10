package vhdxworkspace

import (
	"errors"
	"fmt"
	"time"
)

const (
	Provider                    = "vhdx-differencing"
	ParentSchemaVersion         = 2
	ProtocolSchemaVersion       = 2
	DefaultVirtualBytes   int64 = 64 << 30
	DefaultBlockBytes     int64 = 2 << 20
	DefaultSectorBytes    int64 = 4 << 10
	DefaultQuotaBytes     int64 = 32 << 30
	DefaultHostFloor      int64 = 20 << 30
	DefaultChildReserve   int64 = 2 << 30
	SafetyHostFloor       int64 = 5 << 30
)

const (
	ParentStatusMissing    = "missing"
	ParentStatusPending    = "pending"
	ParentStatusValid      = "valid"
	ParentStatusCorrupt    = "corrupt"
	ParentStatusQuarantine = "quarantined"
)

const (
	CleanupReleased    = "released"
	CleanupRetained    = "retained"
	CleanupQuarantined = "quarantined"
	CleanupUncertain   = "uncertain"
)

var (
	ErrInvalidInput       = errors.New("invalid vhdx workspace input")
	ErrParentConflict     = errors.New("parent transaction conflict")
	ErrStorageUnavailable = errors.New("storage capacity unavailable")
	ErrOwnershipMismatch  = errors.New("workspace ownership mismatch")
	ErrBrokerUnavailable  = errors.New("storage broker unavailable")
)

type Error struct {
	Code      string `json:"code"`
	Operation string `json:"operation,omitempty"`
	Path      string `json:"path,omitempty"`
	Message   string `json:"message,omitempty"`
	Cause     error  `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := e.Code
	if e.Operation != "" {
		message += ": " + e.Operation
	}
	if e.Path != "" {
		message += ": " + e.Path
	}
	if e.Message != "" {
		message += ": " + e.Message
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *Error) Unwrap() error { return e.Cause }

func wrap(code, operation, path string, err error) error {
	if err == nil {
		err = fmt.Errorf("operation failed")
	}
	return &Error{Code: code, Operation: operation, Path: path, Cause: err}
}

type FileIdentity struct {
	VolumeSerial string `json:"volumeSerial"`
	FileID       string `json:"fileId"`
}

type VolumeIdentity struct {
	VirtualDiskID string `json:"virtualDiskId"`
	VolumeGUID    string `json:"volumeGuid"`
	VolumeSerial  string `json:"volumeSerial"`
	Filesystem    string `json:"filesystem"`
	ClusterBytes  int64  `json:"clusterBytes"`
}

type ParentMetadata struct {
	SchemaVersion    int              `json:"schemaVersion"`
	Provider         string           `json:"provider"`
	CompatibilityKey CompatibilityKey `json:"compatibilityKey"`
	SourceSnapshot   SourceSnapshot   `json:"sourceSnapshot"`
	OwnershipToken   string           `json:"ownershipToken"`
	CreatedAt        time.Time        `json:"createdAt"`
	LastUsedAt       time.Time        `json:"lastUsedAt"`
	VHDXPath         string           `json:"vhdxPath"`
	FileIdentity     FileIdentity     `json:"fileIdentity"`
	FileWriteTime    time.Time        `json:"fileWriteTime"`
	Volume           VolumeIdentity   `json:"volume"`
	VirtualBytes     int64            `json:"virtualBytes"`
	BlockBytes       int64            `json:"blockBytes"`
	SectorBytes      int64            `json:"sectorBytes"`
	LogicalBytes     int64            `json:"logicalBytes"`
	AllocatedBytes   int64            `json:"allocatedBytes"`
	CommittedSHA256  string           `json:"committedSha256"`
	Immutable        bool             `json:"immutable"`
}

type LeaseJournal struct {
	SchemaVersion  int          `json:"schemaVersion"`
	LeaseID        string       `json:"leaseId"`
	RunID          string       `json:"runId"`
	UserSID        string       `json:"userSid"`
	OwnershipToken string       `json:"ownershipToken"`
	ParentKey      string       `json:"parentKey"`
	ParentPath     string       `json:"parentPath"`
	ChildPath      string       `json:"childPath"`
	MountPath      string       `json:"mountPath"`
	State          string       `json:"state"`
	ClientPID      int          `json:"clientPid"`
	UnityPID       int          `json:"unityPid,omitempty"`
	Retained       bool         `json:"retained"`
	CreatedAt      time.Time    `json:"createdAt"`
	UpdatedAt      time.Time    `json:"updatedAt"`
	FileIdentity   FileIdentity `json:"fileIdentity"`
	PhysicalPath   string       `json:"physicalPath,omitempty"`
	VolumeGUID     string       `json:"volumeGuid,omitempty"`
}

type RetainedRecord struct {
	SchemaVersion  int       `json:"schemaVersion"`
	RunID          string    `json:"runId"`
	LeaseID        string    `json:"leaseId"`
	OwnershipToken string    `json:"ownershipToken"`
	ParentKey      string    `json:"parentKey"`
	ChildPath      string    `json:"childPath"`
	CreatedAt      time.Time `json:"createdAt"`
}

type Capacity struct {
	QuotaBytes       int64 `json:"quotaBytes"`
	AllocatedBytes   int64 `json:"allocatedBytes"`
	ReclaimableBytes int64 `json:"reclaimableBytes"`
	HostFreeBytes    int64 `json:"hostFreeBytes"`
	HostFloorBytes   int64 `json:"hostFloorBytes"`
	ReserveBytes     int64 `json:"reserveBytes"`
}
