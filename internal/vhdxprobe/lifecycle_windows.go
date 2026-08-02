//go:build windows

package vhdxprobe

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	adminCheckScript = `
$principal = [Security.Principal.WindowsPrincipal](
  [Security.Principal.WindowsIdentity]::GetCurrent()
)
$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
`

	initializeDiskScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$mountPath = $env:TESTPLAY_VHDX_MOUNT_PATH.TrimEnd('\') + '\'
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
  throw "new parent disk is not RAW: $($disk.PartitionStyle)"
}
$existing = @(Get-Partition -DiskNumber $diskNumber -ErrorAction SilentlyContinue)
if ($existing.Count -ne 0) { throw "new parent disk already has partitions" }
if ($disk.IsReadOnly) { Set-Disk -Number $diskNumber -IsReadOnly $false }
if ($disk.IsOffline) { Set-Disk -Number $diskNumber -IsOffline $false }
Initialize-Disk -Number $diskNumber -PartitionStyle GPT -PassThru | Out-Null
$partition = New-Partition -DiskNumber $diskNumber -UseMaximumSize
Format-Volume -Partition $partition -FileSystem NTFS -NewFileSystemLabel 'TestPlayVHDXProbe' -Confirm:$false -Force | Out-Null
Add-PartitionAccessPath -InputObject $partition -AccessPath $mountPath
[pscustomobject]@{
  diskNumber = $diskNumber
  busType = 'File Backed Virtual'
  partitionNumber = $partition.PartitionNumber
  mountPath = $mountPath
} | ConvertTo-Json -Compress
`

	mountDiskScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$mountPath = $env:TESTPLAY_VHDX_MOUNT_PATH.TrimEnd('\') + '\'
$readOnly = $env:TESTPLAY_VHDX_READ_ONLY -eq '1'
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
if ($disk.PartitionStyle.ToString() -ne 'GPT') {
  throw "attached disk is not GPT: $($disk.PartitionStyle)"
}
if ($disk.IsOffline) { Set-Disk -Number $diskNumber -IsOffline $false }
if (-not $readOnly -and $disk.IsReadOnly) {
  Set-Disk -Number $diskNumber -IsReadOnly $false
}
$dataGuid = [guid]'EBD0A0A2-B9E5-4433-87C0-68B6B72699C7'
$partitions = @(Get-Partition -DiskNumber $diskNumber)
$dataPartitions = @($partitions | Where-Object {
  [guid]$_.GptType -eq $dataGuid
})
if ($dataPartitions.Count -ne 1) {
  throw "expected one basic data partition; found $($dataPartitions.Count)"
}
$partition = $dataPartitions[0]
Add-PartitionAccessPath -InputObject $partition -AccessPath $mountPath
[pscustomobject]@{
  diskNumber = $diskNumber
  busType = 'File Backed Virtual'
  partitionNumber = $partition.PartitionNumber
  mountPath = $mountPath
  readOnly = $readOnly
} | ConvertTo-Json -Compress
`

	unmountDiskScript = `
$ErrorActionPreference = 'Stop'
$diskNumber = [int]$env:TESTPLAY_VHDX_DISK_NUMBER
$mountPath = $env:TESTPLAY_VHDX_MOUNT_PATH.TrimEnd('\') + '\'
$disks = @(Get-Disk -Number $diskNumber -ErrorAction Stop)
if ($disks.Count -ne 1) { throw "expected exactly one disk $diskNumber" }
if ($disks[0].BusType.ToString() -ne 'File Backed Virtual') {
  throw "unsafe bus type for disk ${diskNumber}: $($disks[0].BusType)"
}
$matches = @(Get-Partition -DiskNumber $diskNumber | Where-Object {
  @($_.AccessPaths) -contains $mountPath
})
if ($matches.Count -ne 1) {
  throw "expected exactly one partition mounted at $mountPath; found $($matches.Count)"
}
Remove-PartitionAccessPath -InputObject $matches[0] -AccessPath $mountPath
`
)

type attachedVHDX struct {
	handle       virtualDiskHandle
	path         string
	physicalPath string
	diskNumber   int
	mountPath    string
	mounted      bool
}

// Run executes one isolated differencing-VHDX probe.
func Run(ctx context.Context, config Config) (result *Result, returnErr error) {
	if err := cancellationError(ctx, "start", config.Root); err != nil {
		return nil, err
	}
	if err := ensureVirtDiskAPI(); err != nil {
		return nil, err
	}
	elevated, err := isElevated(ctx)
	if err != nil {
		return nil, probeError(CodeNotElevated, "check-administrator", config.Root, err)
	}
	if !elevated {
		return nil, probeError(
			CodeNotElevated,
			"check-administrator",
			config.Root,
			fmt.Errorf("run the integration probe from an elevated PowerShell"),
		)
	}
	plan, err := NewPlan(config, "")
	if err != nil {
		return nil, err
	}
	result = &Result{
		OperationID: filepath.Base(plan.Paths.Operation)[len("testplay-vhdx-probe-"):],
		Paths:       plan.Paths,
		Metrics: StorageMetrics{
			ParentVirtualBytes:  plan.Config.ParentVirtualBytes,
			LogicalPayloadBytes: plan.Config.PayloadBytes,
		},
	}
	safeToDelete := true
	closeAfterFailure := func(disk *attachedVHDX, primary error) error {
		if closeErr := disk.close(ctx); closeErr != nil {
			safeToDelete = false
			return errors.Join(primary, closeErr)
		}
		return primary
	}
	rootCreated := false
	if _, statErr := os.Stat(plan.Paths.Root); os.IsNotExist(statErr) {
		if err := os.Mkdir(plan.Paths.Root, 0700); err != nil {
			return nil, probeError(CodeInvalidProbeRoot, "create-root", plan.Paths.Root, err)
		}
		rootCreated = true
	}
	if err := os.Mkdir(plan.Paths.Operation, 0700); err != nil {
		return nil, probeError(CodeParentExists, "create-operation-directory", plan.Paths.Operation, err)
	}
	if err := os.Mkdir(plan.Paths.Mounts, 0700); err != nil {
		return nil, probeError(CodeInvalidProbeRoot, "create-mount-directory", plan.Paths.Mounts, err)
	}
	defer func() {
		cleanupStarted := time.Now()
		if safeToDelete {
			if err := validateCleanupTarget(plan); err == nil {
				err = os.RemoveAll(plan.Paths.Operation)
				if err == nil {
					result.CleanupPassed = true
				}
				if returnErr == nil && err != nil {
					returnErr = probeError(CodeCleanupFailed, "remove-operation-directory", plan.Paths.Operation, err)
				}
			} else if returnErr == nil {
				returnErr = err
			}
		}
		if rootCreated && result.CleanupPassed {
			_ = os.Remove(plan.Paths.Root)
		}
		result.Durations.CleanupMs = time.Since(cleanupStarted).Milliseconds()
	}()

	if err := cancellationError(ctx, "parent-create", plan.Paths.Parent); err != nil {
		return result, err
	}
	started := time.Now()
	if err := createDynamicVHDX(plan.Paths.Parent, plan.Config.ParentVirtualBytes); err != nil {
		return result, err
	}
	result.Durations.ParentCreateMs = time.Since(started).Milliseconds()

	parentMount := filepath.Join(plan.Paths.Mounts, "parent")
	if err := os.Mkdir(parentMount, 0700); err != nil {
		return result, probeError(CodeMountResolutionFailed, "create-parent-mount", parentMount, err)
	}
	if err := cancellationError(ctx, "parent-attach", plan.Paths.Parent); err != nil {
		return result, err
	}
	started = time.Now()
	parent, err := attachVHDX(plan.Paths.Parent, false)
	if err != nil {
		return result, probeError(CodeParentAttachFailed, "attach-parent", plan.Paths.Parent, err)
	}
	result.ParentPhysicalPath = parent.physicalPath
	result.Durations.ParentAttachMs = time.Since(started).Milliseconds()
	started = time.Now()
	if err := parent.initializeAndMount(ctx, parentMount); err != nil {
		if closeErr := parent.close(ctx); closeErr != nil {
			safeToDelete = false
			return result, errors.Join(err, closeErr)
		}
		return result, probeError(CodeParentInitializeFailed, "initialize-parent", plan.Paths.Parent, err)
	}
	result.Durations.ParentInitializeMs = time.Since(started).Milliseconds()
	if err := cancellationError(ctx, "parent-seed", plan.Paths.Parent); err != nil {
		if closeErr := parent.close(ctx); closeErr != nil {
			safeToDelete = false
			return result, errors.Join(err, closeErr)
		}
		return result, err
	}
	started = time.Now()
	baselineHash, err := seedParent(parentMount, plan.Config.PayloadBytes)
	if err != nil {
		primary := probeError(CodeParentSeedFailed, "seed-parent", parentMount, err)
		return result, closeAfterFailure(parent, primary)
	}
	result.BaselinePayloadHash = baselineHash
	result.Durations.ParentSeedMs = time.Since(started).Milliseconds()
	sizeInfo, err := getVirtualDiskSize(parent.handle, plan.Paths.Parent)
	if err != nil {
		return result, closeAfterFailure(parent, err)
	}
	result.Metrics.ParentVirtualBytes = int64(sizeInfo.VirtualSize)
	started = time.Now()
	if err := parent.close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}
	result.Durations.ParentDetachMs = time.Since(started).Milliseconds()
	result.ParentHashBefore, err = hashFile(plan.Paths.Parent)
	if err != nil {
		return result, probeError(CodeVerificationFailed, "hash-parent-before", plan.Paths.Parent, err)
	}
	result.Metrics.ParentFileBytes, err = regularFileSize(plan.Paths.Parent)
	if err != nil {
		return result, err
	}

	if err := cancellationError(ctx, "child-create", plan.Paths.ChildA); err != nil {
		return result, err
	}
	started = time.Now()
	for _, childPath := range []string{plan.Paths.ChildA, plan.Paths.ChildB} {
		if err := createDifferencingVHDX(childPath, plan.Paths.Parent); err != nil {
			return result, err
		}
		childHandle, openErr := openVirtualDisk(childPath, false)
		if openErr != nil {
			return result, openErr
		}
		verifyErr := verifyDifferencingParent(childHandle, childPath, plan.Paths.Parent)
		closeErr := closeVirtualDiskHandle(childHandle)
		if verifyErr != nil || closeErr != nil {
			return result, errors.Join(verifyErr, closeErr)
		}
	}
	result.Durations.ChildCreateMs = time.Since(started).Milliseconds()
	result.Metrics.ChildInitialFileBytes, err = regularFileSize(plan.Paths.ChildA)
	if err != nil {
		return result, err
	}

	childAMount := filepath.Join(plan.Paths.Mounts, "child-a")
	if err := os.Mkdir(childAMount, 0700); err != nil {
		return result, err
	}
	if err := cancellationError(ctx, "child-a-attach", plan.Paths.ChildA); err != nil {
		return result, err
	}
	started = time.Now()
	childA, err := attachVHDX(plan.Paths.ChildA, false)
	if err != nil {
		return result, err
	}
	result.ChildAPhysicalPath = childA.physicalPath
	mountStarted := time.Now()
	if err := childA.mountExisting(ctx, childAMount, false); err != nil {
		if closeErr := childA.close(ctx); closeErr != nil {
			safeToDelete = false
			return result, errors.Join(err, closeErr)
		}
		return result, err
	}
	result.Durations.MountResolveMs = time.Since(mountStarted).Milliseconds()
	result.Durations.ChildAttachMs = time.Since(started).Milliseconds()
	result.Metrics.ChildAfterAttachFileBytes, _ = regularFileSize(plan.Paths.ChildA)
	if err := verifyPayloadHash(childAMount, baselineHash); err != nil {
		return result, closeAfterFailure(childA, err)
	}
	if err := cancellationError(ctx, "child-a-mutation", plan.Paths.ChildA); err != nil {
		return result, closeAfterFailure(childA, err)
	}
	started = time.Now()
	childAHash, err := mutateChildA(childAMount, plan.Config.PayloadBytes)
	if err != nil {
		primary := probeError(CodeVerificationFailed, "mutate-child-a", childAMount, err)
		return result, closeAfterFailure(childA, primary)
	}
	result.Durations.MutationMs = time.Since(started).Milliseconds()
	started = time.Now()
	if err := childA.close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}
	result.Durations.ChildDetachMs = time.Since(started).Milliseconds()
	result.Metrics.ChildAfterMutationFileBytes, _ = regularFileSize(plan.Paths.ChildA)
	if err := verifyParentFileHash(plan.Paths.Parent, result.ParentHashBefore); err != nil {
		return result, err
	}

	childBMount := filepath.Join(plan.Paths.Mounts, "child-b")
	if err := os.Mkdir(childBMount, 0700); err != nil {
		return result, err
	}
	childB, err := attachVHDX(plan.Paths.ChildB, false)
	if err != nil {
		return result, err
	}
	result.ChildBPhysicalPath = childB.physicalPath
	if err := childB.mountExisting(ctx, childBMount, false); err != nil {
		if closeErr := childB.close(ctx); closeErr != nil {
			safeToDelete = false
			return result, errors.Join(err, closeErr)
		}
		return result, err
	}
	if err := verifyChildBIsBaseline(childBMount, baselineHash); err != nil {
		return result, closeAfterFailure(childB, err)
	}
	childBHash, err := mutateChildB(childBMount, plan.Config.PayloadBytes)
	if err != nil {
		return result, closeAfterFailure(childB, err)
	}
	if err := childB.close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}

	childAReattachMount := filepath.Join(plan.Paths.Mounts, "child-a-reattach")
	if err := os.Mkdir(childAReattachMount, 0700); err != nil {
		return result, err
	}
	childAReattach, err := attachVHDX(plan.Paths.ChildA, false)
	if err != nil {
		return result, err
	}
	if err := childAReattach.mountExisting(ctx, childAReattachMount, false); err != nil {
		return result, closeAfterFailure(childAReattach, err)
	}
	if err := verifyChildAReattach(childAReattachMount, childAHash); err != nil {
		return result, closeAfterFailure(childAReattach, err)
	}
	if err := childAReattach.close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}
	result.Metrics.ChildAfterReattachFileBytes, _ = regularFileSize(plan.Paths.ChildA)
	result.ReattachPersistencePassed = true

	childBVerifyMount := filepath.Join(plan.Paths.Mounts, "child-b-verify")
	if err := os.Mkdir(childBVerifyMount, 0700); err != nil {
		return result, err
	}
	childBVerify, err := attachVHDX(plan.Paths.ChildB, true)
	if err != nil {
		return result, err
	}
	if err := childBVerify.mountExisting(ctx, childBVerifyMount, true); err != nil {
		return result, closeAfterFailure(childBVerify, err)
	}
	if err := verifyChildBMutation(childBVerifyMount, childBHash); err != nil {
		return result, closeAfterFailure(childBVerify, err)
	}
	if err := childBVerify.close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}
	result.SiblingIsolationPassed = true

	parentVerifyMount := filepath.Join(plan.Paths.Mounts, "parent-verify")
	if err := os.Mkdir(parentVerifyMount, 0700); err != nil {
		return result, err
	}
	parentVerify, err := attachVHDX(plan.Paths.Parent, true)
	if err != nil {
		return result, err
	}
	if err := parentVerify.mountExisting(ctx, parentVerifyMount, true); err != nil {
		return result, closeAfterFailure(parentVerify, err)
	}
	if err := verifyParentBaseline(parentVerifyMount, baselineHash); err != nil {
		return result, closeAfterFailure(parentVerify, err)
	}
	if err := parentVerify.close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}
	result.ParentHashAfter, err = hashFile(plan.Paths.Parent)
	if err != nil {
		return result, err
	}
	if result.ParentHashAfter != result.ParentHashBefore {
		return result, probeError(
			CodeParentMutated,
			"verify-parent-file",
			plan.Paths.Parent,
			fmt.Errorf("parent VHDX hash changed"),
		)
	}
	result.ParentIsolationPassed = true
	return result, nil
}

func attachVHDX(path string, readOnly bool) (*attachedVHDX, error) {
	handle, err := openVirtualDisk(path, readOnly)
	if err != nil {
		return nil, err
	}
	if err := attachVirtualDisk(handle, path, readOnly); err != nil {
		_ = closeVirtualDiskHandle(handle)
		return nil, err
	}
	physicalPath, err := getVirtualDiskPhysicalPath(handle, path)
	if err != nil {
		detachErr := detachVirtualDisk(handle, path)
		closeErr := closeVirtualDiskHandle(handle)
		return nil, errors.Join(err, detachErr, closeErr)
	}
	diskNumber, err := diskNumberFromPhysicalPath(physicalPath)
	if err != nil {
		detachErr := detachVirtualDisk(handle, path)
		closeErr := closeVirtualDiskHandle(handle)
		return nil, errors.Join(err, detachErr, closeErr)
	}
	return &attachedVHDX{
		handle:       handle,
		path:         path,
		physicalPath: physicalPath,
		diskNumber:   diskNumber,
	}, nil
}

func (disk *attachedVHDX) initializeAndMount(ctx context.Context, mountPath string) error {
	if err := disk.runStorageScript(ctx, "initialize-and-mount", mountPath, false, initializeDiskScript); err != nil {
		return err
	}
	disk.mountPath = mountPath
	disk.mounted = true
	return nil
}

func (disk *attachedVHDX) mountExisting(ctx context.Context, mountPath string, readOnly bool) error {
	if err := disk.runStorageScript(ctx, "mount-existing", mountPath, readOnly, mountDiskScript); err != nil {
		return probeError(CodeMountResolutionFailed, "mount-existing", disk.path, err)
	}
	disk.mountPath = mountPath
	disk.mounted = true
	return nil
}

func (disk *attachedVHDX) close(ctx context.Context) error {
	var errs []error
	if disk.mounted {
		if err := disk.runStorageScript(ctx, "unmount", disk.mountPath, false, unmountDiskScript); err != nil {
			errs = append(errs, probeError(CodeCleanupFailed, "unmount", disk.mountPath, err))
		} else {
			disk.mounted = false
		}
	}
	if err := detachVirtualDisk(disk.handle, disk.path); err != nil {
		errs = append(errs, err)
	}
	if err := closeVirtualDiskHandle(disk.handle); err != nil {
		errs = append(errs, probeError(CodeCleanupFailed, "close-handle", disk.path, err))
	}
	disk.handle = 0
	return errors.Join(errs...)
}

func (disk *attachedVHDX) runStorageScript(
	ctx context.Context,
	operation string,
	mountPath string,
	readOnly bool,
	script string,
) error {
	command := exec.CommandContext(
		ctx,
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		script,
	)
	command.Env = append(
		os.Environ(),
		"TESTPLAY_VHDX_DISK_NUMBER="+strconv.Itoa(disk.diskNumber),
		"TESTPLAY_VHDX_MOUNT_PATH="+mountPath,
		"TESTPLAY_VHDX_READ_ONLY="+boolDigit(readOnly),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", operation, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func diskNumberFromPhysicalPath(path string) (int, error) {
	match := physicalDrivePattern.FindStringSubmatch(path)
	if len(match) != 2 {
		return 0, probeError(
			CodeUnsafePhysicalDisk,
			"parse-physical-path",
			path,
			fmt.Errorf("not a PhysicalDrive path"),
		)
	}
	number, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, probeError(CodeUnsafePhysicalDisk, "parse-disk-number", path, err)
	}
	return number, nil
}

func isElevated(ctx context.Context) (bool, error) {
	command := exec.CommandContext(
		ctx,
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		adminCheckScript,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.EqualFold(strings.TrimSpace(string(output)), "true"), nil
}

func cancellationError(ctx context.Context, operation, path string) error {
	if err := ctx.Err(); err != nil {
		return probeError(CodeCancelled, operation, path, err)
	}
	return nil
}

func seedParent(mountPath string, payloadBytes int64) (string, error) {
	baseline := filepath.Join(mountPath, "baseline")
	if err := os.Mkdir(baseline, 0700); err != nil {
		return "", err
	}
	payloadPath := filepath.Join(baseline, "payload.bin")
	file, err := os.OpenFile(payloadPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	const blockBytes = 1 << 20
	block := make([]byte, blockBytes)
	var written int64
	for index := uint64(0); written < payloadBytes; index++ {
		fillDeterministicBlock(block, index, 0x41)
		toWrite := int64(len(block))
		if remaining := payloadBytes - written; remaining < toWrite {
			toWrite = remaining
		}
		if _, err := writer.Write(block[:toWrite]); err != nil {
			_ = file.Close()
			return "", err
		}
		written += toWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	manifest, err := json.MarshalIndent(map[string]any{
		"payloadBytes": payloadBytes,
		"sha256":       digest,
		"pattern":      "block-index-v1",
	}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(baseline, "manifest.json"), manifest, 0600); err != nil {
		return "", err
	}
	return digest, nil
}

func fillDeterministicBlock(block []byte, index uint64, salt byte) {
	for position := range block {
		block[position] = byte((uint64(position)*31 + index*17 + uint64(salt)) % 251)
	}
	binary.LittleEndian.PutUint64(block[:8], index)
}

func mutateChildA(mountPath string, payloadBytes int64) (string, error) {
	payload := filepath.Join(mountPath, "baseline", "payload.bin")
	if err := overwritePayloadRegion(payload, payloadBytes/2, 0xa1); err != nil {
		return "", err
	}
	baseline := filepath.Join(mountPath, "baseline")
	if err := os.Rename(
		filepath.Join(baseline, "manifest.json"),
		filepath.Join(baseline, "manifest-child-a.json"),
	); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(baseline, "child-a-marker.txt"), []byte("child-a\n"), 0600); err != nil {
		return "", err
	}
	for index := 0; index < 4; index++ {
		path := filepath.Join(baseline, fmt.Sprintf("child-a-small-%d.txt", index))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("child-a-%d\n", index)), 0600); err != nil {
			return "", err
		}
	}
	return hashFile(payload)
}

func mutateChildB(mountPath string, payloadBytes int64) (string, error) {
	payload := filepath.Join(mountPath, "baseline", "payload.bin")
	if err := overwritePayloadRegion(payload, payloadBytes/4, 0xb2); err != nil {
		return "", err
	}
	if err := os.WriteFile(
		filepath.Join(mountPath, "baseline", "child-b-marker.txt"),
		[]byte("child-b\n"),
		0600,
	); err != nil {
		return "", err
	}
	return hashFile(payload)
}

func overwritePayloadRegion(path string, offset int64, salt byte) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	const mutationBytes = 1 << 20
	offset -= offset % mutationBytes
	block := make([]byte, mutationBytes)
	fillDeterministicBlock(block, uint64(offset/mutationBytes), salt)
	if _, err := file.WriteAt(block, offset); err != nil {
		return err
	}
	return file.Sync()
}

func verifyPayloadHash(mountPath, expected string) error {
	actual, err := hashFile(filepath.Join(mountPath, "baseline", "payload.bin"))
	if err != nil {
		return probeError(CodeVerificationFailed, "hash-payload", mountPath, err)
	}
	if actual != expected {
		return probeError(
			CodeVerificationFailed,
			"verify-payload",
			mountPath,
			fmt.Errorf("hash %s does not match baseline %s", actual, expected),
		)
	}
	return nil
}

func verifyChildBIsBaseline(mountPath, baselineHash string) error {
	if err := verifyPayloadHash(mountPath, baselineHash); err != nil {
		return probeError(CodeSiblingIsolationFailed, "verify-child-b-baseline", mountPath, err)
	}
	for _, name := range []string{"manifest-child-a.json", "child-a-marker.txt"} {
		if _, err := os.Stat(filepath.Join(mountPath, "baseline", name)); !os.IsNotExist(err) {
			return probeError(
				CodeSiblingIsolationFailed,
				"verify-child-a-marker-absent",
				filepath.Join(mountPath, "baseline", name),
				fmt.Errorf("Child A mutation leaked into Child B"),
			)
		}
	}
	return nil
}

func verifyChildAReattach(mountPath, expectedHash string) error {
	actual, err := hashFile(filepath.Join(mountPath, "baseline", "payload.bin"))
	if err != nil || actual != expectedHash {
		return probeError(
			CodeReattachPersistenceFailed,
			"verify-child-a-payload",
			mountPath,
			fmt.Errorf("payload hash=%q expected=%q: %w", actual, expectedHash, err),
		)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "baseline", "child-a-marker.txt")); err != nil {
		return probeError(CodeReattachPersistenceFailed, "verify-child-a-marker", mountPath, err)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "baseline", "child-b-marker.txt")); !os.IsNotExist(err) {
		return probeError(
			CodeSiblingIsolationFailed,
			"verify-child-b-marker-absent",
			mountPath,
			fmt.Errorf("Child B mutation leaked into Child A"),
		)
	}
	return nil
}

func verifyChildBMutation(mountPath, expectedHash string) error {
	actual, err := hashFile(filepath.Join(mountPath, "baseline", "payload.bin"))
	if err != nil || actual != expectedHash {
		return probeError(
			CodeSiblingIsolationFailed,
			"verify-child-b-payload",
			mountPath,
			fmt.Errorf("payload hash=%q expected=%q: %w", actual, expectedHash, err),
		)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "baseline", "child-b-marker.txt")); err != nil {
		return probeError(CodeSiblingIsolationFailed, "verify-child-b-marker", mountPath, err)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "baseline", "child-a-marker.txt")); !os.IsNotExist(err) {
		return probeError(
			CodeSiblingIsolationFailed,
			"verify-child-a-marker-absent",
			mountPath,
			fmt.Errorf("Child A mutation leaked into Child B"),
		)
	}
	return nil
}

func verifyParentBaseline(mountPath, baselineHash string) error {
	if err := verifyPayloadHash(mountPath, baselineHash); err != nil {
		return probeError(CodeParentMutated, "verify-parent-payload", mountPath, err)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "baseline", "manifest.json")); err != nil {
		return probeError(CodeParentMutated, "verify-parent-manifest", mountPath, err)
	}
	for _, name := range []string{
		"manifest-child-a.json",
		"child-a-marker.txt",
		"child-b-marker.txt",
	} {
		if _, err := os.Stat(filepath.Join(mountPath, "baseline", name)); !os.IsNotExist(err) {
			return probeError(
				CodeParentMutated,
				"verify-child-marker-absent",
				filepath.Join(mountPath, "baseline", name),
				fmt.Errorf("child mutation leaked into Parent"),
			)
		}
	}
	return nil
}

func verifyParentFileHash(path, expected string) error {
	actual, err := hashFile(path)
	if err != nil {
		return probeError(CodeVerificationFailed, "hash-parent", path, err)
	}
	if actual != expected {
		return probeError(
			CodeParentMutated,
			"verify-parent-file",
			path,
			fmt.Errorf("hash %s does not match %s", actual, expected),
		)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func regularFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", path)
	}
	return info.Size(), nil
}

func boolDigit(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
