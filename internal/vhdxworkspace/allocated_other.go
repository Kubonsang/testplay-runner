//go:build !windows

package vhdxworkspace

import "os"

func fileAllocatedBytes(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
