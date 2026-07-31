//go:build windows

package unityvhdxfixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/storagehelper"
	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

const DefaultParentVirtualBytes int64 = 4 << 30

var (
	fixtureKernel32               = syscall.NewLazyDLL("kernel32.dll")
	fixtureGetCompressedFileSizeW = fixtureKernel32.NewProc("GetCompressedFileSizeW")
)

type ParentFixture struct {
	Path                string
	VirtualBytes        int64
	LogicalBytes        int64
	AllocatedBytes      *int64
	LibraryLogicalBytes int64
	Hash                string
	CreateMs            int64
	AttachMs            int64
	InitializeMs        int64
	SeedMs              int64
	DetachMs            int64
}

func PrepareParent(ctx context.Context, parentPath, mountPath string, virtualBytes int64, seed func(string) error) (ParentFixture, error) {
	result := ParentFixture{Path: parentPath, VirtualBytes: virtualBytes}
	if virtualBytes <= 0 {
		return result, fixtureError(CodeInvalidFixtureRoot, "validate-parent-size", parentPath, fmt.Errorf("size must be positive"))
	}
	if _, err := os.Lstat(parentPath); err == nil {
		return result, fixtureError(CodeInvalidFixtureRoot, "create-parent", parentPath, fmt.Errorf("path already exists"))
	} else if !os.IsNotExist(err) {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(parentPath), 0700); err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(mountPath), 0700); err != nil {
		return result, err
	}
	if err := os.Mkdir(mountPath, 0700); err != nil {
		return result, err
	}
	started := time.Now()
	if err := vhdxstorage.CreateDynamic(parentPath, virtualBytes); err != nil {
		return result, err
	}
	result.CreateMs = time.Since(started).Milliseconds()
	attachment, err := vhdxstorage.Open(parentPath, false)
	if err != nil {
		return result, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = attachment.Close(context.Background())
		}
	}()
	started = time.Now()
	if err := attachment.Attach(false); err != nil {
		return result, err
	}
	if _, err := attachment.ResolvePhysicalPath(); err != nil {
		return result, err
	}
	result.AttachMs = time.Since(started).Milliseconds()
	started = time.Now()
	if err := attachment.InitializeAndMount(ctx, mountPath); err != nil {
		return result, err
	}
	result.InitializeMs = time.Since(started).Milliseconds()
	started = time.Now()
	if err := seed(mountPath); err != nil {
		return result, err
	}
	result.SeedMs = time.Since(started).Milliseconds()
	usage, err := shadow.MeasureDirectoryUsage(mountPath)
	if err != nil {
		return result, err
	}
	result.LibraryLogicalBytes = usage.LogicalBytes
	if size, err := attachment.Size(); err == nil {
		result.VirtualBytes = int64(size.VirtualSize)
	}
	started = time.Now()
	if err := attachment.Close(ctx); err != nil {
		return result, err
	}
	closed = true
	result.DetachMs = time.Since(started).Milliseconds()
	if err := os.Remove(mountPath); err != nil {
		return result, err
	}
	info, err := os.Stat(parentPath)
	if err != nil {
		return result, err
	}
	result.LogicalBytes = info.Size()
	result.AllocatedBytes, _ = AllocatedFileBytes(parentPath)
	result.Hash, err = HashFile(parentPath)
	if err != nil {
		return result, err
	}
	return result, nil
}

func HashFile(path string) (string, error) {
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

func AllocatedFileBytes(path string) (*int64, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var high uint32
	low, _, callErr := fixtureGetCompressedFileSizeW.Call(uintptr(unsafe.Pointer(pathPtr)), uintptr(unsafe.Pointer(&high)))
	runtime.KeepAlive(pathPtr)
	if low == 0xffffffff {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return nil, errno
		}
	}
	value := int64((uint64(high) << 32) | uint64(uint32(low)))
	return &value, nil
}

func FileSizes(path string) (*int64, *int64) {
	var logical *int64
	if info, err := os.Stat(path); err == nil {
		value := info.Size()
		logical = &value
	}
	allocated, _ := AllocatedFileBytes(path)
	return logical, allocated
}

func InspectParentFile(ctx context.Context, parentPath, mountPath, relativePath string) (bool, error) {
	if err := os.Mkdir(mountPath, 0700); err != nil {
		return false, err
	}
	attachment, err := vhdxstorage.OpenAndAttach(parentPath, true)
	if err != nil {
		return false, err
	}
	if err := attachment.MountExisting(ctx, mountPath, true); err != nil {
		return false, errors.Join(err, attachment.Close(ctx))
	}
	_, statErr := os.Stat(filepath.Join(mountPath, relativePath))
	closeErr := attachment.Close(ctx)
	removeErr := os.Remove(mountPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return false, errors.Join(statErr, closeErr, removeErr)
	}
	return statErr == nil, errors.Join(closeErr, removeErr)
}

type DiskSnapshot struct {
	Number       int    `json:"number"`
	SerialNumber string `json:"serialNumber"`
}

func FileBackedDisks(ctx context.Context) ([]DiskSnapshot, error) {
	script := `$value = [pscustomobject]@{ items = @(Get-Disk | Where-Object { $_.BusType.ToString() -eq 'File Backed Virtual' } | Sort-Object Number | ForEach-Object { [pscustomobject]@{ number = $_.Number; serialNumber = $_.SerialNumber } }) }; $value | ConvertTo-Json -Compress -Depth 4`
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("snapshot File Backed Virtual disks: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var envelope struct {
		Items []DiskSnapshot `json:"items"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("decode disk snapshot: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return envelope.Items, nil
}

type MountSnapshot struct {
	Phase          string `json:"phase"`
	Exists         bool   `json:"exists"`
	DirectoryMount bool   `json:"directoryMount"`
	PhysicalPath   string `json:"physicalPath,omitempty"`
	VolumeGUIDPath string `json:"volumeGuidPath,omitempty"`
	MountPath      string `json:"mountPath"`
	BusType        string `json:"busType,omitempty"`
}

func InspectActiveMount(ctx context.Context, lease storagehelper.WorkspaceLease, phase string) (MountSnapshot, error) {
	diskNumber, err := vhdxstorage.DiskNumberFromPhysicalPath(lease.PhysicalPath)
	if err != nil {
		return MountSnapshot{}, err
	}
	script := `$ErrorActionPreference='Stop'; $disk=Get-Disk -Number ([int]$env:TESTPLAY_FIXTURE_DISK) -ErrorAction Stop; $mount=$env:TESTPLAY_FIXTURE_MOUNT.TrimEnd('\')+'\'; $parts=@(Get-Partition -DiskNumber $disk.Number | Where-Object { @($_.AccessPaths) -contains $mount }); if($parts.Count -ne 1){throw "expected one mounted partition; found $($parts.Count)"}; $volume=Get-Volume -Partition $parts[0] -ErrorAction Stop; [pscustomobject]@{ exists=(Test-Path -LiteralPath $mount); directoryMount=(@($parts[0].AccessPaths)-contains $mount); physicalPath=('\\.\PhysicalDrive'+$disk.Number); volumeGuidPath=$volume.Path; mountPath=$mount.TrimEnd('\'); busType=$disk.BusType.ToString() } | ConvertTo-Json -Compress`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), fmt.Sprintf("TESTPLAY_FIXTURE_DISK=%d", diskNumber), "TESTPLAY_FIXTURE_MOUNT="+lease.MountPath)
	output, err := command.CombinedOutput()
	if err != nil {
		return MountSnapshot{}, fixtureError(CodeLibraryMountLost, "inspect-mount", lease.MountPath, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output))))
	}
	var snapshot MountSnapshot
	if err := json.Unmarshal(output, &snapshot); err != nil {
		return snapshot, err
	}
	snapshot.Phase = phase
	if !snapshot.Exists || !snapshot.DirectoryMount {
		return snapshot, fixtureError(CodeLibraryMountReplaced, "inspect-mount", lease.MountPath, fmt.Errorf("mount is absent or no longer a directory mount"))
	}
	if !strings.EqualFold(snapshot.PhysicalPath, lease.PhysicalPath) || !strings.EqualFold(snapshot.BusType, "File Backed Virtual") {
		return snapshot, fixtureError(CodeLibraryMountReplaced, "inspect-mount", lease.MountPath, fmt.Errorf("physical disk identity changed"))
	}
	if !strings.EqualFold(strings.TrimRight(snapshot.VolumeGUIDPath, "\\"), strings.TrimRight(lease.VolumeGUIDPath, "\\")) {
		return snapshot, fixtureError(CodeLibraryVolumeChanged, "inspect-mount", lease.MountPath, fmt.Errorf("expected=%s actual=%s", lease.VolumeGUIDPath, snapshot.VolumeGUIDPath))
	}
	return snapshot, nil
}

func InspectReleasedMount(lease storagehelper.WorkspaceLease) (MountSnapshot, error) {
	snapshot := MountSnapshot{Phase: MountReleased, MountPath: lease.MountPath, PhysicalPath: lease.PhysicalPath, VolumeGUIDPath: lease.VolumeGUIDPath}
	if _, err := os.Stat(lease.MountPath); err == nil {
		return snapshot, fixtureError(CodeCleanupFailed, "verify-released-mount", lease.MountPath, fmt.Errorf("mount path remains"))
	} else if !os.IsNotExist(err) {
		return snapshot, err
	}
	snapshot.Exists = false
	return snapshot, nil
}

func WriteJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}
