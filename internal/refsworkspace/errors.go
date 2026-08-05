package refsworkspace

import (
	"errors"
	"fmt"
)

const (
	CodeUnsupportedPlatform               = "unsupported-platform"
	CodeNotElevated                       = "not-elevated"
	CodeReFSFormatUnavailable             = "refs-format-unavailable"
	CodeDevDriveUnavailable               = "dev-drive-unavailable"
	CodeDevDriveDisabled                  = "dev-drive-disabled"
	CodeDevDriveFormatFailed              = "dev-drive-format-failed"
	CodeDevDriveVerificationFailed        = "dev-drive-verification-failed"
	CodeTemporaryDriveLetterUnavailable   = "temporary-drive-letter-unavailable"
	CodeTemporaryDriveLetterCleanupFailed = "temporary-drive-letter-cleanup-failed"
	CodeBlockCloneUnavailable             = "refs-block-clone-unavailable"
	CodePoolNotFound                      = "pool-not-found"
	CodeIncompleteSetup                   = "incomplete-setup"
	CodePoolMountNotReady                 = "pool-mount-not-ready"
	CodePoolPersistenceVerificationFailed = "pool-persistence-verification-failed"
	CodePoolCorrupt                       = "pool-corrupt"
	CodePoolAlreadyMounted                = "pool-already-mounted"
	CodePoolNotMounted                    = "pool-not-mounted"
	CodeBaselineMissing                   = "baseline-missing"
	CodeBaselineCorrupt                   = "baseline-corrupt"
	CodeBaselineInUse                     = "baseline-in-use"
	CodeLeaseConflict                     = "lease-conflict"
	CodeOrphanFound                       = "orphan-found"
	CodeStorageBudgetExceeded             = "storage-budget-exceeded"
	CodeHostFreeSpaceFloor                = "host-free-space-floor"
	CodeDiskFull                          = "disk-full"
	CodeCloneFailed                       = "clone-failed"
	CodeJunctionFailed                    = "junction-failed"
	CodeCleanupFailed                     = "cleanup-failed"
	CodeCancelled                         = "cancelled"
	CodeInvalidConfiguration              = "invalid-configuration"
	CodeOwnershipMismatch                 = "ownership-mismatch"
)

// Error is the stable machine-readable error boundary for the probe.
type Error struct {
	Code                   string                    `json:"code"`
	Operation              string                    `json:"operation,omitempty"`
	Path                   string                    `json:"path,omitempty"`
	Cause                  error                     `json:"-"`
	CleanupState           string                    `json:"cleanupState,omitempty"`
	OwnerMetadataCommitted bool                      `json:"ownerMetadataCommitted"`
	OwnedVHDXPath          string                    `json:"ownedVhdxPath,omitempty"`
	ManualRecoveryRequired bool                      `json:"manualRecoveryRequired"`
	NativeEvidence         *NativeEvidence           `json:"nativeEvidence,omitempty"`
	SetupTransaction       *SetupTransactionEvidence `json:"setupTransaction,omitempty"`
	MountPath              string                    `json:"mountPath,omitempty"`
	PoolMetadataPath       string                    `json:"poolMetadataPath,omitempty"`
	MountReadyTimeoutMs    int64                     `json:"mountReadyTimeoutMs,omitempty"`
	LastObservedError      string                    `json:"lastObservedFilesystemError,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	value := e.Code
	if e.Operation != "" {
		value += ": " + e.Operation
	}
	if e.Path != "" {
		value += ": " + e.Path
	}
	if e.Cause != nil {
		value += ": " + e.Cause.Error()
	}
	return value
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

func errorWithCleanupEvidence(err error, state, ownedVHDX string, ownerCommitted bool) error {
	if err == nil {
		return nil
	}
	var existing *Error
	code, operation, path := ErrorCode(err), "", ""
	if errors.As(err, &existing) {
		operation, path = existing.Operation, existing.Path
	}
	var nativeEvidence *NativeEvidence
	if existing != nil {
		nativeEvidence = existing.NativeEvidence
	}
	wrapped := &Error{Code: code, Operation: operation, Path: path, Cause: err, CleanupState: state, OwnerMetadataCommitted: ownerCommitted, OwnedVHDXPath: ownedVHDX, ManualRecoveryRequired: state != "released" && state != "preserved", NativeEvidence: nativeEvidence}
	if existing != nil {
		wrapped.SetupTransaction = existing.SetupTransaction
		wrapped.MountPath = existing.MountPath
		wrapped.PoolMetadataPath = existing.PoolMetadataPath
		wrapped.MountReadyTimeoutMs = existing.MountReadyTimeoutMs
		wrapped.LastObservedError = existing.LastObservedError
	}
	return wrapped
}

func errorWithNativeEvidence(err error, evidence *NativeEvidence) error {
	if err == nil || evidence == nil {
		return err
	}
	var existing *Error
	if errors.As(err, &existing) {
		return &Error{
			Code: existing.Code, Operation: existing.Operation, Path: existing.Path, Cause: err,
			CleanupState: existing.CleanupState, OwnerMetadataCommitted: existing.OwnerMetadataCommitted,
			OwnedVHDXPath: existing.OwnedVHDXPath, ManualRecoveryRequired: existing.ManualRecoveryRequired,
			NativeEvidence: evidence, SetupTransaction: existing.SetupTransaction, MountPath: existing.MountPath, PoolMetadataPath: existing.PoolMetadataPath,
			MountReadyTimeoutMs: existing.MountReadyTimeoutMs, LastObservedError: existing.LastObservedError,
		}
	}
	return &Error{Code: ErrorCode(err), Cause: err, NativeEvidence: evidence}
}

func errorWithSetupTransactionEvidence(err error, evidence *SetupTransactionEvidence) error {
	if err == nil || evidence == nil {
		return err
	}
	var existing *Error
	if errors.As(err, &existing) {
		return &Error{
			Code: existing.Code, Operation: existing.Operation, Path: existing.Path, Cause: err,
			CleanupState: existing.CleanupState, OwnerMetadataCommitted: existing.OwnerMetadataCommitted,
			OwnedVHDXPath: existing.OwnedVHDXPath, ManualRecoveryRequired: existing.ManualRecoveryRequired,
			NativeEvidence: existing.NativeEvidence, SetupTransaction: evidence,
			MountPath: existing.MountPath, PoolMetadataPath: existing.PoolMetadataPath,
			MountReadyTimeoutMs: existing.MountReadyTimeoutMs, LastObservedError: existing.LastObservedError,
		}
	}
	return &Error{Code: ErrorCode(err), Cause: err, SetupTransaction: evidence}
}

func ErrorCode(err error) string {
	var probeErr *Error
	if errors.As(err, &probeErr) {
		return probeErr.Code
	}
	return "unknown"
}

func cancelled(operation, path string, err error) error {
	return newError(CodeCancelled, operation, path, err)
}

func require(condition bool, code, operation, path, message string) error {
	if condition {
		return nil
	}
	return newError(code, operation, path, fmt.Errorf("%s", message))
}
