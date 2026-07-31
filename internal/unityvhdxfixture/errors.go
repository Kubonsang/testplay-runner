// Package unityvhdxfixture owns the small Unity Library compatibility harness.
// It is intentionally disconnected from the public testplay CLI and production
// workspace backends.
package unityvhdxfixture

import (
	"errors"
	"fmt"
)

const (
	CodeUnsupportedPlatform          = "unsupported-platform"
	CodeUnityEditorNotFound          = "unity-editor-not-found"
	CodeUnityVersionMismatch         = "unity-version-mismatch"
	CodeUnityLicenseFailed           = "unity-license-failed"
	CodeUnityPackageResolutionFailed = "unity-package-resolution-failed"
	CodeUnityNativeCrash             = "unity-native-crash"
	CodeUnityAssetDatabaseOpenFailed = "unity-asset-database-open-failed"
	CodeUnityLibraryPathUnavailable  = "unity-library-path-unavailable"
	CodeUnityRunFailed               = "unity-run-failed"
	CodePhysicalLibraryIsReparse     = "physical-library-is-reparse-point"
	CodePhysicalLibraryNotDirectory  = "physical-library-not-directory"
	CodePhysicalLibraryDangling      = "physical-library-dangling"
	CodePhysicalLibraryCopyEscaped   = "physical-library-copy-escaped"
	CodeNestedReparsePointFound      = "nested-reparse-point-found"
	CodeLibraryMountLost             = "library-mount-lost"
	CodeLibraryMountReplaced         = "library-mount-replaced"
	CodeLibraryVolumeChanged         = "library-volume-changed"
	CodeSemanticParityFailed         = "semantic-parity-failed"
	CodeParentIsolationFailed        = "parent-isolation-failed"
	CodeCleanupFailed                = "cleanup-failed"
	CodeInvalidFixtureRoot           = "invalid-fixture-root"
	CodeInvalidArtifactRoot          = "invalid-artifact-root"
	CodeEvidenceWriteFailed          = "evidence-write-failed"
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

func (e *Error) Unwrap() error { return e.err }

func fixtureError(code, operation, path string, cause error) error {
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

func required(value, name string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}
