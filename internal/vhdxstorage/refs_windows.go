//go:build windows

package vhdxstorage

import (
	"context"
	"fmt"
	"strings"
)

const initializeReFSDiskScript = `
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
  throw "directory mount host must be NTFS; found $($hostVolume.FileSystemType)"
}
$deadline = [DateTime]::UtcNow.AddSeconds(15)
do {
  $disks = @(Get-Disk -Number $diskNumber -ErrorAction SilentlyContinue)
  if ($disks.Count -eq 1) { break }
  Start-Sleep -Milliseconds 200
} while ([DateTime]::UtcNow -lt $deadline)
if ($disks.Count -ne 1) { throw "expected exactly one disk $diskNumber; found $($disks.Count)" }
$disk = $disks[0]
if ($disk.BusType.ToString() -ne 'File Backed Virtual') {
  throw "unsafe bus type for disk ${diskNumber}: $($disk.BusType)"
}
if ($disk.PartitionStyle.ToString() -ne 'RAW') {
  throw "new Managed ReFS disk is not RAW: $($disk.PartitionStyle)"
}
if (@(Get-Partition -DiskNumber $diskNumber -ErrorAction SilentlyContinue).Count -ne 0) {
  throw 'new Managed ReFS disk already has partitions'
}
if ($disk.IsReadOnly) { Set-Disk -Number $diskNumber -IsReadOnly $false }
if ($disk.IsOffline) { Set-Disk -Number $diskNumber -IsOffline $false }
Initialize-Disk -Number $diskNumber -PartitionStyle GPT -PassThru | Out-Null
$partition = New-Partition -DiskNumber $diskNumber -UseMaximumSize -AssignDriveLetter:$false
try {
  Format-Volume -Partition $partition -FileSystem ReFS -NewFileSystemLabel 'TestPlayManagedReFS' -SetIntegrityStreams:$false -Confirm:$false -Force -ErrorAction Stop | Out-Null
} catch {
  throw "ReFS format unavailable: $($_.Exception.Message)"
}
$volume = Get-Volume -Partition $partition -ErrorAction Stop
if ($volume.FileSystemType.ToString() -ne 'ReFS') {
  throw "ReFS format verification failed: $($volume.FileSystemType)"
}
Add-PartitionAccessPath -InputObject $partition -AccessPath $mountPath -ErrorAction Stop
[pscustomobject]@{
  partitionNumber = $partition.PartitionNumber
  volumeGuidPath = $volume.Path
  filesystem = $volume.FileSystemType.ToString()
  clusterSize = [int64]$volume.AllocationUnitSize
  totalBytes = [int64]$volume.Size
  freeBytes = [int64]$volume.SizeRemaining
} | ConvertTo-Json -Compress
`

const inspectReFSVolumeScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$partitionNumber = [int]$env:TESTPLAY_VHDX_PARTITION_NUMBER
$partition = Get-Partition -DiskNumber $diskNumber -PartitionNumber $partitionNumber -ErrorAction Stop
$volume = Get-Volume -Partition $partition -ErrorAction Stop
[pscustomobject]@{
  partitionNumber = $partition.PartitionNumber
  volumeGuidPath = $volume.Path
  filesystem = $volume.FileSystemType.ToString()
  clusterSize = [int64]$volume.AllocationUnitSize
  totalBytes = [int64]$volume.Size
  freeBytes = [int64]$volume.SizeRemaining
} | ConvertTo-Json -Compress
`

// ReFSVolumeInfo is the measured volume identity returned by the Storage module.
type ReFSVolumeInfo struct {
	PartitionNumber int    `json:"partitionNumber"`
	VolumeGUIDPath  string `json:"volumeGuidPath"`
	Filesystem      string `json:"filesystem"`
	ClusterSize     int64  `json:"clusterSize"`
	TotalBytes      int64  `json:"totalBytes"`
	FreeBytes       int64  `json:"freeBytes"`
}

func (a *Attachment) InitializeReFSAndMount(ctx context.Context, mountPath string) (ReFSVolumeInfo, error) {
	var result ReFSVolumeInfo
	if _, err := a.runPowerShell(ctx, initializeReFSDiskScript, mountPath, 0, false, &result); err != nil {
		code := CodeMountFailed
		if strings.Contains(strings.ToLower(err.Error()), "refs format unavailable") {
			return result, fmt.Errorf("refs format unavailable: %w", err)
		}
		return result, newError(code, "initialize-refs-and-mount", mountPath, err)
	}
	a.partitionNumber = result.PartitionNumber
	a.volumeGUIDPath = result.VolumeGUIDPath
	a.mountPath = mountPath
	a.mounted = true
	return result, nil
}

func (a *Attachment) InspectReFSVolume(ctx context.Context) (ReFSVolumeInfo, error) {
	var result ReFSVolumeInfo
	if _, err := a.runPowerShell(ctx, inspectReFSVolumeScript, a.mountPath, a.partitionNumber, false, &result); err != nil {
		return result, newError(CodeVolumeResolutionFailed, "inspect-refs-volume", a.mountPath, err)
	}
	return result, nil
}
