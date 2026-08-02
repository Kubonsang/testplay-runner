package shadow

import (
	"io/fs"
	"path/filepath"
)

// DirectoryUsage separates logical file sizes from filesystem-allocated bytes.
// AllocatedBytes falls back to logical size on platforms that do not expose
// block allocation.
type DirectoryUsage struct {
	LogicalBytes   int64
	AllocatedBytes int64
}

// MeasureDirectoryUsage walks regular files under root once and reports both
// their logical size and their filesystem allocation.
func MeasureDirectoryUsage(root string) (DirectoryUsage, error) {
	var usage DirectoryUsage
	err := filepath.Walk(root, func(_ string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			usage.LogicalBytes += info.Size()
			usage.AllocatedBytes += allocatedFileBytes(info)
		}
		return nil
	})
	return usage, err
}

// DirectoryAllocatedBytes returns the filesystem blocks allocated by regular
// files under root when the platform exposes that information. On platforms
// without block counts it falls back to logical file size.
func DirectoryAllocatedBytes(root string) (int64, error) {
	usage, err := MeasureDirectoryUsage(root)
	return usage.AllocatedBytes, err
}
