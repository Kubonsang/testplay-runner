//go:build windows

package mountedcopy

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
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	getFileAttributesW          = kernel32.NewProc("GetFileAttributesW")
	getVolumeNameForMountPointW = kernel32.NewProc("GetVolumeNameForVolumeMountPointW")
)

func IsReparsePoint(path string) (bool, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, _, callErr := getFileAttributesW.Call(uintptr(unsafe.Pointer(pathPtr)))
	runtime.KeepAlive(pathPtr)
	if uint32(attributes) == uint32(0xffffffff) {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return false, errno
		}
		return false, fmt.Errorf("GetFileAttributesW failed")
	}
	return uint32(attributes)&fileAttributeReparsePoint != 0, nil
}

func resolveSourceRoot(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", newError(CodeInvalidSource, "inspect-source-root", path, err)
	}
	reparse, err := IsReparsePoint(path)
	if err != nil {
		return "", newError(CodeInvalidSource, "inspect-source-root", path, err)
	}
	if !reparse && info.Mode()&os.ModeSymlink == 0 {
		if !info.IsDir() {
			return "", newError(CodeSourceNotDirectory, "inspect-source-root", path, fmt.Errorf("source is not a directory"))
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
	ok, _, callErr := getVolumeNameForMountPointW.Call(uintptr(unsafe.Pointer(mountPtr)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	runtime.KeepAlive(mountPtr)
	if ok == 0 {
		return "", newError(CodeRootNotVolumeMount, "verify-source-volume-mount", path, fmt.Errorf("root reparse point is not a volume mount: %w", callErr))
	}
	volumeRoot := syscall.UTF16ToString(buffer)
	if !strings.HasPrefix(strings.ToLower(volumeRoot), `\\?\volume{`) || !strings.HasSuffix(volumeRoot, `\`) {
		return "", newError(CodeRootNotVolumeMount, "verify-source-volume-mount", path, fmt.Errorf("unexpected volume path %q", volumeRoot))
	}
	return volumeRoot, nil
}
