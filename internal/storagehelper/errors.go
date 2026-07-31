package storagehelper

import (
	"errors"
	"fmt"

	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

const (
	CodeUnsupportedPlatform  = "unsupported-platform"
	CodeNotElevated          = "not-elevated"
	CodeInvalidRequest       = "invalid-request"
	CodeUnsupportedSchema    = "unsupported-schema"
	CodeUnknownOperation     = "unknown-operation"
	CodeInvalidStoreRoot     = "invalid-store-root"
	CodeInvalidWorkspaceRoot = "invalid-workspace-root"
	CodeInvalidParentPath    = "invalid-parent-path"
	CodeInvalidChildPath     = "invalid-child-path"
	CodeInvalidMountPath     = "invalid-mount-path"
	CodeParentNotFound       = "parent-not-found"
	CodeParentInvalid        = "parent-invalid"
	CodeChildExists          = "child-exists"
	CodeMountPathNotEmpty    = "mount-path-not-empty"
	CodeRequestConflict      = "request-conflict"
	CodeChildPathConflict    = "child-path-conflict"
	CodeMountPathConflict    = "mount-path-conflict"
	CodeLeaseConflict        = "lease-conflict"
	CodeOrphanFound          = "orphan-found"
	CodeJournalWriteFailed   = "journal-write-failed"
	CodeReleaseFailed        = "release-failed"
)

type Error struct {
	Code      string `json:"code"`
	Operation string `json:"operation,omitempty"`
	Path      string `json:"path,omitempty"`
	Win32Code uint32 `json:"win32Code,omitempty"`
	Cause     string `json:"cause,omitempty"`
	err       error
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
	if e.Cause != "" {
		message += ": " + e.Cause
	}
	return message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func helperError(code, operation, path string, cause error) *Error {
	value := &Error{Code: code, Operation: operation, Path: path, err: cause}
	if cause != nil {
		value.Cause = cause.Error()
	}
	var storageErr *vhdxstorage.Error
	if errors.As(cause, &storageErr) {
		value.Code = storageErr.Code
		value.Operation = storageErr.Operation
		value.Path = storageErr.Path
		value.Win32Code = storageErr.Win32Code
		if storageErr.Cause != nil {
			value.Cause = storageErr.Cause.Error()
		}
	}
	return value
}

func wrapReleaseError(cause error) *Error {
	return helperError(CodeReleaseFailed, "release", "", cause)
}

func errorCode(err error) string {
	var value *Error
	if errors.As(err, &value) {
		return value.Code
	}
	var storageErr *vhdxstorage.Error
	if errors.As(err, &storageErr) {
		return storageErr.Code
	}
	return fmt.Sprintf("%T", err)
}
