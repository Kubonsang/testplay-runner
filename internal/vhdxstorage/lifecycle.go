// Package vhdxstorage owns the storage-helper workspace lifecycle. The package
// name is retained for compatibility with the original Windows VHDX probe;
// platform backends may use a different native copy-on-write primitive.
package vhdxstorage

import (
	"context"
	"fmt"
)

const (
	// Provider is retained for callers that name the original Windows backend.
	Provider        = "vhdx-differencing"
	ProviderAPFS    = "apfs-clonefile"
	ProviderReflink = "linux-reflink"
)

// CreateOptions fixes the parent disk geometry that all differencing children
// inherit. Zero geometry fields retain the platform default for legacy callers.
type CreateOptions struct {
	MaximumSize       int64
	BlockSizeInBytes  uint32
	SectorSizeInBytes uint32
}

type FileUsage struct {
	LogicalBytes   int64 `json:"logicalBytes"`
	AllocatedBytes int64 `json:"allocatedBytes"`
}

const (
	CodeUnsupportedPlatform               = "unsupported-platform"
	CodeNotElevated                       = "not-elevated"
	CodeParentNotFound                    = "parent-not-found"
	CodeParentInvalid                     = "parent-invalid"
	CodeChildExists                       = "child-exists"
	CodeChildCreateFailed                 = "child-create-failed"
	CodeChildOpenFailed                   = "child-open-failed"
	CodeAttachFailed                      = "attach-failed"
	CodePhysicalPathResolutionFailed      = "physical-path-resolution-failed"
	CodeVolumeResolutionFailed            = "volume-resolution-failed"
	CodeMountFailed                       = "mount-failed"
	CodeMountVisibilityTimeout            = "mount-visibility-timeout"
	CodeUnmountFailed                     = "unmount-failed"
	CodeDetachFailed                      = "detach-failed"
	CodeDetachVisibilityTimeout           = "detach-visibility-timeout"
	CodeCleanupFailed                     = "cleanup-failed"
	CodeCancelled                         = "cancelled"
	CodeVirtDiskAPIUnavailable            = "virt-disk-api-unavailable"
	CodeUnsafePhysicalDisk                = "unsafe-physical-disk"
	CodeCoWUnavailable                    = "cow-unavailable"
	CodeUnsafeSource                      = "unsafe-source"
	CodeMountOwnershipLost                = "mount-ownership-lost"
	CodeChildOwnershipLost                = "child-ownership-lost"
	CodeDevDriveUnavailable               = "dev-drive-unavailable"
	CodeDevDriveDisabled                  = "dev-drive-disabled"
	CodeDevDriveFormatFailed              = "dev-drive-format-failed"
	CodeDevDriveVerificationFailed        = "dev-drive-verification-failed"
	CodeTemporaryDriveLetterUnavailable   = "temporary-drive-letter-unavailable"
	CodeTemporaryDriveLetterCleanupFailed = "temporary-drive-letter-cleanup-failed"
)

const (
	StateCreatingChild = "creating-child"
	StateOpening       = "opening"
	StateAttaching     = "attaching"
	StateWaitingVolume = "waiting-volume"
	StateMounting      = "mounting"
	StateReady         = "ready"
	StateUnmounting    = "unmounting"
	StateDetaching     = "detaching"
	StateReleased      = "released"
)

// Error preserves the storage operation, path, Win32 status, and cause.
type Error struct {
	Code      string
	Operation string
	Path      string
	Win32Code uint32
	Cause     error
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
	if e.Win32Code != 0 {
		message += fmt.Sprintf(": win32=%d", e.Win32Code)
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newError(code, operation, path string, cause error) error {
	return &Error{Code: code, Operation: operation, Path: path, Cause: cause}
}

type Progress struct {
	State          string
	PhysicalPath   string
	VolumeGUIDPath string
}

type ProgressFunc func(Progress) error

type AcquireRequest struct {
	ParentPath string
	ChildPath  string
	MountPath  string
	// StoreRoot and LeaseID are internal ownership inputs. They are not part of
	// the public storage-helper request schema.
	StoreRoot string
	LeaseID   string
}

type LeaseInfo struct {
	ParentPath     string
	ChildPath      string
	PhysicalPath   string
	VolumeGUIDPath string
	MountPath      string
}

// Metrics contains only measured values. Nil fields are omitted rather than
// populated with estimates.
type Metrics struct {
	TotalWallClockMs              *int64 `json:"totalWallClockMs,omitempty"`
	AcquireWallClockMs            *int64 `json:"acquireWallClockMs,omitempty"`
	ReleaseWallClockMs            *int64 `json:"releaseWallClockMs,omitempty"`
	ChildCreateMs                 *int64 `json:"childCreateMs,omitempty"`
	ChildOpenMs                   *int64 `json:"childOpenMs,omitempty"`
	AttachCallMs                  *int64 `json:"attachCallMs,omitempty"`
	PhysicalPathResolveMs         *int64 `json:"physicalPathResolveMs,omitempty"`
	PnPDiscoveryWaitMs            *int64 `json:"pnpDiscoveryWaitMs,omitempty"`
	VolumeReadyWaitMs             *int64 `json:"volumeReadyWaitMs,omitempty"`
	MountCallMs                   *int64 `json:"mountCallMs,omitempty"`
	MountVisibilityWaitMs         *int64 `json:"mountVisibilityWaitMs,omitempty"`
	WorkspaceReadyMs              *int64 `json:"workspaceReadyMs,omitempty"`
	UnmountCallMs                 *int64 `json:"unmountCallMs,omitempty"`
	DetachCallMs                  *int64 `json:"detachCallMs,omitempty"`
	DetachVisibilityWaitMs        *int64 `json:"detachVisibilityWaitMs,omitempty"`
	CleanupMs                     *int64 `json:"cleanupMs,omitempty"`
	PowerShellBootstrapMs         *int64 `json:"powershellBootstrapMs,omitempty"`
	ChildBeforeAttachLogicalBytes *int64 `json:"childBeforeAttachLogicalBytes,omitempty"`
	ChildReadyLogicalBytes        *int64 `json:"childReadyLogicalBytes,omitempty"`
	ChildReleasedLogicalBytes     *int64 `json:"childReleasedLogicalBytes,omitempty"`
	ChildReadyAllocatedBytes      *int64 `json:"childReadyAllocatedBytes,omitempty"`
	ChildReleasedAllocatedBytes   *int64 `json:"childReleasedAllocatedBytes,omitempty"`
}

type Lease interface {
	Info() LeaseInfo
	Release(context.Context, bool, ProgressFunc) (Metrics, error)
}

type Backend interface {
	Platform() string
	Provider() string
	Supported() bool
	RequiresElevation() bool
	IsElevated(context.Context) (bool, error)
	Acquire(context.Context, AcquireRequest, ProgressFunc) (Lease, Metrics, error)
}

func notify(progress ProgressFunc, value Progress) error {
	if progress == nil {
		return nil
	}
	return progress(value)
}

func milliseconds(value int64) *int64 { return &value }
