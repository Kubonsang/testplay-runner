// Package gnfvhdxbenchmark owns the opt-in, single-worker GNF_ comparison
// harness. It is intentionally disconnected from the public CLI.
package gnfvhdxbenchmark

import (
	"errors"
	"fmt"
)

const (
	CodeUnsupportedPlatform = "unsupported-platform"
	CodeInvalidInput        = "invalid-input"
	CodeSourceDirty         = "source-dirty"
	CodeSelectionMismatch   = "selection-mismatch"
	CodeSemanticMismatch    = "semantic-parity-failed"
	CodeParentChanged       = "parent-hash-changed"
	CodeContamination       = "workspace-contamination"
	CodeMountIntegrity      = "mount-integrity-failed"
	CodeCleanupFailed       = "cleanup-failed"
	CodeWarmLibraryInvalid  = "warm-library-invalid"
	CodeEvidenceWrite       = "evidence-write-failed"
)

type Error struct {
	Code      string `json:"code"`
	Operation string `json:"operation,omitempty"`
	Path      string `json:"path,omitempty"`
	Cause     string `json:"cause,omitempty"`
	err       error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s: %s: %s", e.Code, e.Operation, e.Path, e.Cause)
}

func (e *Error) Unwrap() error { return e.err }

func benchmarkError(code, operation, path string, cause error) error {
	value := &Error{Code: code, Operation: operation, Path: path, err: cause}
	if cause != nil {
		value.Cause = cause.Error()
	}
	return value
}

func ErrorCode(err error) string {
	var value *Error
	if errors.As(err, &value) {
		return value.Code
	}
	return ""
}
