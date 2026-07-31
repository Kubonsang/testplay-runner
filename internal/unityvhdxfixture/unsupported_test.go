//go:build !windows

package unityvhdxfixture

import (
	"context"
	"testing"
)

func TestRequireWindowsReturnsStructuredUnsupportedPlatform(t *testing.T) {
	if code := ErrorCode(RequireWindows(context.Background())); code != CodeUnsupportedPlatform {
		t.Fatalf("code=%q want=%q", code, CodeUnsupportedPlatform)
	}
}
