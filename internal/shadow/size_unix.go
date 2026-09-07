//go:build !windows

package shadow

import (
	"io/fs"
	"syscall"
)

func measureDirectoryUsage(root string) (DirectoryUsage, error) {
	return measureDirectoryUsageByWalk(root)
}

func allocatedFileBytes(_ string, info fs.FileInfo) (int64, error) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Blocks * 512, nil
	}
	return info.Size(), nil
}
