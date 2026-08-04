//go:build windows

package refsworkspace

import (
	"context"

	"golang.org/x/sys/windows"
)

type nativeWorkerStorageMeter struct{ paths Paths }

func newNativeWorkerStorageMeter(paths Paths) WorkerStorageMeter {
	return nativeWorkerStorageMeter{paths: paths}
}

func (meter nativeWorkerStorageMeter) VolumeUsedBytes(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	_, total, free, err := diskFreeSpace(meter.paths.Mount)
	if err != nil {
		return 0, err
	}
	return int64(total - free), nil
}

func (meter nativeWorkerStorageMeter) HostFreeBytes(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	available, _, _, err := diskFreeSpace(meter.paths.Root)
	return int64(available), err
}

func diskFreeSpace(path string) (available, total, free uint64, returnErr error) {
	value, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, 0, err
	}
	err = windows.GetDiskFreeSpaceEx(value, &available, &total, &free)
	return available, total, free, err
}
