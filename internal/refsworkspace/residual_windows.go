//go:build windows

package refsworkspace

import "os"

func inspectUnmountedMountPath(path string) (int, int, error) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return 0, 0, nil
	} else if err != nil {
		return 0, 0, err
	}
	reparse, err := pathIsReparsePoint(path)
	if err != nil {
		return 0, 0, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, 0, err
	}
	count := 0
	if reparse {
		count = 1
	}
	return count, len(entries), nil
}
