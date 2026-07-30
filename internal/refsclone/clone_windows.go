//go:build windows

package refsclone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileSupportsBlockRefcounting = 0x08000000

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procGetDiskFreeSpaceW     = kernel32.NewProc("GetDiskFreeSpaceW")
	procGetCompressedFileSize = kernel32.NewProc("GetCompressedFileSizeW")
)

// duplicateExtentsData mirrors the documented WinIoCtl.h
// DUPLICATE_EXTENTS_DATA structure: HANDLE followed by three LARGE_INTEGERs.
type duplicateExtentsData struct {
	FileHandle       windows.Handle
	SourceFileOffset int64
	TargetFileOffset int64
	ByteCount        int64
}

// fileStandardInfo mirrors FILE_STANDARD_INFO from winbase.h.
type fileStandardInfo struct {
	AllocationSize int64
	EndOfFile      int64
	NumberOfLinks  uint32
	DeletePending  byte
	Directory      byte
	_              [2]byte
}

func probePlatform(ctx context.Context, root string) (Capability, error) {
	volume, err := inspectVolumeWindows(root)
	if err != nil {
		return Capability{}, err
	}
	capability := Capability{
		Filesystem:   volume.Filesystem,
		SameVolume:   true,
		ClusterSize:  volume.ClusterSize,
		VolumeSerial: volume.Serial,
		Evidence: []string{
			"filesystem=" + volume.Filesystem,
			fmt.Sprintf("volumeSerial=%08x", volume.Serial),
			fmt.Sprintf("clusterSize=%d", volume.ClusterSize),
		},
	}
	if !strings.EqualFold(volume.Filesystem, "ReFS") {
		capability.UnsupportedReason = "volume filesystem is not ReFS"
		return capability, &Error{Code: CodeUnsupportedFilesystem, Operation: "probe", Path: root, Cause: errors.New(capability.UnsupportedReason)}
	}
	if !volume.SupportsClone {
		capability.UnsupportedReason = "FILE_SUPPORTS_BLOCK_REFCOUNTING is absent"
		return capability, &Error{Code: CodeControlUnsupported, Operation: "probe", Path: root, Cause: errors.New(capability.UnsupportedReason)}
	}
	if err := ctx.Err(); err != nil {
		return capability, cancelled("probe", root, err)
	}

	fixtureDir, err := os.MkdirTemp(root, "testplay-refs-capability-")
	if err != nil {
		return capability, classifyWindowsError("create-probe-directory", root, err)
	}
	cleanup := func() error { return os.RemoveAll(fixtureDir) }
	source := filepath.Join(fixtureDir, SourceFixtureName)
	destination := filepath.Join(fixtureDir, CloneFixtureName)
	pattern := bytes.Repeat([]byte{0x54, 0x50, 0x52, 0x46}, int(volume.ClusterSize)/4)
	if err := os.WriteFile(source, pattern, 0600); err != nil {
		_ = cleanup()
		return capability, classifyWindowsError("write-probe-source", source, err)
	}
	result, cloneErr := cloneFileWindows(ctx, Request{
		SourcePath: source, DestinationPath: destination, Length: int64(volume.ClusterSize),
	}, volume)
	if cloneErr == nil {
		sourceData, sourceErr := os.ReadFile(source)
		destinationData, destinationErr := os.ReadFile(destination)
		if sourceErr != nil || destinationErr != nil || !bytes.Equal(sourceData, destinationData) {
			cloneErr = &Error{Code: CodeVerificationFailed, Operation: "probe-byte-parity", Path: fixtureDir}
		}
	}
	cleanupErr := cleanup()
	if cloneErr != nil {
		return capability, cloneErr
	}
	if cleanupErr != nil {
		return capability, &Error{Code: CodeCleanup, Operation: "probe-cleanup", Path: fixtureDir, Cause: cleanupErr}
	}
	capability.Supported = true
	capability.Evidence = append(capability.Evidence,
		"DeviceIoControl="+result.ControlCodeUsed,
		fmt.Sprintf("bytesCloned=%d", result.BytesCloned),
		"byteParity=true",
		"cleanup=true",
	)
	return capability, nil
}

func cloneFilePlatform(ctx context.Context, request Request) (*Result, error) {
	sourceVolume, err := inspectVolumeWindows(request.SourcePath)
	if err != nil {
		return nil, err
	}
	destinationVolume, err := inspectVolumeWindows(filepath.Dir(request.DestinationPath))
	if err != nil {
		return nil, err
	}
	if sourceVolume.Serial != destinationVolume.Serial ||
		!strings.EqualFold(sourceVolume.Root, destinationVolume.Root) {
		return nil, &Error{Code: CodeDifferentVolume, Operation: "clone", Path: request.DestinationPath}
	}
	if !strings.EqualFold(sourceVolume.Filesystem, "ReFS") || !sourceVolume.SupportsClone {
		return nil, &Error{Code: CodeUnsupportedFilesystem, Operation: "clone", Path: request.DestinationPath, Cause: fmt.Errorf("filesystem=%s blockRefcounting=%t", sourceVolume.Filesystem, sourceVolume.SupportsClone)}
	}
	return cloneFileWindows(ctx, request, sourceVolume)
}

func cloneFileWindows(ctx context.Context, request Request, volume VolumeInfo) (*Result, error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, &Error{Code: CodeUnsupportedPlatform, Operation: "clone", Cause: fmt.Errorf("unsupported Windows architecture %s", runtime.GOARCH)}
	}
	if unsafe.Sizeof(duplicateExtentsData{}) != 32 {
		return nil, &Error{Code: CodeUnsupportedPlatform, Operation: "clone", Cause: fmt.Errorf("unexpected DUPLICATE_EXTENTS_DATA size %d", unsafe.Sizeof(duplicateExtentsData{}))}
	}
	if unsafe.Sizeof(fileStandardInfo{}) != 24 {
		return nil, &Error{Code: CodeUnsupportedPlatform, Operation: "clone", Cause: fmt.Errorf("unexpected FILE_STANDARD_INFO size %d", unsafe.Sizeof(fileStandardInfo{}))}
	}
	if err := ValidateRequest(request, volume.ClusterSize); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(request.DestinationPath); err == nil {
		return nil, &Error{Code: CodeDestinationExists, Operation: "clone", Path: request.DestinationPath}
	} else if !os.IsNotExist(err) {
		return nil, classifyWindowsError("inspect-destination", request.DestinationPath, err)
	}
	source, err := os.Open(request.SourcePath)
	if err != nil {
		return nil, classifyWindowsError("open-source", request.SourcePath, err)
	}
	defer source.Close()
	sourceInfo, err := source.Stat()
	if err != nil {
		return nil, classifyWindowsError("stat-source", request.SourcePath, err)
	}
	if request.SourceOffset+request.Length > sourceInfo.Size() {
		return nil, &Error{Code: CodeInvalidLength, Operation: "clone", Path: request.SourcePath, Cause: errors.New("source range exceeds file size")}
	}
	if err := ctx.Err(); err != nil {
		return nil, cancelled("clone", request.DestinationPath, err)
	}
	destination, err := os.OpenFile(request.DestinationPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, classifyWindowsError("create-destination", request.DestinationPath, err)
	}
	keep := false
	defer func() {
		_ = destination.Close()
		if !keep {
			_ = os.Remove(request.DestinationPath)
		}
	}()
	if err := destination.Truncate(request.DestinationOffset + request.Length); err != nil {
		return nil, classifyWindowsError("set-destination-length", request.DestinationPath, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, cancelled("clone", request.DestinationPath, err)
	}
	data := duplicateExtentsData{
		FileHandle:       windows.Handle(source.Fd()),
		SourceFileOffset: request.SourceOffset,
		TargetFileOffset: request.DestinationOffset,
		ByteCount:        request.Length,
	}
	var returned uint32
	started := time.Now()
	err = windows.DeviceIoControl(
		windows.Handle(destination.Fd()),
		windows.FSCTL_DUPLICATE_EXTENTS_TO_FILE,
		(*byte)(unsafe.Pointer(&data)),
		uint32(unsafe.Sizeof(data)),
		nil, 0, &returned, nil,
	)
	duration := time.Since(started)
	if err != nil {
		return &Result{
			ControlCodeUsed: ControlDuplicateExtents,
			Duration:        duration, DurationMs: duration.Milliseconds(),
			ClusterSize: volume.ClusterSize,
		}, classifyWindowsError("DeviceIoControl("+ControlDuplicateExtents+")", request.DestinationPath, err)
	}
	keep = true
	return &Result{
		ControlCodeUsed: ControlDuplicateExtents,
		BytesCloned:     request.Length,
		Duration:        duration, DurationMs: duration.Milliseconds(),
		ClusterSize: volume.ClusterSize,
	}, nil
}

func inspectVolumePlatform(path string) (VolumeInfo, error) {
	return inspectVolumeWindows(path)
}

func inspectVolumeWindows(path string) (VolumeInfo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return VolumeInfo{}, classifyWindowsError("absolute-path", path, err)
	}
	pathPtr, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return VolumeInfo{}, classifyWindowsError("encode-path", path, err)
	}
	var rootBuffer [windows.MAX_PATH + 1]uint16
	if err := windows.GetVolumePathName(pathPtr, &rootBuffer[0], uint32(len(rootBuffer))); err != nil {
		return VolumeInfo{}, classifyWindowsError("get-volume-path", path, err)
	}
	root := windows.UTF16ToString(rootBuffer[:])
	rootPtr, _ := windows.UTF16PtrFromString(root)
	var serial, flags uint32
	var fsBuffer [32]uint16
	if err := windows.GetVolumeInformation(rootPtr, nil, 0, &serial, nil, &flags, &fsBuffer[0], uint32(len(fsBuffer))); err != nil {
		return VolumeInfo{}, classifyWindowsError("get-volume-information", path, err)
	}
	var sectorsPerCluster, bytesPerSector, freeClusters, totalClusters uint32
	r1, _, callErr := procGetDiskFreeSpaceW.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		uintptr(unsafe.Pointer(&sectorsPerCluster)),
		uintptr(unsafe.Pointer(&bytesPerSector)),
		uintptr(unsafe.Pointer(&freeClusters)),
		uintptr(unsafe.Pointer(&totalClusters)),
	)
	if r1 == 0 {
		return VolumeInfo{}, classifyWindowsError("get-disk-free-space", root, callErr)
	}
	var freeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(rootPtr, nil, nil, &freeBytes); err != nil {
		return VolumeInfo{}, classifyWindowsError("get-disk-free-space-ex", root, err)
	}
	return VolumeInfo{
		Root:          root,
		Filesystem:    windows.UTF16ToString(fsBuffer[:]),
		Serial:        serial,
		ClusterSize:   uint64(sectorsPerCluster) * uint64(bytesPerSector),
		FreeBytes:     freeBytes,
		SupportsClone: flags&fileSupportsBlockRefcounting != 0,
	}, nil
}

func measureFilePlatform(path string) (FileMeasurement, error) {
	file, err := os.Open(path)
	if err != nil {
		return FileMeasurement{}, classifyWindowsError("open-measurement", path, err)
	}
	defer file.Close()
	var standard fileStandardInfo
	if err := windows.GetFileInformationByHandleEx(
		windows.Handle(file.Fd()),
		windows.FileStandardInfo,
		(*byte)(unsafe.Pointer(&standard)),
		uint32(unsafe.Sizeof(standard)),
	); err != nil {
		return FileMeasurement{}, classifyWindowsError("get-file-standard-info", path, err)
	}
	pathPtr, _ := windows.UTF16PtrFromString(path)
	var high uint32
	low, _, callErr := procGetCompressedFileSize.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&high)),
	)
	if uint32(low) == 0xffffffff && callErr != syscall.Errno(0) {
		return FileMeasurement{}, classifyWindowsError("get-compressed-file-size", path, callErr)
	}
	return FileMeasurement{
		LogicalBytes:    standard.EndOfFile,
		AllocationBytes: standard.AllocationSize,
		CompressedBytes: uint64(high)<<32 | uint64(uint32(low)),
	}, nil
}

func classifyWindowsError(operation, path string, err error) error {
	code := CodeDeviceIOControl
	if errors.Is(err, os.ErrNotExist) {
		code = CodeSourceNotFound
	} else if errors.Is(err, os.ErrPermission) {
		code = CodeAccessDenied
	} else if errors.Is(err, windows.ERROR_NOT_SUPPORTED) ||
		errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		code = CodeControlUnsupported
	} else if errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		code = CodeFileLocked
	}
	var errno syscall.Errno
	var win32 uint32
	if errors.As(err, &errno) {
		win32 = uint32(errno)
	}
	return &Error{Code: code, Operation: operation, Path: path, Win32Code: win32, Cause: err}
}
