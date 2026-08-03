//go:build !windows && !darwin && !linux

package vhdxstorage

import (
	"context"
	"runtime"
)

type unsupportedBackend struct{}

func NewBackend() Backend { return unsupportedBackend{} }

func (unsupportedBackend) Platform() string        { return runtime.GOOS }
func (unsupportedBackend) Provider() string        { return "unsupported" }
func (unsupportedBackend) Supported() bool         { return false }
func (unsupportedBackend) RequiresElevation() bool { return false }

func (unsupportedBackend) IsElevated(context.Context) (bool, error) { return false, nil }

func (unsupportedBackend) Acquire(
	ctx context.Context,
	request AcquireRequest,
	_ ProgressFunc,
) (Lease, Metrics, error) {
	if err := ctx.Err(); err != nil {
		return nil, Metrics{}, newError(CodeCancelled, "acquire", request.ChildPath, err)
	}
	return nil, Metrics{}, newError(CodeUnsupportedPlatform, "acquire", request.ChildPath, nil)
}
