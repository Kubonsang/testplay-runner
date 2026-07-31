//go:build !windows

package unityvhdxfixture

import (
	"context"
	"runtime"
)

func RequireWindows(context.Context) error {
	return fixtureError(CodeUnsupportedPlatform, "unity-vhdx-fixture", runtime.GOOS, nil)
}
