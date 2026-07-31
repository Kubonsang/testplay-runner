//go:build !windows

package unityvhdxfixture

import (
	"fmt"
	"os"
)

func physicalPathIsReparse(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSymlink != 0, nil
}

func resolvePhysicalLibrarySourceRoot(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fixtureError(CodePhysicalLibraryDangling, "inspect-source-root", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fixtureError(CodePhysicalLibraryIsReparse, "verify-source-volume-mount", path, fmt.Errorf("only Windows volume mounts may be dereferenced"))
	}
	if !info.IsDir() {
		return "", fixtureError(CodePhysicalLibraryNotDirectory, "inspect-source-root", path, fmt.Errorf("source is not a directory"))
	}
	return path, nil
}
