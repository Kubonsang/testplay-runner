//go:build !windows

package vhdxprobe

import "context"

// Run reports that the VirtDisk probe is available only on Windows.
func Run(ctx context.Context, config Config) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, probeError(CodeCancelled, "start", config.Root, err)
	}
	return nil, probeError(CodeUnsupportedPlatform, "start", config.Root, nil)
}
