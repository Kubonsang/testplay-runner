//go:build !windows

package vhdxprobe

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestUnsupportedPlatformStub(t *testing.T) {
	_, err := Run(context.Background(), Config{Root: filepath.Join(t.TempDir(), "probe")})
	var probeErr *Error
	if !errors.As(err, &probeErr) || probeErr.Code != CodeUnsupportedPlatform {
		t.Fatalf("error = %#v", err)
	}
}
