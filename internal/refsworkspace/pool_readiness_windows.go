//go:build windows

package refsworkspace

import (
	"errors"

	"golang.org/x/sys/windows"
)

func platformTemporaryMountedPoolReadinessError(err error, contentVisible bool) bool {
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_NOT_READY) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return true
	}
	return !contentVisible && errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
