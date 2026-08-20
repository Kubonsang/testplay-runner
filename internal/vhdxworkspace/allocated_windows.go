//go:build windows

package vhdxworkspace

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32Allocated           = windows.NewLazySystemDLL("kernel32.dll")
	procGetCompressedFileSizeW2 = kernel32Allocated.NewProc("GetCompressedFileSizeW")
)

func fileAllocatedBytes(path string) (int64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var high uint32
	low, _, callErr := procGetCompressedFileSizeW2.Call(uintptr(unsafe.Pointer(pathPtr)), uintptr(unsafe.Pointer(&high)))
	runtime.KeepAlive(pathPtr)
	if low == 0xffffffff {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return 0, errno
		}
	}
	return int64((uint64(high) << 32) | uint64(uint32(low))), nil
}
