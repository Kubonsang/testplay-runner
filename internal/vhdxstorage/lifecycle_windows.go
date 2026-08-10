//go:build windows

package vhdxstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	virtualStorageTypeDeviceVHDX     = 3
	virtualDiskAccessAttachRO        = 0x00010000
	virtualDiskAccessAttachRW        = 0x00020000
	virtualDiskAccessDetach          = 0x00040000
	virtualDiskAccessGetInfo         = 0x00080000
	createVirtualDiskVersion2        = 2
	openVirtualDiskVersion1          = 1
	attachVirtualDiskVersion1        = 1
	attachVirtualDiskReadOnly        = 0x00000001
	attachVirtualDiskNoDriveLetter   = 0x00000002
	getVirtualDiskInfoSize           = 1
	getVirtualDiskInfoParentLocation = 3
	getVirtualDiskInfoVirtualDiskID  = 14
)

var (
	virtDiskDLL                    = syscall.NewLazyDLL("virtdisk.dll")
	procCreateVirtualDisk          = virtDiskDLL.NewProc("CreateVirtualDisk")
	procOpenVirtualDisk            = virtDiskDLL.NewProc("OpenVirtualDisk")
	procAttachVirtualDisk          = virtDiskDLL.NewProc("AttachVirtualDisk")
	procDetachVirtualDisk          = virtDiskDLL.NewProc("DetachVirtualDisk")
	procGetVirtualDiskPhysicalPath = virtDiskDLL.NewProc("GetVirtualDiskPhysicalPath")
	procGetVirtualDiskInformation  = virtDiskDLL.NewProc("GetVirtualDiskInformation")
	kernel32DLL                    = syscall.NewLazyDLL("kernel32.dll")
	procGetCompressedFileSizeW     = kernel32DLL.NewProc("GetCompressedFileSizeW")
	physicalDrivePattern           = regexp.MustCompile(`(?i)^\\\\\.\\PhysicalDrive([0-9]+)$`)
)

const adminCheckScript = `
$principal = [Security.Principal.WindowsPrincipal](
  [Security.Principal.WindowsIdentity]::GetCurrent()
)
$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
`

const initializeDiskScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$mountPath = $env:TESTPLAY_VHDX_MOUNT_PATH.TrimEnd('\') + '\'
$mountItem = Get-Item -LiteralPath $mountPath -Force -ErrorAction Stop
if (($mountItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
  throw "mount path is a reparse point: $mountPath"
}
if (@(Get-ChildItem -LiteralPath $mountPath -Force).Count -ne 0) {
  throw "mount path is not empty: $mountPath"
}
$hostVolume = Get-Volume -FilePath $mountItem.FullName -ErrorAction Stop
if ($hostVolume.FileSystemType.ToString() -ne 'NTFS') {
  throw "mount host must be NTFS; found $($hostVolume.FileSystemType)"
}
$deadline = [DateTime]::UtcNow.AddSeconds(15)
$started = [Diagnostics.Stopwatch]::StartNew()
do {
  $disks = @(Get-Disk -Number $diskNumber -ErrorAction SilentlyContinue)
  if ($disks.Count -eq 1) { break }
  Start-Sleep -Milliseconds 200
} while ([DateTime]::UtcNow -lt $deadline)
$pnpMs = $started.ElapsedMilliseconds
if ($disks.Count -ne 1) { throw "expected exactly one disk $diskNumber; found $($disks.Count)" }
$disk = $disks[0]
if ($disk.BusType.ToString() -ne 'File Backed Virtual') {
  throw "unsafe bus type for disk ${diskNumber}: $($disk.BusType)"
}
if ($disk.PartitionStyle.ToString() -ne 'RAW') {
  throw "new parent disk is not RAW: $($disk.PartitionStyle)"
}
$existing = @(Get-Partition -DiskNumber $diskNumber -ErrorAction SilentlyContinue)
if ($existing.Count -ne 0) { throw 'new parent disk already has partitions' }
if ($disk.IsReadOnly) { Set-Disk -Number $diskNumber -IsReadOnly $false }
if ($disk.IsOffline) { Set-Disk -Number $diskNumber -IsOffline $false }
Initialize-Disk -Number $diskNumber -PartitionStyle GPT -PassThru | Out-Null
$partition = New-Partition -DiskNumber $diskNumber -UseMaximumSize
Format-Volume -Partition $partition -FileSystem NTFS -NewFileSystemLabel 'TestPlayVHDXProbe' -Confirm:$false -Force | Out-Null
$volume = Get-Volume -Partition $partition
Add-PartitionAccessPath -InputObject $partition -AccessPath $mountPath
[pscustomobject]@{
  partitionNumber = $partition.PartitionNumber
  volumeGuidPath = $volume.Path
  pnpDiscoveryWaitMs = $pnpMs
  volumeReadyWaitMs = $started.ElapsedMilliseconds - $pnpMs
} | ConvertTo-Json -Compress
`

const resolveVolumeScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$readOnly = $env:TESTPLAY_VHDX_READ_ONLY -eq 'true'
$deadline = [DateTime]::UtcNow.AddSeconds(15)
$started = [Diagnostics.Stopwatch]::StartNew()
do {
  $disks = @(Get-Disk -Number $diskNumber -ErrorAction SilentlyContinue)
  if ($disks.Count -eq 1) { break }
  Start-Sleep -Milliseconds 200
} while ([DateTime]::UtcNow -lt $deadline)
$pnpMs = $started.ElapsedMilliseconds
if ($disks.Count -ne 1) { throw "expected exactly one disk $diskNumber; found $($disks.Count)" }
$disk = $disks[0]
if ($disk.BusType.ToString() -ne 'File Backed Virtual') {
  throw "unsafe bus type for disk ${diskNumber}: $($disk.BusType)"
}
if ($disk.PartitionStyle.ToString() -ne 'GPT') {
  throw "attached disk is not GPT: $($disk.PartitionStyle)"
}
if ($disk.IsOffline) { Set-Disk -Number $diskNumber -IsOffline $false }
if (-not $readOnly -and $disk.IsReadOnly) { Set-Disk -Number $diskNumber -IsReadOnly $false }
$dataGuid = [guid]'EBD0A0A2-B9E5-4433-87C0-68B6B72699C7'
do {
  $partitions = @(Get-Partition -DiskNumber $diskNumber -ErrorAction SilentlyContinue)
  $dataPartitions = @($partitions | Where-Object { [guid]$_.GptType -eq $dataGuid })
  if ($dataPartitions.Count -eq 1) {
    $volume = Get-Volume -Partition $dataPartitions[0] -ErrorAction SilentlyContinue
    if ($null -ne $volume -and -not [string]::IsNullOrWhiteSpace($volume.Path)) { break }
  }
  Start-Sleep -Milliseconds 200
} while ([DateTime]::UtcNow -lt $deadline)
if ($dataPartitions.Count -ne 1) { throw "expected one basic data partition; found $($dataPartitions.Count)" }
if ($null -eq $volume -or [string]::IsNullOrWhiteSpace($volume.Path)) { throw 'volume was not ready' }
[pscustomobject]@{
  partitionNumber = $dataPartitions[0].PartitionNumber
  volumeGuidPath = $volume.Path
  pnpDiscoveryWaitMs = $pnpMs
  volumeReadyWaitMs = $started.ElapsedMilliseconds - $pnpMs
} | ConvertTo-Json -Compress
`

const mountDiskScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$partitionNumber = [int]$env:TESTPLAY_VHDX_PARTITION_NUMBER
$mountPath = $env:TESTPLAY_VHDX_MOUNT_PATH.TrimEnd('\') + '\'
$mountItem = Get-Item -LiteralPath $mountPath -Force -ErrorAction Stop
if (($mountItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
  throw "mount path is a reparse point: $mountPath"
}
$hostVolume = Get-Volume -FilePath $mountItem.FullName -ErrorAction Stop
if ($hostVolume.FileSystemType.ToString() -ne 'NTFS') {
  throw "mount host must be NTFS; found $($hostVolume.FileSystemType)"
}
$disk = Get-Disk -Number $diskNumber -ErrorAction Stop
if ($disk.BusType.ToString() -ne 'File Backed Virtual') {
  throw "unsafe bus type for disk ${diskNumber}: $($disk.BusType)"
}
$partition = Get-Partition -DiskNumber $diskNumber -PartitionNumber $partitionNumber -ErrorAction Stop
$items = @(Get-ChildItem -LiteralPath $mountPath -Force)
if ($items.Count -ne 0) { throw "mount path is not empty: $mountPath" }
$call = [Diagnostics.Stopwatch]::StartNew()
Add-PartitionAccessPath -InputObject $partition -AccessPath $mountPath
$callMs = $call.ElapsedMilliseconds
$visible = [Diagnostics.Stopwatch]::StartNew()
$deadline = [DateTime]::UtcNow.AddSeconds(15)
do {
  $current = Get-Partition -DiskNumber $diskNumber -PartitionNumber $partitionNumber -ErrorAction Stop
  if (@($current.AccessPaths) -contains $mountPath) { break }
  Start-Sleep -Milliseconds 100
} while ([DateTime]::UtcNow -lt $deadline)
if (-not (@($current.AccessPaths) -contains $mountPath)) { throw 'mount visibility timeout' }
[pscustomobject]@{
  mountCallMs = $callMs
  mountVisibilityWaitMs = $visible.ElapsedMilliseconds
} | ConvertTo-Json -Compress
`

const unmountDiskScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$partitionNumber = [int]$env:TESTPLAY_VHDX_PARTITION_NUMBER
$mountPath = $env:TESTPLAY_VHDX_MOUNT_PATH.TrimEnd('\') + '\'
$disk = Get-Disk -Number $diskNumber -ErrorAction Stop
if ($disk.BusType.ToString() -ne 'File Backed Virtual') {
  throw "unsafe bus type for disk ${diskNumber}: $($disk.BusType)"
}
$partition = Get-Partition -DiskNumber $diskNumber -PartitionNumber $partitionNumber -ErrorAction Stop
if (-not (@($partition.AccessPaths) -contains $mountPath)) { throw "mount path is not owned by this partition: $mountPath" }
$call = [Diagnostics.Stopwatch]::StartNew()
Remove-PartitionAccessPath -InputObject $partition -AccessPath $mountPath
$callMs = $call.ElapsedMilliseconds
$visible = [Diagnostics.Stopwatch]::StartNew()
$deadline = [DateTime]::UtcNow.AddSeconds(15)
do {
  $current = Get-Partition -DiskNumber $diskNumber -PartitionNumber $partitionNumber -ErrorAction Stop
  if (-not (@($current.AccessPaths) -contains $mountPath)) { break }
  Start-Sleep -Milliseconds 100
} while ([DateTime]::UtcNow -lt $deadline)
if (@($current.AccessPaths) -contains $mountPath) { throw 'unmount visibility timeout' }
[pscustomobject]@{
  unmountCallMs = $callMs
  detachVisibilityWaitMs = $visible.ElapsedMilliseconds
} | ConvertTo-Json -Compress
`

const waitDetachScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$started = [Diagnostics.Stopwatch]::StartNew()
$deadline = [DateTime]::UtcNow.AddSeconds(15)
do {
  $disk = Get-Disk -Number $diskNumber -ErrorAction SilentlyContinue
  if ($null -eq $disk) { break }
  Start-Sleep -Milliseconds 100
} while ([DateTime]::UtcNow -lt $deadline)
if ($null -ne $disk) { throw "disk $diskNumber remained visible after detach" }
[pscustomobject]@{ detachVisibilityWaitMs = $started.ElapsedMilliseconds } | ConvertTo-Json -Compress
`

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

type openVirtualDiskParametersV1 struct{ Version, RWDepth uint32 }
type attachVirtualDiskParametersV1 struct{ Version, Reserved uint32 }
type virtualDiskHandle uintptr

type SizeInfo struct {
	VirtualSize  uint64
	PhysicalSize uint64
	BlockSize    uint32
	SectorSize   uint32
}

var microsoftVirtualStorageVendor = guid{
	Data1: 0xec984aec, Data2: 0xa0f9, Data3: 0x47e9,
	Data4: [8]byte{0x90, 0x1f, 0x71, 0x41, 0x5a, 0x66, 0x34, 0x5b},
}

func vhdxStorageType() virtualStorageType {
	return virtualStorageType{DeviceID: virtualStorageTypeDeviceVHDX, VendorID: microsoftVirtualStorageVendor}
}

func EnsureAvailable() error {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return newError(CodeUnsupportedPlatform, "load-virtdisk", "", fmt.Errorf("64-bit Windows is required"))
	}
	if err := virtDiskDLL.Load(); err != nil {
		return newError(CodeVirtDiskAPIUnavailable, "load-virtdisk", "", err)
	}
	for _, proc := range []*syscall.LazyProc{procCreateVirtualDisk, procOpenVirtualDisk, procAttachVirtualDisk, procDetachVirtualDisk, procGetVirtualDiskPhysicalPath, procGetVirtualDiskInformation} {
		if err := proc.Find(); err != nil {
			return newError(CodeVirtDiskAPIUnavailable, "resolve-virtdisk-api", "", err)
		}
	}
	return nil
}

func IsElevated(ctx context.Context) (bool, error) {
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", adminCheckScript)
	output, err := command.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("administrator check: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.EqualFold(strings.TrimSpace(string(output)), "true"), nil
}

func CreateDynamic(path string, maximumSize int64) error {
	return createVHDX(path, CreateOptions{MaximumSize: maximumSize}, "")
}

func CreateDynamicWithOptions(path string, options CreateOptions) error {
	if options.MaximumSize <= 0 {
		return newError(CodeChildCreateFailed, "validate-create-options", path, fmt.Errorf("maximum size must be positive"))
	}
	if options.BlockSizeInBytes != 0 && (options.BlockSizeInBytes < 1<<20 || options.BlockSizeInBytes > 256<<20 || options.BlockSizeInBytes%(1<<20) != 0) {
		return newError(CodeChildCreateFailed, "validate-create-options", path, fmt.Errorf("VHDX block size must be a 1 MiB multiple between 1 and 256 MiB"))
	}
	if options.SectorSizeInBytes != 0 && options.SectorSizeInBytes != 512 && options.SectorSizeInBytes != 4096 {
		return newError(CodeChildCreateFailed, "validate-create-options", path, fmt.Errorf("sector size must be 512 or 4096"))
	}
	return createVHDX(path, options, "")
}

func CreateDifferencing(path, parentPath string) error {
	return createVHDX(path, CreateOptions{}, parentPath)
}

func createVHDX(path string, options CreateOptions, parentPath string) error {
	if _, err := os.Lstat(path); err == nil {
		return newError(CodeChildExists, "create-vhdx", path, fmt.Errorf("path already exists"))
	} else if !os.IsNotExist(err) {
		return newError(CodeChildCreateFailed, "stat-vhdx", path, err)
	}
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return newError(CodeChildCreateFailed, "encode-vhdx-path", path, err)
	}
	var parentPtr *uint16
	if parentPath != "" {
		parentPtr, err = syscall.UTF16PtrFromString(parentPath)
		if err != nil {
			return newError(CodeChildCreateFailed, "encode-parent-path", parentPath, err)
		}
	}
	parameters := createVirtualDiskParametersV2{
		Version: createVirtualDiskVersion2, MaximumSize: uint64(options.MaximumSize),
		BlockSizeInBytes: options.BlockSizeInBytes, SectorSizeInBytes: options.SectorSizeInBytes,
		PhysicalSectorSizeInBytes: options.SectorSizeInBytes,
		ParentPath:                parentPtr, ParentVirtualStorageType: vhdxStorageType(),
	}
	storageType := vhdxStorageType()
	var handle virtualDiskHandle
	status, _, _ := procCreateVirtualDisk.Call(uintptr(unsafe.Pointer(&storageType)), uintptr(unsafe.Pointer(pathPtr)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&parameters)), 0, uintptr(unsafe.Pointer(&handle)))
	runtime.KeepAlive(pathPtr)
	runtime.KeepAlive(parentPtr)
	runtime.KeepAlive(parameters)
	if status != 0 {
		return win32Error(CodeChildCreateFailed, "CreateVirtualDisk", path, status)
	}
	if err := closeVirtualDiskHandle(handle); err != nil {
		return newError(CodeCleanupFailed, "close-created-vhdx", path, err)
	}
	return nil
}

type Attachment struct {
	mu              sync.Mutex
	handle          virtualDiskHandle
	path            string
	physicalPath    string
	diskNumber      int
	partitionNumber int
	volumeGUIDPath  string
	mountPath       string
	attached        bool
	mounted         bool
}

func Open(path string, readOnly bool) (*Attachment, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, newError(CodeChildOpenFailed, "encode-vhdx-path", path, err)
	}
	access := uintptr(virtualDiskAccessAttachRW | virtualDiskAccessDetach | virtualDiskAccessGetInfo)
	if readOnly {
		access = virtualDiskAccessAttachRO | virtualDiskAccessDetach | virtualDiskAccessGetInfo
	}
	parameters := openVirtualDiskParametersV1{Version: openVirtualDiskVersion1, RWDepth: 1}
	storageType := vhdxStorageType()
	var handle virtualDiskHandle
	status, _, _ := procOpenVirtualDisk.Call(uintptr(unsafe.Pointer(&storageType)), uintptr(unsafe.Pointer(pathPtr)), access, 0, uintptr(unsafe.Pointer(&parameters)), uintptr(unsafe.Pointer(&handle)))
	runtime.KeepAlive(pathPtr)
	if status != 0 {
		return nil, win32Error(CodeChildOpenFailed, "OpenVirtualDisk", path, status)
	}
	return &Attachment{handle: handle, path: path}, nil
}

func OpenAndAttach(path string, readOnly bool) (*Attachment, error) {
	attachment, err := Open(path, readOnly)
	if err != nil {
		return nil, err
	}
	if err := attachment.Attach(readOnly); err != nil {
		_ = attachment.CloseHandle()
		return nil, err
	}
	if _, err := attachment.ResolvePhysicalPath(); err != nil {
		detachErr := attachment.Detach()
		closeErr := attachment.CloseHandle()
		return nil, errors.Join(err, detachErr, closeErr)
	}
	return attachment, nil
}

func (a *Attachment) Attach(readOnly bool) error {
	flags := uintptr(attachVirtualDiskNoDriveLetter)
	if readOnly {
		flags |= attachVirtualDiskReadOnly
	}
	parameters := attachVirtualDiskParametersV1{Version: attachVirtualDiskVersion1}
	status, _, _ := procAttachVirtualDisk.Call(uintptr(a.handle), 0, flags, 0, uintptr(unsafe.Pointer(&parameters)), 0)
	if status != 0 {
		return win32Error(CodeAttachFailed, "AttachVirtualDisk", a.path, status)
	}
	a.attached = true
	return nil
}

func (a *Attachment) ResolvePhysicalPath() (string, error) {
	buffer := make([]uint16, 1024)
	sizeBytes := uint32(len(buffer) * 2)
	status, _, _ := procGetVirtualDiskPhysicalPath.Call(uintptr(a.handle), uintptr(unsafe.Pointer(&sizeBytes)), uintptr(unsafe.Pointer(&buffer[0])))
	if status != 0 {
		return "", win32Error(CodePhysicalPathResolutionFailed, "GetVirtualDiskPhysicalPath", a.path, status)
	}
	physicalPath := syscall.UTF16ToString(buffer)
	number, err := DiskNumberFromPhysicalPath(physicalPath)
	if err != nil {
		return "", err
	}
	a.physicalPath = physicalPath
	a.diskNumber = number
	return physicalPath, nil
}

func (a *Attachment) PhysicalPath() string   { return a.physicalPath }
func (a *Attachment) VolumeGUIDPath() string { return a.volumeGUIDPath }

func (a *Attachment) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.volumeGUIDPath == "" {
		return newError(CodeCleanupFailed, "flush-volume", a.path, fmt.Errorf("volume identity is unavailable"))
	}
	ptr, err := windows.UTF16PtrFromString(strings.TrimSuffix(a.volumeGUIDPath, `\`))
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return newError(CodeCleanupFailed, "open-volume-for-flush", a.volumeGUIDPath, err)
	}
	defer windows.CloseHandle(handle)
	if err := windows.FlushFileBuffers(handle); err != nil {
		return newError(CodeCleanupFailed, "FlushFileBuffers", a.volumeGUIDPath, err)
	}
	return nil
}

func (a *Attachment) Size() (SizeInfo, error) {
	buffer := make([]byte, 64)
	*(*uint32)(unsafe.Pointer(&buffer[0])) = getVirtualDiskInfoSize
	size := uint32(len(buffer))
	var used uint32
	status, _, _ := procGetVirtualDiskInformation.Call(uintptr(a.handle), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&used)))
	if status != 0 {
		return SizeInfo{}, win32Error(CodeParentInvalid, "GetVirtualDiskInformation(size)", a.path, status)
	}
	return SizeInfo{VirtualSize: *(*uint64)(unsafe.Pointer(&buffer[8])), PhysicalSize: *(*uint64)(unsafe.Pointer(&buffer[16])), BlockSize: *(*uint32)(unsafe.Pointer(&buffer[24])), SectorSize: *(*uint32)(unsafe.Pointer(&buffer[28]))}, nil
}

func (a *Attachment) VirtualDiskID() (string, error) {
	buffer := make([]byte, 32)
	*(*uint32)(unsafe.Pointer(&buffer[0])) = getVirtualDiskInfoVirtualDiskID
	size := uint32(len(buffer))
	var used uint32
	status, _, _ := procGetVirtualDiskInformation.Call(uintptr(a.handle), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&used)))
	if status != 0 {
		return "", win32Error(CodeParentInvalid, "GetVirtualDiskInformation(virtual-disk-id)", a.path, status)
	}
	identifier := *(*windows.GUID)(unsafe.Pointer(&buffer[8]))
	return identifier.String(), nil
}

func (a *Attachment) VerifyParent(expectedParent string) error {
	buffer := make([]byte, 64<<10)
	*(*uint32)(unsafe.Pointer(&buffer[0])) = getVirtualDiskInfoParentLocation
	size := uint32(len(buffer))
	var used uint32
	status, _, _ := procGetVirtualDiskInformation.Call(uintptr(a.handle), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&used)))
	if status != 0 {
		return win32Error(CodeParentInvalid, "GetVirtualDiskInformation(parent)", a.path, status)
	}
	resolved := *(*uint32)(unsafe.Pointer(&buffer[8])) != 0
	words := unsafe.Slice((*uint16)(unsafe.Pointer(&buffer[12])), (len(buffer)-12)/2)
	parent := filepath.Clean(syscall.UTF16ToString(words))
	if !resolved || !strings.EqualFold(parent, filepath.Clean(expectedParent)) {
		return newError(CodeParentInvalid, "verify-differencing-parent", a.path, fmt.Errorf("resolved=%t parent=%q expected=%q", resolved, parent, expectedParent))
	}
	return nil
}

type volumeResult struct {
	PartitionNumber    int    `json:"partitionNumber"`
	VolumeGUIDPath     string `json:"volumeGuidPath"`
	PnPDiscoveryWaitMs int64  `json:"pnpDiscoveryWaitMs"`
	VolumeReadyWaitMs  int64  `json:"volumeReadyWaitMs"`
}
type mountResult struct {
	MountCallMs           int64 `json:"mountCallMs"`
	MountVisibilityWaitMs int64 `json:"mountVisibilityWaitMs"`
}
type unmountResult struct {
	UnmountCallMs          int64 `json:"unmountCallMs"`
	DetachVisibilityWaitMs int64 `json:"detachVisibilityWaitMs"`
}
type detachResult struct {
	DetachVisibilityWaitMs int64 `json:"detachVisibilityWaitMs"`
}

func (a *Attachment) InitializeAndMount(ctx context.Context, mountPath string) error {
	var result volumeResult
	if _, err := a.runPowerShell(ctx, initializeDiskScript, mountPath, 0, false, &result); err != nil {
		return newError(CodeMountFailed, "initialize-and-mount", mountPath, err)
	}
	a.partitionNumber = result.PartitionNumber
	a.volumeGUIDPath = result.VolumeGUIDPath
	a.mountPath = mountPath
	a.mounted = true
	return nil
}

func (a *Attachment) ResolveVolume(ctx context.Context, readOnly bool) (volumeResult, int64, error) {
	var result volumeResult
	bootstrap, err := a.runPowerShell(ctx, resolveVolumeScript, "", 0, readOnly, &result)
	if err != nil {
		return result, bootstrap, newError(CodeVolumeResolutionFailed, "resolve-volume", a.path, err)
	}
	a.partitionNumber = result.PartitionNumber
	a.volumeGUIDPath = result.VolumeGUIDPath
	return result, bootstrap, nil
}

func (a *Attachment) Mount(ctx context.Context, mountPath string, readOnly bool) (mountResult, int64, error) {
	var result mountResult
	bootstrap, err := a.runPowerShell(ctx, mountDiskScript, mountPath, a.partitionNumber, readOnly, &result)
	if err != nil {
		code := CodeMountFailed
		if strings.Contains(strings.ToLower(err.Error()), "mount visibility timeout") {
			code = CodeMountVisibilityTimeout
		}
		return result, bootstrap, newError(code, "mount", mountPath, err)
	}
	a.mountPath = mountPath
	a.mounted = true
	return result, bootstrap, nil
}

func (a *Attachment) MountExisting(ctx context.Context, mountPath string, readOnly bool) error {
	if _, _, err := a.ResolveVolume(ctx, readOnly); err != nil {
		return err
	}
	_, _, err := a.Mount(ctx, mountPath, readOnly)
	return err
}

func (a *Attachment) Unmount(ctx context.Context) (unmountResult, int64, error) {
	if !a.mounted {
		return unmountResult{}, 0, nil
	}
	var result unmountResult
	bootstrap, err := a.runPowerShell(ctx, unmountDiskScript, a.mountPath, a.partitionNumber, false, &result)
	if err != nil {
		return result, bootstrap, newError(CodeUnmountFailed, "unmount", a.mountPath, err)
	}
	a.mounted = false
	return result, bootstrap, nil
}

func (a *Attachment) Detach() error {
	if !a.attached {
		return nil
	}
	status, _, _ := procDetachVirtualDisk.Call(uintptr(a.handle), 0, 0)
	if status != 0 {
		return win32Error(CodeDetachFailed, "DetachVirtualDisk", a.path, status)
	}
	a.attached = false
	return nil
}

func (a *Attachment) WaitDetached(ctx context.Context) (int64, int64, error) {
	var result detachResult
	bootstrap, err := a.runPowerShell(ctx, waitDetachScript, "", 0, false, &result)
	if err != nil {
		return result.DetachVisibilityWaitMs, bootstrap, newError(CodeDetachVisibilityTimeout, "wait-detach", a.physicalPath, err)
	}
	return result.DetachVisibilityWaitMs, bootstrap, nil
}

func (a *Attachment) CloseHandle() error {
	if a.handle == 0 {
		return nil
	}
	err := closeVirtualDiskHandle(a.handle)
	a.handle = 0
	return err
}

func (a *Attachment) Close(ctx context.Context) error {
	var errs []error
	if a.mounted {
		_, _, err := a.Unmount(ctx)
		errs = appendIf(errs, err)
	}
	if a.attached {
		errs = appendIf(errs, a.Detach())
	}
	errs = appendIf(errs, a.CloseHandle())
	return errors.Join(errs...)
}

func appendIf(values []error, err error) []error {
	if err != nil {
		return append(values, err)
	}
	return values
}

func (a *Attachment) runPowerShell(ctx context.Context, script, mountPath string, partitionNumber int, readOnly bool, result any) (int64, error) {
	started := time.Now()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "TESTPLAY_VHDX_DISK_NUMBER="+strconv.Itoa(a.diskNumber), "TESTPLAY_VHDX_PARTITION_NUMBER="+strconv.Itoa(partitionNumber), "TESTPLAY_VHDX_MOUNT_PATH="+mountPath, "TESTPLAY_VHDX_READ_ONLY="+strconv.FormatBool(readOnly))
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	ms := time.Since(started).Milliseconds()
	if err != nil {
		return ms, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if result != nil {
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(output))), result); err != nil {
			return ms, fmt.Errorf("decode PowerShell result: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return ms, nil
}

func DiskNumberFromPhysicalPath(path string) (int, error) {
	match := physicalDrivePattern.FindStringSubmatch(path)
	if len(match) != 2 {
		return 0, newError(CodeUnsafePhysicalDisk, "parse-physical-path", path, fmt.Errorf("not an exact PhysicalDrive path"))
	}
	number, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, newError(CodeUnsafePhysicalDisk, "parse-disk-number", path, err)
	}
	return number, nil
}

func closeVirtualDiskHandle(handle virtualDiskHandle) error {
	if handle == 0 {
		return nil
	}
	return syscall.CloseHandle(syscall.Handle(handle))
}
func win32Error(code, operation, path string, status uintptr) error {
	return &Error{Code: code, Operation: operation, Path: path, Win32Code: uint32(status), Cause: syscall.Errno(status)}
}

func logicalFileSize(path string) (*int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	value := info.Size()
	return &value, nil
}

func allocatedFileSize(path string) (*int64, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var high uint32
	low, _, callErr := procGetCompressedFileSizeW.Call(uintptr(unsafe.Pointer(pathPtr)), uintptr(unsafe.Pointer(&high)))
	runtime.KeepAlive(pathPtr)
	if low == 0xffffffff {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return nil, errno
		}
	}
	value := int64((uint64(high) << 32) | uint64(uint32(low)))
	return &value, nil
}

func FileIdentity(path string) (string, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

func FileUsageOf(path string) (FileUsage, error) {
	logical, err := logicalFileSize(path)
	if err != nil {
		return FileUsage{}, err
	}
	allocated, err := allocatedFileSize(path)
	if err != nil {
		return FileUsage{}, err
	}
	return FileUsage{LogicalBytes: *logical, AllocatedBytes: *allocated}, nil
}

type windowsBackend struct{}

func NewBackend() Backend                                           { return windowsBackend{} }
func (windowsBackend) Platform() string                             { return "windows" }
func (windowsBackend) Provider() string                             { return Provider }
func (windowsBackend) Supported() bool                              { return true }
func (windowsBackend) RequiresElevation() bool                      { return true }
func (windowsBackend) IsElevated(ctx context.Context) (bool, error) { return IsElevated(ctx) }

func (windowsBackend) Acquire(ctx context.Context, request AcquireRequest, progress ProgressFunc) (Lease, Metrics, error) {
	started := time.Now()
	metrics := Metrics{}
	if err := ctx.Err(); err != nil {
		return nil, metrics, newError(CodeCancelled, "acquire", request.ChildPath, err)
	}
	if err := EnsureAvailable(); err != nil {
		return nil, metrics, err
	}
	parentInfo, err := os.Lstat(request.ParentPath)
	if os.IsNotExist(err) {
		return nil, metrics, newError(CodeParentNotFound, "stat-parent", request.ParentPath, err)
	}
	if err != nil {
		return nil, metrics, newError(CodeParentInvalid, "stat-parent", request.ParentPath, err)
	}
	if !parentInfo.Mode().IsRegular() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return nil, metrics, newError(CodeParentInvalid, "validate-parent", request.ParentPath, fmt.Errorf("parent must be a regular non-link file"))
	}
	if _, err := os.Lstat(request.ChildPath); err == nil {
		return nil, metrics, newError(CodeChildExists, "stat-child", request.ChildPath, nil)
	} else if !os.IsNotExist(err) {
		return nil, metrics, newError(CodeChildCreateFailed, "stat-child", request.ChildPath, err)
	}
	mountCreated := false
	if _, err := os.Stat(request.MountPath); os.IsNotExist(err) {
		if err := os.Mkdir(request.MountPath, 0700); err != nil {
			return nil, metrics, newError(CodeMountFailed, "create-mount-path", request.MountPath, err)
		}
		mountCreated = true
	} else if err != nil {
		return nil, metrics, newError(CodeMountFailed, "stat-mount-path", request.MountPath, err)
	}
	childCreated := false
	var attachment *Attachment
	fail := func(primary error) (Lease, Metrics, error) {
		var cleanupErr error
		if attachment != nil {
			cleanupErr = attachment.Close(context.Background())
		}
		if cleanupErr == nil && childCreated {
			cleanupErr = os.Remove(request.ChildPath)
		}
		if mountCreated {
			_ = os.Remove(request.MountPath)
		}
		metrics.TotalWallClockMs = milliseconds(time.Since(started).Milliseconds())
		metrics.AcquireWallClockMs = metrics.TotalWallClockMs
		return nil, metrics, errors.Join(primary, cleanupErr)
	}
	if err := notify(progress, Progress{State: StateCreatingChild}); err != nil {
		return fail(err)
	}
	phase := time.Now()
	if err := CreateDifferencing(request.ChildPath, request.ParentPath); err != nil {
		return fail(err)
	}
	childCreated = true
	metrics.ChildCreateMs = milliseconds(time.Since(phase).Milliseconds())
	metrics.ChildBeforeAttachLogicalBytes, _ = logicalFileSize(request.ChildPath)
	if err := notify(progress, Progress{State: StateOpening}); err != nil {
		return fail(err)
	}
	phase = time.Now()
	attachment, err = Open(request.ChildPath, false)
	if err != nil {
		return fail(err)
	}
	if err := attachment.VerifyParent(request.ParentPath); err != nil {
		return fail(err)
	}
	metrics.ChildOpenMs = milliseconds(time.Since(phase).Milliseconds())
	if err := notify(progress, Progress{State: StateAttaching}); err != nil {
		return fail(err)
	}
	phase = time.Now()
	if err := attachment.Attach(false); err != nil {
		return fail(err)
	}
	metrics.AttachCallMs = milliseconds(time.Since(phase).Milliseconds())
	phase = time.Now()
	physicalPath, err := attachment.ResolvePhysicalPath()
	if err != nil {
		return fail(err)
	}
	metrics.PhysicalPathResolveMs = milliseconds(time.Since(phase).Milliseconds())
	if err := notify(progress, Progress{State: StateWaitingVolume, PhysicalPath: physicalPath}); err != nil {
		return fail(err)
	}
	volume, bootstrap, err := attachment.ResolveVolume(ctx, false)
	if err != nil {
		return fail(err)
	}
	metrics.PnPDiscoveryWaitMs = milliseconds(volume.PnPDiscoveryWaitMs)
	metrics.VolumeReadyWaitMs = milliseconds(volume.VolumeReadyWaitMs)
	metrics.PowerShellBootstrapMs = milliseconds(bootstrap)
	if err := notify(progress, Progress{State: StateMounting, PhysicalPath: physicalPath, VolumeGUIDPath: volume.VolumeGUIDPath}); err != nil {
		return fail(err)
	}
	mount, bootstrap, err := attachment.Mount(ctx, request.MountPath, false)
	if err != nil {
		return fail(err)
	}
	metrics.MountCallMs = milliseconds(mount.MountCallMs)
	metrics.MountVisibilityWaitMs = milliseconds(mount.MountVisibilityWaitMs)
	value := *metrics.PowerShellBootstrapMs + bootstrap
	metrics.PowerShellBootstrapMs = milliseconds(value)
	metrics.ChildReadyLogicalBytes, _ = logicalFileSize(request.ChildPath)
	metrics.ChildReadyAllocatedBytes, _ = allocatedFileSize(request.ChildPath)
	metrics.WorkspaceReadyMs = milliseconds(time.Since(started).Milliseconds())
	metrics.AcquireWallClockMs = metrics.WorkspaceReadyMs
	metrics.TotalWallClockMs = metrics.WorkspaceReadyMs
	if err := notify(progress, Progress{State: StateReady, PhysicalPath: physicalPath, VolumeGUIDPath: volume.VolumeGUIDPath}); err != nil {
		return fail(err)
	}
	return &windowsLease{attachment: attachment, info: LeaseInfo{ParentPath: request.ParentPath, ChildPath: request.ChildPath, PhysicalPath: physicalPath, VolumeGUIDPath: volume.VolumeGUIDPath, MountPath: request.MountPath}, mountCreated: mountCreated}, metrics, nil
}

// AttachExisting reopens a retained, broker-owned differencing child. It never
// creates or replaces either VHDX and verifies the child's recorded parent
// before exposing the mount.
func AttachExisting(ctx context.Context, request AcquireRequest, progress ProgressFunc) (Lease, Metrics, error) {
	started := time.Now()
	metrics := Metrics{}
	if err := ctx.Err(); err != nil {
		return nil, metrics, newError(CodeCancelled, "attach-existing", request.ChildPath, err)
	}
	for label, path := range map[string]string{"parent": request.ParentPath, "child": request.ChildPath} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, metrics, newError(CodeParentInvalid, "validate-"+label, path, err)
		}
	}
	mountCreated := false
	if _, err := os.Stat(request.MountPath); os.IsNotExist(err) {
		if err := os.Mkdir(request.MountPath, 0700); err != nil {
			return nil, metrics, newError(CodeMountFailed, "create-mount-path", request.MountPath, err)
		}
		mountCreated = true
	} else if err != nil {
		return nil, metrics, err
	}
	attachment, err := Open(request.ChildPath, false)
	if err != nil {
		return nil, metrics, err
	}
	fail := func(primary error) (Lease, Metrics, error) {
		cleanupErr := attachment.Close(context.Background())
		if mountCreated {
			_ = os.Remove(request.MountPath)
		}
		return nil, metrics, errors.Join(primary, cleanupErr)
	}
	if err := attachment.VerifyParent(request.ParentPath); err != nil {
		return fail(err)
	}
	phase := time.Now()
	if err := attachment.Attach(false); err != nil {
		return fail(err)
	}
	metrics.AttachCallMs = milliseconds(time.Since(phase).Milliseconds())
	physicalPath, err := attachment.ResolvePhysicalPath()
	if err != nil {
		return fail(err)
	}
	volume, bootstrap, err := attachment.ResolveVolume(ctx, false)
	if err != nil {
		return fail(err)
	}
	metrics.PnPDiscoveryWaitMs = milliseconds(volume.PnPDiscoveryWaitMs)
	metrics.VolumeReadyWaitMs = milliseconds(volume.VolumeReadyWaitMs)
	metrics.PowerShellBootstrapMs = milliseconds(bootstrap)
	mount, bootstrap, err := attachment.Mount(ctx, request.MountPath, false)
	if err != nil {
		return fail(err)
	}
	metrics.MountCallMs = milliseconds(mount.MountCallMs)
	metrics.MountVisibilityWaitMs = milliseconds(mount.MountVisibilityWaitMs)
	value := *metrics.PowerShellBootstrapMs + bootstrap
	metrics.PowerShellBootstrapMs = milliseconds(value)
	metrics.ChildReadyLogicalBytes, _ = logicalFileSize(request.ChildPath)
	metrics.ChildReadyAllocatedBytes, _ = allocatedFileSize(request.ChildPath)
	metrics.WorkspaceReadyMs = milliseconds(time.Since(started).Milliseconds())
	metrics.AcquireWallClockMs = metrics.WorkspaceReadyMs
	metrics.TotalWallClockMs = metrics.WorkspaceReadyMs
	if err := notify(progress, Progress{State: StateReady, PhysicalPath: physicalPath, VolumeGUIDPath: volume.VolumeGUIDPath}); err != nil {
		return fail(err)
	}
	return &windowsLease{attachment: attachment, info: LeaseInfo{ParentPath: request.ParentPath, ChildPath: request.ChildPath, PhysicalPath: physicalPath, VolumeGUIDPath: volume.VolumeGUIDPath, MountPath: request.MountPath}, mountCreated: mountCreated}, metrics, nil
}

type windowsLease struct {
	mu           sync.Mutex
	attachment   *Attachment
	info         LeaseInfo
	mountCreated bool
	released     bool
	metrics      Metrics
}

func (l *windowsLease) Info() LeaseInfo { return l.info }
func (l *windowsLease) Release(ctx context.Context, deleteChild bool, progress ProgressFunc) (Metrics, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return l.metrics, nil
	}
	started := time.Now()
	metrics := Metrics{}
	if err := notify(progress, Progress{State: StateUnmounting, PhysicalPath: l.info.PhysicalPath, VolumeGUIDPath: l.info.VolumeGUIDPath}); err != nil {
		return metrics, err
	}
	unmount, bootstrap, err := l.attachment.Unmount(ctx)
	if err != nil {
		return metrics, err
	}
	metrics.UnmountCallMs = milliseconds(unmount.UnmountCallMs)
	metrics.PowerShellBootstrapMs = milliseconds(bootstrap)
	if err := notify(progress, Progress{State: StateDetaching, PhysicalPath: l.info.PhysicalPath, VolumeGUIDPath: l.info.VolumeGUIDPath}); err != nil {
		return metrics, err
	}
	phase := time.Now()
	if err := l.attachment.Detach(); err != nil {
		return metrics, err
	}
	metrics.DetachCallMs = milliseconds(time.Since(phase).Milliseconds())
	if err := l.attachment.CloseHandle(); err != nil {
		return metrics, newError(CodeCleanupFailed, "close-handle", l.info.ChildPath, err)
	}
	wait, bootstrap, err := l.attachment.WaitDetached(ctx)
	if err != nil {
		return metrics, err
	}
	metrics.DetachVisibilityWaitMs = milliseconds(wait)
	value := *metrics.PowerShellBootstrapMs + bootstrap
	metrics.PowerShellBootstrapMs = milliseconds(value)
	metrics.ChildReleasedLogicalBytes, _ = logicalFileSize(l.info.ChildPath)
	metrics.ChildReleasedAllocatedBytes, _ = allocatedFileSize(l.info.ChildPath)
	cleanup := time.Now()
	if deleteChild {
		if err := os.Remove(l.info.ChildPath); err != nil && !os.IsNotExist(err) {
			return metrics, newError(CodeCleanupFailed, "remove-child", l.info.ChildPath, err)
		}
	}
	if l.mountCreated {
		if err := os.Remove(l.info.MountPath); err != nil && !os.IsNotExist(err) {
			return metrics, newError(CodeCleanupFailed, "remove-mount-path", l.info.MountPath, err)
		}
	}
	metrics.CleanupMs = milliseconds(time.Since(cleanup).Milliseconds())
	metrics.ReleaseWallClockMs = milliseconds(time.Since(started).Milliseconds())
	metrics.TotalWallClockMs = metrics.ReleaseWallClockMs
	if err := notify(progress, Progress{State: StateReleased, PhysicalPath: l.info.PhysicalPath, VolumeGUIDPath: l.info.VolumeGUIDPath}); err != nil {
		return metrics, err
	}
	l.released = true
	l.metrics = metrics
	return metrics, nil
}
