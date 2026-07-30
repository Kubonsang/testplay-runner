//go:build windows

package vhdxprobe

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const (
	virtualStorageTypeDeviceVHDX = 3

	virtualDiskAccessAttachRO = 0x00010000
	virtualDiskAccessAttachRW = 0x00020000
	virtualDiskAccessDetach   = 0x00040000
	virtualDiskAccessGetInfo  = 0x00080000

	createVirtualDiskVersion2 = 2
	openVirtualDiskVersion1   = 1
	attachVirtualDiskVersion1 = 1

	attachVirtualDiskReadOnly      = 0x00000001
	attachVirtualDiskNoDriveLetter = 0x00000002

	getVirtualDiskInfoSize           = 1
	getVirtualDiskInfoParentLocation = 3
)

var (
	virtDiskDLL                    = syscall.NewLazyDLL("virtdisk.dll")
	procCreateVirtualDisk          = virtDiskDLL.NewProc("CreateVirtualDisk")
	procOpenVirtualDisk            = virtDiskDLL.NewProc("OpenVirtualDisk")
	procAttachVirtualDisk          = virtDiskDLL.NewProc("AttachVirtualDisk")
	procDetachVirtualDisk          = virtDiskDLL.NewProc("DetachVirtualDisk")
	procGetVirtualDiskPhysicalPath = virtDiskDLL.NewProc("GetVirtualDiskPhysicalPath")
	procGetVirtualDiskInformation  = virtDiskDLL.NewProc("GetVirtualDiskInformation")

	physicalDrivePattern = regexp.MustCompile(`(?i)^\\\\\.\\PhysicalDrive([0-9]+)$`)
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type virtualStorageType struct {
	DeviceID uint32
	VendorID guid
}

// This flattened layout is CREATE_VIRTUAL_DISK_PARAMETERS Version2 on 64-bit
// Windows. The explicit padding matches Windows SDK 10.0.26100.0 virtdisk.h.
type createVirtualDiskParametersV2 struct {
	Version                   uint32
	_                         uint32
	UniqueID                  guid
	MaximumSize               uint64
	BlockSizeInBytes          uint32
	SectorSizeInBytes         uint32
	PhysicalSectorSizeInBytes uint32
	_                         uint32
	ParentPath                *uint16
	SourcePath                *uint16
	OpenFlags                 uint32
	ParentVirtualStorageType  virtualStorageType
	SourceVirtualStorageType  virtualStorageType
	ResiliencyGUID            guid
	_                         uint32
}

type openVirtualDiskParametersV1 struct {
	Version uint32
	RWDepth uint32
}

type attachVirtualDiskParametersV1 struct {
	Version  uint32
	Reserved uint32
}

type virtualDiskHandle uintptr

type virtualDiskSize struct {
	VirtualSize  uint64
	PhysicalSize uint64
	BlockSize    uint32
	SectorSize   uint32
}

var microsoftVirtualStorageVendor = guid{
	Data1: 0xec984aec,
	Data2: 0xa0f9,
	Data3: 0x47e9,
	Data4: [8]byte{0x90, 0x1f, 0x71, 0x41, 0x5a, 0x66, 0x34, 0x5b},
}

func vhdxStorageType() virtualStorageType {
	return virtualStorageType{
		DeviceID: virtualStorageTypeDeviceVHDX,
		VendorID: microsoftVirtualStorageVendor,
	}
}

func ensureVirtDiskAPI() error {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return probeError(
			CodeUnsupportedPlatform,
			"load-virtdisk",
			"",
			fmt.Errorf("the probe requires 64-bit Windows"),
		)
	}
	if err := virtDiskDLL.Load(); err != nil {
		return probeError(CodeVirtDiskAPIUnavailable, "load-virtdisk", "", err)
	}
	for _, proc := range []*syscall.LazyProc{
		procCreateVirtualDisk,
		procOpenVirtualDisk,
		procAttachVirtualDisk,
		procDetachVirtualDisk,
		procGetVirtualDiskPhysicalPath,
		procGetVirtualDiskInformation,
	} {
		if err := proc.Find(); err != nil {
			return probeError(CodeVirtDiskAPIUnavailable, "resolve-virtdisk-api", "", err)
		}
	}
	return nil
}

func createDynamicVHDX(path string, maximumSize int64) error {
	return createVHDX(path, maximumSize, "")
}

func createDifferencingVHDX(path, parentPath string) error {
	return createVHDX(path, 0, parentPath)
}

func createVHDX(path string, maximumSize int64, parentPath string) error {
	if _, err := os.Lstat(path); err == nil {
		code := CodeParentExists
		if parentPath != "" {
			code = CodeChildExists
		}
		return probeError(code, "create-vhdx", path, fmt.Errorf("path already exists"))
	} else if !os.IsNotExist(err) {
		return probeError(CodeParentCreateFailed, "stat-vhdx", path, err)
	}
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return probeError(CodeParentCreateFailed, "encode-vhdx-path", path, err)
	}
	var parentPtr *uint16
	if parentPath != "" {
		parentPtr, err = syscall.UTF16PtrFromString(parentPath)
		if err != nil {
			return probeError(CodeChildCreateFailed, "encode-parent-path", parentPath, err)
		}
	}
	parameters := createVirtualDiskParametersV2{
		Version:                  createVirtualDiskVersion2,
		MaximumSize:              uint64(maximumSize),
		ParentPath:               parentPtr,
		ParentVirtualStorageType: vhdxStorageType(),
	}
	var handle virtualDiskHandle
	status, _, _ := procCreateVirtualDisk.Call(
		uintptr(unsafe.Pointer(&virtualStorageType{
			DeviceID: virtualStorageTypeDeviceVHDX,
			VendorID: microsoftVirtualStorageVendor,
		})),
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&parameters)),
		0,
		uintptr(unsafe.Pointer(&handle)),
	)
	runtime.KeepAlive(pathPtr)
	runtime.KeepAlive(parentPtr)
	runtime.KeepAlive(parameters)
	if status != 0 {
		code := CodeParentCreateFailed
		if parentPath != "" {
			code = CodeChildCreateFailed
		}
		return win32ProbeError(code, "CreateVirtualDisk", path, status)
	}
	if err := closeVirtualDiskHandle(handle); err != nil {
		return probeError(CodeCleanupFailed, "close-created-vhdx", path, err)
	}
	return nil
}

func openVirtualDisk(path string, readOnly bool) (virtualDiskHandle, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	access := uintptr(virtualDiskAccessAttachRW | virtualDiskAccessDetach | virtualDiskAccessGetInfo)
	if readOnly {
		access = virtualDiskAccessAttachRO | virtualDiskAccessDetach | virtualDiskAccessGetInfo
	}
	parameters := openVirtualDiskParametersV1{
		Version: openVirtualDiskVersion1,
		RWDepth: 1,
	}
	storageType := vhdxStorageType()
	var handle virtualDiskHandle
	status, _, _ := procOpenVirtualDisk.Call(
		uintptr(unsafe.Pointer(&storageType)),
		uintptr(unsafe.Pointer(pathPtr)),
		access,
		0,
		uintptr(unsafe.Pointer(&parameters)),
		uintptr(unsafe.Pointer(&handle)),
	)
	runtime.KeepAlive(pathPtr)
	if status != 0 {
		return 0, win32ProbeError(CodeChildAttachFailed, "OpenVirtualDisk", path, status)
	}
	return handle, nil
}

func attachVirtualDisk(handle virtualDiskHandle, path string, readOnly bool) error {
	flags := uintptr(attachVirtualDiskNoDriveLetter)
	if readOnly {
		flags |= attachVirtualDiskReadOnly
	}
	parameters := attachVirtualDiskParametersV1{Version: attachVirtualDiskVersion1}
	status, _, _ := procAttachVirtualDisk.Call(
		uintptr(handle),
		0,
		flags,
		0,
		uintptr(unsafe.Pointer(&parameters)),
		0,
	)
	if status != 0 {
		return win32ProbeError(CodeChildAttachFailed, "AttachVirtualDisk", path, status)
	}
	return nil
}

func detachVirtualDisk(handle virtualDiskHandle, path string) error {
	status, _, _ := procDetachVirtualDisk.Call(uintptr(handle), 0, 0)
	if status != 0 {
		return win32ProbeError(CodeDetachFailed, "DetachVirtualDisk", path, status)
	}
	return nil
}

func closeVirtualDiskHandle(handle virtualDiskHandle) error {
	if handle == 0 {
		return nil
	}
	return syscall.CloseHandle(syscall.Handle(handle))
}

func getVirtualDiskPhysicalPath(handle virtualDiskHandle, path string) (string, error) {
	buffer := make([]uint16, 1024)
	sizeBytes := uint32(len(buffer) * 2)
	status, _, _ := procGetVirtualDiskPhysicalPath.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&sizeBytes)),
		uintptr(unsafe.Pointer(&buffer[0])),
	)
	if status != 0 {
		return "", win32ProbeError(
			CodeUnsafePhysicalDisk,
			"GetVirtualDiskPhysicalPath",
			path,
			status,
		)
	}
	physicalPath := syscall.UTF16ToString(buffer)
	if !physicalDrivePattern.MatchString(physicalPath) {
		return "", probeError(
			CodeUnsafePhysicalDisk,
			"validate-physical-path",
			physicalPath,
			fmt.Errorf("unexpected virtual disk physical path"),
		)
	}
	return physicalPath, nil
}

func getVirtualDiskSize(handle virtualDiskHandle, path string) (virtualDiskSize, error) {
	buffer := make([]byte, 64)
	*(*uint32)(unsafe.Pointer(&buffer[0])) = getVirtualDiskInfoSize
	size := uint32(len(buffer))
	var used uint32
	status, _, _ := procGetVirtualDiskInformation.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&size)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&used)),
	)
	if status != 0 {
		return virtualDiskSize{}, win32ProbeError(
			CodeVerificationFailed,
			"GetVirtualDiskInformation(size)",
			path,
			status,
		)
	}
	return virtualDiskSize{
		VirtualSize:  *(*uint64)(unsafe.Pointer(&buffer[8])),
		PhysicalSize: *(*uint64)(unsafe.Pointer(&buffer[16])),
		BlockSize:    *(*uint32)(unsafe.Pointer(&buffer[24])),
		SectorSize:   *(*uint32)(unsafe.Pointer(&buffer[28])),
	}, nil
}

func getVirtualDiskParent(handle virtualDiskHandle, path string) (string, bool, error) {
	buffer := make([]byte, 64<<10)
	*(*uint32)(unsafe.Pointer(&buffer[0])) = getVirtualDiskInfoParentLocation
	size := uint32(len(buffer))
	var used uint32
	status, _, _ := procGetVirtualDiskInformation.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&size)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&used)),
	)
	if status != 0 {
		return "", false, win32ProbeError(
			CodeVerificationFailed,
			"GetVirtualDiskInformation(parent)",
			path,
			status,
		)
	}
	resolved := *(*uint32)(unsafe.Pointer(&buffer[8])) != 0
	words := unsafe.Slice((*uint16)(unsafe.Pointer(&buffer[12])), (len(buffer)-12)/2)
	parent := syscall.UTF16ToString(words)
	return filepath.Clean(parent), resolved, nil
}

func verifyDifferencingParent(handle virtualDiskHandle, child, expectedParent string) error {
	parent, resolved, err := getVirtualDiskParent(handle, child)
	if err != nil {
		return err
	}
	if !resolved || !strings.EqualFold(parent, filepath.Clean(expectedParent)) {
		return probeError(
			CodeVerificationFailed,
			"verify-differencing-parent",
			child,
			fmt.Errorf("resolved=%t parent=%q expected=%q", resolved, parent, expectedParent),
		)
	}
	return nil
}

func win32ProbeError(code, operation, path string, status uintptr) error {
	return &Error{
		Code:      code,
		Operation: operation,
		Path:      path,
		Win32Code: uint32(status),
		Cause:     syscall.Errno(status),
	}
}
