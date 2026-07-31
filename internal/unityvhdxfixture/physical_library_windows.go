//go:build windows

package unityvhdxfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const fileAttributeReparsePoint = 0x400

var (
	physicalKernel32                    = syscall.NewLazyDLL("kernel32.dll")
	physicalGetFileAttributesW          = physicalKernel32.NewProc("GetFileAttributesW")
	physicalGetVolumeNameForMountPointW = physicalKernel32.NewProc("GetVolumeNameForVolumeMountPointW")
)

func physicalPathIsReparse(path string) (bool, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, _, callErr := physicalGetFileAttributesW.Call(uintptr(unsafe.Pointer(pathPtr)))
	runtime.KeepAlive(pathPtr)
	if uint32(attributes) == uint32(0xffffffff) {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return false, errno
		}
		return false, fmt.Errorf("GetFileAttributesW failed")
	}
	return uint32(attributes)&fileAttributeReparsePoint != 0, nil
}

func resolvePhysicalLibrarySourceRoot(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fixtureError(CodePhysicalLibraryDangling, "inspect-source-root", path, err)
	}
	reparse, err := physicalPathIsReparse(path)
	if err != nil {
		return "", fixtureError(CodePhysicalLibraryDangling, "inspect-source-root", path, err)
	}
	if !reparse && info.Mode()&os.ModeSymlink == 0 {
		if !info.IsDir() {
			return "", fixtureError(CodePhysicalLibraryNotDirectory, "inspect-source-root", path, fmt.Errorf("source is not a directory"))
		}
		return path, nil
	}

	mountPath := filepath.Clean(path)
	if !strings.HasSuffix(mountPath, `\`) {
		mountPath += `\`
	}
	mountPtr, err := syscall.UTF16PtrFromString(mountPath)
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, 1024)
	ok, _, callErr := physicalGetVolumeNameForMountPointW.Call(
		uintptr(unsafe.Pointer(mountPtr)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
	)
	runtime.KeepAlive(mountPtr)
	if ok == 0 {
		return "", fixtureError(CodePhysicalLibraryIsReparse, "verify-source-volume-mount", path, fmt.Errorf("root reparse point is not a volume mount: %w", callErr))
	}
	volumeRoot := syscall.UTF16ToString(buffer)
	if !strings.HasPrefix(strings.ToLower(volumeRoot), `\\?\volume{`) || !strings.HasSuffix(volumeRoot, `\`) {
		return "", fixtureError(CodePhysicalLibraryIsReparse, "verify-source-volume-mount", path, fmt.Errorf("unexpected volume path %q", volumeRoot))
	}
	return volumeRoot, nil
}
