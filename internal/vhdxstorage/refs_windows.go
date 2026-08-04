//go:build windows

package vhdxstorage

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const devDriveCapabilityScript = `
$ErrorActionPreference = 'Stop'
$minimumBuild = 22621
$build = [Environment]::OSVersion.Version.Build
if ($build -lt $minimumBuild) {
  throw "dev-drive-unavailable: Windows build ${build} is below ${minimumBuild}"
}
$formatVolume = Get-Command Format-Volume -ErrorAction SilentlyContinue
if ($null -eq $formatVolume) {
  throw 'dev-drive-unavailable: Format-Volume is unavailable'
}
if (-not $formatVolume.Parameters.ContainsKey('DevDrive')) {
  throw 'dev-drive-unavailable: Format-Volume has no DevDrive parameter'
}
$fsutil = Get-Command fsutil.exe -ErrorAction SilentlyContinue
if ($null -eq $fsutil) {
  throw 'dev-drive-unavailable: fsutil.exe is unavailable'
}
$devDriveOutput = @(& $fsutil.Source devdrv query 2>&1)
$devDriveExitCode = $LASTEXITCODE
if ($devDriveExitCode -ne 0) {
  throw "dev-drive-disabled: fsutil devdrv query exitCode=${devDriveExitCode}: $($devDriveOutput -join [Environment]::NewLine)"
}
`

const initializeDevDriveDiskScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$mountPath = $env:TESTPLAY_VHDX_MOUNT_PATH.TrimEnd('\') + '\'

function Get-AvailableTemporaryDriveLetter {
  $used = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
  @(Get-Volume -ErrorAction SilentlyContinue) | ForEach-Object {
    if ($null -ne $_.DriveLetter) { [void]$used.Add([string]$_.DriveLetter) }
  }
  @(Get-PSDrive -PSProvider FileSystem -ErrorAction SilentlyContinue) | ForEach-Object {
    if ($_.Name -match '^[A-Z]$') { [void]$used.Add($_.Name) }
  }
  @(Get-Partition -ErrorAction SilentlyContinue) | ForEach-Object {
    @($_.AccessPaths) | ForEach-Object {
      if ($_ -match '^([A-Z]):\\$') { [void]$used.Add($Matches[1]) }
    }
  }
  for ($code = [int][char]'Z'; $code -ge [int][char]'D'; $code--) {
    $candidate = [string][char]$code
    if (-not $used.Contains($candidate) -and -not (Test-Path -LiteralPath "${candidate}:\")) {
      return $candidate
    }
  }
  return $null
}

$mountItem = Get-Item -LiteralPath $mountPath -Force -ErrorAction Stop
if (($mountItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
  throw "mount path is a reparse point: $mountPath"
}
if (@(Get-ChildItem -LiteralPath $mountPath -Force).Count -ne 0) {
  throw "mount path is not empty: $mountPath"
}
$hostVolume = Get-Volume -FilePath $mountItem.FullName -ErrorAction Stop
if ($hostVolume.FileSystemType.ToString() -ne 'NTFS') {
  throw "dev-drive-unavailable: directory mount host must be NTFS; found $($hostVolume.FileSystemType)"
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
  throw "new Managed Dev Drive disk is not RAW: $($disk.PartitionStyle)"
}
if (@(Get-Partition -DiskNumber $diskNumber -ErrorAction SilentlyContinue).Count -ne 0) {
  throw 'new Managed Dev Drive disk already has partitions'
}
if ($disk.IsReadOnly) { Set-Disk -Number $diskNumber -IsReadOnly $false }
if ($disk.IsOffline) { Set-Disk -Number $diskNumber -IsOffline $false }

Initialize-Disk -Number $diskNumber -PartitionStyle GPT -PassThru | Out-Null
$partition = New-Partition -DiskNumber $diskNumber -UseMaximumSize -AssignDriveLetter:$false
$temporaryLetter = Get-AvailableTemporaryDriveLetter
if ([string]::IsNullOrWhiteSpace($temporaryLetter)) {
  throw 'temporary-drive-letter-unavailable: no unused drive letter from D through Z'
}
$temporaryAccessPath = "${temporaryLetter}:\"
$temporaryAssigned = $false
$temporaryRemoved = $false
$privateMountAssigned = $false
$formatAttempted = $false
$formatSucceeded = $false
$queryExitCode = -1
$queryOutput = ''

try {
  try {
    Add-PartitionAccessPath -InputObject $partition -AccessPath $temporaryAccessPath -ErrorAction Stop
  } catch {
    throw "temporary-drive-letter-unavailable: failed to assign ${temporaryAccessPath}: $($_.Exception.Message)"
  }
  $temporaryAssigned = $true

  $formatAttempted = $true
  try {
    Format-Volume -DriveLetter ([char]$temporaryLetter) -DevDrive -NewFileSystemLabel 'TestPlayManagedDevDrive' -Confirm:$false -Force -ErrorAction Stop | Out-Null
    $formatSucceeded = $true
  } catch {
    throw "dev-drive-format-failed: $($_.Exception.Message)"
  }

  $volume = Get-Volume -DriveLetter ([char]$temporaryLetter) -ErrorAction Stop
  if ($volume.FileSystemType.ToString() -ne 'ReFS') {
    throw "dev-drive-verification-failed: filesystem=$($volume.FileSystemType)"
  }

  $rawQueryOutput = @(& fsutil.exe devdrv query "${temporaryLetter}:" 2>&1)
  $queryExitCode = $LASTEXITCODE
  $queryOutput = $rawQueryOutput -join [Environment]::NewLine
  if ($queryExitCode -ne 0) {
    throw "dev-drive-verification-failed: fsutil devdrv query exitCode=${queryExitCode}: $queryOutput"
  }

  try {
    Add-PartitionAccessPath -InputObject $partition -AccessPath $mountPath -ErrorAction Stop
  } catch {
    throw "dev-drive-verification-failed: private directory mount failed: $($_.Exception.Message)"
  }
  $privateMountAssigned = $true
  try {
    Remove-PartitionAccessPath -InputObject $partition -AccessPath $temporaryAccessPath -ErrorAction Stop
  } catch {
    throw "temporary-drive-letter-cleanup-failed: failed to remove ${temporaryAccessPath}: $($_.Exception.Message)"
  }
  $temporaryRemoved = $true

  $current = Get-Partition -DiskNumber $diskNumber -PartitionNumber $partition.PartitionNumber -ErrorAction Stop
  if (-not (@($current.AccessPaths) -contains $mountPath)) {
    throw 'dev-drive-verification-failed: private directory mount is not visible'
  }
  if (@($current.AccessPaths) -contains $temporaryAccessPath) {
    throw 'temporary-drive-letter-cleanup-failed: temporary drive letter remains visible'
  }
  $mountedVolume = Get-Volume -FilePath $mountPath -ErrorAction Stop
  if ($mountedVolume.FileSystemType.ToString() -ne 'ReFS') {
    throw "dev-drive-verification-failed: private mount filesystem=$($mountedVolume.FileSystemType)"
  }

  [pscustomobject]@{
    partitionNumber = $partition.PartitionNumber
    volumeGuidPath = $volume.Path
    filesystem = $volume.FileSystemType.ToString()
    clusterSize = [int64]$volume.AllocationUnitSize
    totalBytes = [int64]$volume.Size
    freeBytes = [int64]$volume.SizeRemaining
    devDrive = [ordered]@{
      formatAttempted = $formatAttempted
      formatSucceeded = $formatSucceeded
      queryExitCode = $queryExitCode
      queryOutput = $queryOutput
      temporaryDriveLetterAssigned = $temporaryAssigned
      temporaryDriveLetterRemoved = $temporaryRemoved
      privateMountVerified = $true
    }
  } | ConvertTo-Json -Depth 4 -Compress
} catch {
  $primary = $_.Exception.Message
  $cleanupErrors = [Collections.Generic.List[string]]::new()
  if ($privateMountAssigned) {
    try {
      $current = Get-Partition -DiskNumber $diskNumber -PartitionNumber $partition.PartitionNumber -ErrorAction Stop
      if (@($current.AccessPaths) -contains $mountPath) {
        Remove-PartitionAccessPath -InputObject $current -AccessPath $mountPath -ErrorAction Stop
      }
    } catch {
      $cleanupErrors.Add("private mount cleanup: $($_.Exception.Message)")
    }
  }
  if ($temporaryAssigned -and -not $temporaryRemoved) {
    try {
      $current = Get-Partition -DiskNumber $diskNumber -PartitionNumber $partition.PartitionNumber -ErrorAction Stop
      if (@($current.AccessPaths) -contains $temporaryAccessPath) {
        Remove-PartitionAccessPath -InputObject $current -AccessPath $temporaryAccessPath -ErrorAction Stop
      }
      $temporaryRemoved = $true
    } catch {
      $cleanupErrors.Add("temporary drive letter cleanup: $($_.Exception.Message)")
    }
  }
  if ($cleanupErrors.Count -ne 0) {
    throw "temporary-drive-letter-cleanup-failed: primary=${primary}; cleanup=$($cleanupErrors -join '; ')"
  }
  throw $primary
}
`

const inspectDevDriveVolumeScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$partitionNumber = [int]$env:TESTPLAY_VHDX_PARTITION_NUMBER
$mountPath = $env:TESTPLAY_VHDX_MOUNT_PATH.TrimEnd('\') + '\'
$partition = Get-Partition -DiskNumber $diskNumber -PartitionNumber $partitionNumber -ErrorAction Stop
$volume = Get-Volume -Partition $partition -ErrorAction Stop
if ($volume.FileSystemType.ToString() -ne 'ReFS') {
  throw "dev-drive-verification-failed: filesystem=$($volume.FileSystemType)"
}
if (-not (@($partition.AccessPaths) -contains $mountPath)) {
  throw 'dev-drive-verification-failed: private directory mount is not visible'
}
$rawQueryOutput = @(& fsutil.exe devdrv query $mountPath 2>&1)
$queryExitCode = $LASTEXITCODE
$queryOutput = $rawQueryOutput -join [Environment]::NewLine
if ($queryExitCode -ne 0) {
  throw "dev-drive-verification-failed: fsutil devdrv query exitCode=${queryExitCode}: $queryOutput"
}
[pscustomobject]@{
  partitionNumber = $partition.PartitionNumber
  volumeGuidPath = $volume.Path
  filesystem = $volume.FileSystemType.ToString()
  clusterSize = [int64]$volume.AllocationUnitSize
  totalBytes = [int64]$volume.Size
  freeBytes = [int64]$volume.SizeRemaining
  devDrive = [ordered]@{
    formatAttempted = $false
    formatSucceeded = $false
    queryExitCode = $queryExitCode
    queryOutput = $queryOutput
    temporaryDriveLetterAssigned = $false
    temporaryDriveLetterRemoved = $false
    privateMountVerified = $true
  }
} | ConvertTo-Json -Depth 4 -Compress
`

// DevDriveEvidence contains structural and raw fsutil evidence for a mounted
// Windows developer volume.
type DevDriveEvidence struct {
	FormatAttempted              bool   `json:"formatAttempted"`
	FormatSucceeded              bool   `json:"formatSucceeded"`
	QueryExitCode                int    `json:"queryExitCode"`
	QueryOutput                  string `json:"queryOutput"`
	TemporaryDriveLetterAssigned bool   `json:"temporaryDriveLetterAssigned"`
	TemporaryDriveLetterRemoved  bool   `json:"temporaryDriveLetterRemoved"`
	PrivateMountVerified         bool   `json:"privateMountVerified"`
}

// ReFSVolumeInfo is the measured Dev Drive volume identity returned by the
// Windows Storage module.
type ReFSVolumeInfo struct {
	PartitionNumber int              `json:"partitionNumber"`
	VolumeGUIDPath  string           `json:"volumeGuidPath"`
	Filesystem      string           `json:"filesystem"`
	ClusterSize     int64            `json:"clusterSize"`
	TotalBytes      int64            `json:"totalBytes"`
	FreeBytes       int64            `json:"freeBytes"`
	DevDrive        DevDriveEvidence `json:"devDrive"`
}

func EnsureDevDriveAvailable(ctx context.Context) error {
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", devDriveCapabilityScript)
	output, err := command.CombinedOutput()
	if err != nil {
		return classifyDevDriveError("dev-drive-preflight", "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output))), CodeDevDriveUnavailable)
	}
	return nil
}

func (a *Attachment) InitializeDevDriveAndMount(ctx context.Context, mountPath string) (ReFSVolumeInfo, error) {
	var result ReFSVolumeInfo
	if _, err := a.runPowerShell(ctx, initializeDevDriveDiskScript, mountPath, 0, false, &result); err != nil {
		return result, classifyDevDriveError("initialize-dev-drive-and-mount", mountPath, err, CodeDevDriveFormatFailed)
	}
	a.partitionNumber = result.PartitionNumber
	a.volumeGUIDPath = result.VolumeGUIDPath
	a.mountPath = mountPath
	a.mounted = true
	return result, nil
}

func (a *Attachment) InspectDevDriveVolume(ctx context.Context) (ReFSVolumeInfo, error) {
	var result ReFSVolumeInfo
	if _, err := a.runPowerShell(ctx, inspectDevDriveVolumeScript, a.mountPath, a.partitionNumber, false, &result); err != nil {
		return result, classifyDevDriveError("inspect-dev-drive-volume", a.mountPath, err, CodeDevDriveVerificationFailed)
	}
	return result, nil
}

func classifyDevDriveError(operation, path string, err error, fallback string) error {
	lower := strings.ToLower(err.Error())
	code := fallback
	for _, candidate := range []string{
		CodeTemporaryDriveLetterCleanupFailed,
		CodeTemporaryDriveLetterUnavailable,
		CodeDevDriveVerificationFailed,
		CodeDevDriveFormatFailed,
		CodeDevDriveDisabled,
		CodeDevDriveUnavailable,
	} {
		if strings.Contains(lower, candidate) {
			code = candidate
			break
		}
	}
	return newError(code, operation, path, err)
}
