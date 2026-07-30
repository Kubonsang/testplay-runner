//go:build windows

package shadow

import "io/fs"

func allocatedFileBytes(info fs.FileInfo) int64 {
	return info.Size()
}
