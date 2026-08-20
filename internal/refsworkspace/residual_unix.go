//go:build !windows

package refsworkspace

import "os"

func inspectUnmountedMountPath(path string) (int, int, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	reparse := 0
	if info.Mode()&os.ModeSymlink != 0 {
		reparse = 1
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, 0, err
	}
	return reparse, len(entries), nil
}
