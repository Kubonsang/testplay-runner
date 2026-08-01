//go:build !windows

package mountedcopy

import (
	"fmt"
	"os"
)

func IsReparsePoint(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSymlink != 0, nil
}

func resolveSourceRoot(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", newError(CodeInvalidSource, "inspect-source-root", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", newError(CodeRootNotVolumeMount, "verify-source-volume-mount", path, fmt.Errorf("only Windows volume mounts may be dereferenced"))
	}
	if !info.IsDir() {
		return "", newError(CodeSourceNotDirectory, "inspect-source-root", path, fmt.Errorf("source is not a directory"))
	}
	return path, nil
}
