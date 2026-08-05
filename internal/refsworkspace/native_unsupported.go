//go:build !windows

package refsworkspace

import (
	"context"
	"runtime"
)

type unsupportedPoolNative struct{}

func newPoolNative() PoolNative                { return unsupportedPoolNative{} }
func (unsupportedPoolNative) Platform() string { return runtime.GOOS }
func (unsupportedPoolNative) EnsureAvailable(context.Context) error {
	return newError(CodeUnsupportedPlatform, "native", runtime.GOOS, nil)
}
func (unsupportedPoolNative) IsElevated(context.Context) (bool, error) { return false, nil }
func (unsupportedPoolNative) CreateDynamic(string, int64) error {
	return newError(CodeUnsupportedPlatform, "create-vhdx", "", nil)
}
func (unsupportedPoolNative) Mount(context.Context, string, string, bool) (MountedPool, error) {
	return nil, newError(CodeUnsupportedPlatform, "mount", "", nil)
}
func (unsupportedPoolNative) MountReadOnly(context.Context, string, string) (MountedPool, error) {
	return nil, newError(CodeUnsupportedPlatform, "mount-read-only", "", nil)
}
func (unsupportedPoolNative) MountReadOnly(context.Context, string, string) (MountedPool, error) {
	return nil, newError(CodeUnsupportedPlatform, "mount-read-only", "", nil)
}
func (unsupportedPoolNative) FileIdentity(string) (string, error) {
	return "", newError(CodeUnsupportedPlatform, "file-identity", "", nil)
}
func (unsupportedPoolNative) FileUsage(string) (FileUsage, error) {
	return FileUsage{}, newError(CodeUnsupportedPlatform, "file-usage", "", nil)
}
func (unsupportedPoolNative) HostFilesystem(string) (string, error) {
	return "", newError(CodeUnsupportedPlatform, "host-filesystem", "", nil)
}
func (unsupportedPoolNative) HostFreeBytes(string) (int64, error) {
	return 0, newError(CodeUnsupportedPlatform, "host-free", "", nil)
}
func (unsupportedPoolNative) RemoveVHDX(string) error {
	return newError(CodeUnsupportedPlatform, "remove-vhdx", "", nil)
}
