// Package mountedcopy copies the contents of a verified directory root while
// deliberately excluding the root's own reparse metadata.
package mountedcopy

import (
	"errors"
	"fmt"
)

const (
	CodeInvalidSource      = "invalid-source"
	CodeInvalidDestination = "invalid-destination"
	CodeRootOverlap        = "root-overlap"
	CodeDestinationExists  = "destination-exists"
	CodeSourceNotDirectory = "source-not-directory"
	CodeRootNotVolumeMount = "root-not-volume-mount"
	CodeNestedReparse      = "nested-reparse-point"
	CodeCopyFailed         = "copy-failed"
)

type Error struct {
	Code      string
	Operation string
	Path      string
	err       error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s: %s: %v", e.Code, e.Operation, e.Path, e.err)
}

func (e *Error) Unwrap() error { return e.err }

func newError(code, operation, path string, err error) error {
	return &Error{Code: code, Operation: operation, Path: path, err: err}
}

func ErrorCode(err error) string {
	var value *Error
	if errors.As(err, &value) {
		return value.Code
	}
	return ""
}
