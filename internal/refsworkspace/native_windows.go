//go:build windows

package refsworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
	"golang.org/x/sys/windows"
)

const fileSupportsBlockRefcounting = 0x08000000

type windowsPoolNative struct{}

func newPoolNative() PoolNative            { return windowsPoolNative{} }
func (windowsPoolNative) Platform() string { return runtime.GOOS }
func (windowsPoolNative) EnsureAvailable(ctx context.Context) error {
	if err := vhdxstorage.EnsureAvailable(); err != nil {
		return err
	}
	return vhdxstorage.EnsureDevDriveAvailable(ctx)
}
func (windowsPoolNative) IsElevated(ctx context.Context) (bool, error) {
	return vhdxstorage.IsElevated(ctx)
}
func (windowsPoolNative) CreateDynamic(path string, maximumBytes int64) error {
	return vhdxstorage.CreateDynamic(path, maximumBytes)
}

type windowsMountedPool struct {
	attachment *vhdxstorage.Attachment
	volume     VolumeInfo
	metrics    NativeMountMetrics
	devDrive   DevDriveEvidence
}

func (pool *windowsMountedPool) Volume() VolumeInfo                 { return pool.volume }
func (pool *windowsMountedPool) Metrics() NativeMountMetrics        { return pool.metrics }
func (pool *windowsMountedPool) DevDriveEvidence() DevDriveEvidence { return pool.devDrive }

func (pool *windowsMountedPool) WaitReady(ctx context.Context, paths Paths, expected PoolMetadata) (time.Duration, error) {
	return waitForMountedPoolReady(ctx, paths, expected, pool.volume, osMountedPoolReadinessInspector{}, mountedPoolReadinessOptions{})
}

func (pool *windowsMountedPool) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	volumePath := strings.TrimSuffix(pool.volume.VolumeGUIDPath, `\`)
	path, err := windows.UTF16PtrFromString(volumePath)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return fmt.Errorf("open exact volume for flush: %w", err)
	}
	defer windows.CloseHandle(handle)
	if err := windows.FlushFileBuffers(handle); err != nil {
		return fmt.Errorf("FlushFileBuffers exact volume: %w", err)
	}
	return nil
}

func (pool *windowsMountedPool) Close(ctx context.Context) error {
	if pool.attachment == nil {
		return nil
	}
	var errs []error
	if _, _, err := pool.attachment.Unmount(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := pool.attachment.Detach(); err != nil {
		errs = append(errs, err)
	} else {
		if _, _, err := pool.attachment.WaitDetached(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := pool.attachment.CloseHandle(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		pool.attachment = nil
	}
	return errors.Join(errs...)
}

func (windowsPoolNative) Mount(ctx context.Context, vhdxPath, mountPath string, initialize bool) (MountedPool, error) {
	if err := validateUnmountedMountDirectory(mountPath); err != nil {
		return nil, err
	}
	attachStarted := time.Now()
	attachment, err := vhdxstorage.OpenAndAttach(vhdxPath, false)
	if err != nil {
		mapped := mapNativeError("open-and-attach-vhdx", vhdxPath, err)
		return nil, errorWithCleanupEvidence(mapped, "released", vhdxPath, false)
	}
	attachMs := time.Since(attachStarted).Milliseconds()
	fail := func(primary error) (MountedPool, error) {
		cleanupPool := &windowsMountedPool{attachment: attachment}
		cleanupErr := closeMountedBounded(cleanupPool)
		if cleanupErr != nil {
			return nil, cleanupFailure("cleanup-failed-mount", vhdxPath, errors.Join(primary, cleanupErr), false)
		}
		mapped := mapNativeError("mount-or-initialize-volume", vhdxPath, primary)
		return nil, errorWithCleanupEvidence(mapped, "released", vhdxPath, false)
	}
	var refsInfo vhdxstorage.ReFSVolumeInfo
	mountStarted := time.Now()
	if initialize {
		refsInfo, err = attachment.InitializeDevDriveAndMount(ctx, mountPath)
	} else {
		if err = attachment.MountExisting(ctx, mountPath, false); err == nil {
			refsInfo, err = attachment.InspectDevDriveVolume(ctx)
		}
	}
	if err != nil {
		return fail(err)
	}
	if reparse, err := pathIsReparsePoint(mountPath); err != nil || !reparse {
		return fail(newError(CodePoolCorrupt, "verify-directory-mount", mountPath, errors.Join(err, fmt.Errorf("mounted path is not a reparse point"))))
	}
	volume, err := inspectMountedVolume(mountPath, refsInfo)
	if err != nil {
		return fail(err)
	}
	return &windowsMountedPool{
		attachment: attachment,
		volume:     volume,
		metrics:    NativeMountMetrics{AttachMs: attachMs, MountMs: time.Since(mountStarted).Milliseconds()},
		devDrive: DevDriveEvidence{
			FormatAttempted:              refsInfo.DevDrive.FormatAttempted,
			FormatSucceeded:              refsInfo.DevDrive.FormatSucceeded,
			QueryExitCode:                refsInfo.DevDrive.QueryExitCode,
			QueryOutput:                  refsInfo.DevDrive.QueryOutput,
			TemporaryDriveLetterAssigned: refsInfo.DevDrive.TemporaryDriveLetterAssigned,
			TemporaryDriveLetterRemoved:  refsInfo.DevDrive.TemporaryDriveLetterRemoved,
			PrivateMountVerified:         refsInfo.DevDrive.PrivateMountVerified,
		},
	}, nil
}

func validateUnmountedMountDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return newError(CodePoolNotMounted, "validate-mount-directory", path, err)
	}
	reparse, err := pathIsReparsePoint(path)
	if err != nil {
		return newError(CodePoolCorrupt, "inspect-mount-directory", path, err)
	}
	if reparse || info.Mode()&os.ModeSymlink != 0 {
		return newError(CodePoolAlreadyMounted, "validate-mount-directory", path, fmt.Errorf("mount path is already a reparse point"))
	}
	if !info.IsDir() {
		return newError(CodePoolCorrupt, "validate-mount-directory", path, fmt.Errorf("mount path is not a directory"))
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return newError(CodePoolCorrupt, "read-mount-directory", path, err)
	}
	if len(entries) != 0 {
		return newError(CodePoolCorrupt, "validate-mount-directory", path, fmt.Errorf("mount path is not empty"))
	}
	return nil
}

func pathIsReparsePoint(path string) (bool, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, err := windows.GetFileAttributes(ptr)
	if err != nil {
		return false, err
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}

func inspectMountedVolume(mountPath string, refsInfo vhdxstorage.ReFSVolumeInfo) (VolumeInfo, error) {
	path, err := windows.UTF16PtrFromString(mountPath)
	if err != nil {
		return VolumeInfo{}, err
	}
	handle, err := windows.CreateFile(path, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return VolumeInfo{}, err
	}
	defer windows.CloseHandle(handle)
	filesystem := make([]uint16, 64)
	var serial, maxComponent, flags uint32
	if err := windows.GetVolumeInformationByHandle(handle, nil, 0, &serial, &maxComponent, &flags, &filesystem[0], uint32(len(filesystem))); err != nil {
		return VolumeInfo{}, err
	}
	name := windows.UTF16ToString(filesystem)
	if !strings.EqualFold(name, refsInfo.Filesystem) {
		return VolumeInfo{}, fmt.Errorf("filesystem APIs disagree: handle=%q storage=%q", name, refsInfo.Filesystem)
	}
	root, err := windows.UTF16PtrFromString(mountPath)
	if err != nil {
		return VolumeInfo{}, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(root, &available, &total, &free); err != nil {
		return VolumeInfo{}, err
	}
	return VolumeInfo{
		VolumeGUIDPath:       refsInfo.VolumeGUIDPath,
		Filesystem:           name,
		ClusterSize:          refsInfo.ClusterSize,
		TotalBytes:           int64(total),
		FreeBytes:            int64(free),
		UsedBytes:            int64(total - free),
		SupportsBlockCloning: flags&fileSupportsBlockRefcounting != 0,
	}, nil
}

func (windowsPoolNative) FileIdentity(path string) (string, error) {
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

type fileStandardInformation struct {
	AllocationSize int64
	EndOfFile      int64
	NumberOfLinks  uint32
	DeletePending  byte
	Directory      byte
	_              [2]byte
}

func (windowsPoolNative) FileUsage(path string) (FileUsage, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return FileUsage{}, err
	}
	handle, err := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return FileUsage{}, err
	}
	defer windows.CloseHandle(handle)
	var info fileStandardInformation
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileStandardInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return FileUsage{}, err
	}
	return FileUsage{LogicalBytes: info.EndOfFile, AllocatedBytes: info.AllocationSize}, nil
}

func (windowsPoolNative) HostFilesystem(path string) (string, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	filesystem := make([]uint16, 64)
	var serial, maxComponent, flags uint32
	if err := windows.GetVolumeInformationByHandle(handle, nil, 0, &serial, &maxComponent, &flags, &filesystem[0], uint32(len(filesystem))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(filesystem), nil
}

func (windowsPoolNative) HostFreeBytes(path string) (int64, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &available, &total, &free); err != nil {
		return 0, err
	}
	return int64(available), nil
}

func (windowsPoolNative) RemoveVHDX(path string) error { return os.Remove(path) }
