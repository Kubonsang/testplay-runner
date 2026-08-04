//go:build !windows

package refsworkspace

import "context"

type unsupportedWorkerStorageMeter struct{}

func newNativeWorkerStorageMeter(Paths) WorkerStorageMeter { return unsupportedWorkerStorageMeter{} }
func (unsupportedWorkerStorageMeter) VolumeUsedBytes(context.Context) (int64, error) {
	return 0, newError(CodeUnsupportedPlatform, "measure-refs-used", "", nil)
}
func (unsupportedWorkerStorageMeter) HostFreeBytes(context.Context) (int64, error) {
	return 0, newError(CodeUnsupportedPlatform, "measure-host-free", "", nil)
}
