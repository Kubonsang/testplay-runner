//go:build !windows

package refsclone

import (
	"context"
	"runtime"
)

func probePlatform(_ context.Context, root string) (Capability, error) {
	return Capability{
		Supported:         false,
		UnsupportedReason: ErrUnsupportedPlatform.Error(),
		Evidence:          []string{"platform=" + runtime.GOOS},
	}, &Error{Code: CodeUnsupportedPlatform, Operation: "probe", Path: root, Cause: ErrUnsupportedPlatform}
}

func cloneFilePlatform(_ context.Context, request Request) (*Result, error) {
	return nil, &Error{Code: CodeUnsupportedPlatform, Operation: "clone", Path: request.DestinationPath, Cause: ErrUnsupportedPlatform}
}

func measureFilePlatform(path string) (FileMeasurement, error) {
	return FileMeasurement{}, &Error{Code: CodeUnsupportedPlatform, Operation: "measure-file", Path: path, Cause: ErrUnsupportedPlatform}
}

func inspectVolumePlatform(path string) (VolumeInfo, error) {
	return VolumeInfo{}, &Error{Code: CodeUnsupportedPlatform, Operation: "inspect-volume", Path: path, Cause: ErrUnsupportedPlatform}
}
