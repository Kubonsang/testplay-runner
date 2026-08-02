package vhdxprobe

import "fmt"

const (
	CodeUnsupportedPlatform       = "unsupported-platform"
	CodeNotElevated               = "not-elevated"
	CodeVirtDiskAPIUnavailable    = "virt-disk-api-unavailable"
	CodeInvalidProbeRoot          = "invalid-probe-root"
	CodeParentExists              = "parent-exists"
	CodeParentCreateFailed        = "parent-create-failed"
	CodeParentAttachFailed        = "parent-attach-failed"
	CodeUnsafePhysicalDisk        = "unsafe-physical-disk"
	CodeParentInitializeFailed    = "parent-initialize-failed"
	CodeParentSeedFailed          = "parent-seed-failed"
	CodeParentMutated             = "parent-mutated"
	CodeChildExists               = "child-exists"
	CodeChildCreateFailed         = "child-create-failed"
	CodeChildAttachFailed         = "child-attach-failed"
	CodeMountResolutionFailed     = "mount-resolution-failed"
	CodeDetachFailed              = "detach-failed"
	CodeVerificationFailed        = "verification-failed"
	CodeSiblingIsolationFailed    = "sibling-isolation-failed"
	CodeReattachPersistenceFailed = "reattach-persistence-failed"
	CodeCleanupFailed             = "cleanup-failed"
	CodeCancelled                 = "cancelled"
)

// Error preserves the structured probe stage and the original Win32 status.
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

func probeError(code, operation, path string, cause error) error {
	return &Error{
		Code:      code,
		Operation: operation,
		Path:      path,
		Cause:     cause,
	}
}
