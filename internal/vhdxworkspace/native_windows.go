//go:build windows

package vhdxworkspace

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
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
	"golang.org/x/sys/windows"
)

type windowsNative struct {
	backend       vhdxstorage.Backend
	bootSessionID string
}

func NewNative() Native {
	return windowsNative{backend: vhdxstorage.NewBackend(), bootSessionID: windowsBootSessionID()}
}
func (windowsNative) Platform() string             { return "windows" }
func (native windowsNative) BootSessionID() string { return native.bootSessionID }

func windowsBootSessionID() string {
	// The System process is created once for each Windows boot. Its creation
	// FILETIME remains stable across broker restarts but changes after reboot,
	// unlike a PID which Windows can reuse for an unrelated process.
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, 4)
	if err == nil {
		defer windows.CloseHandle(handle)
		var creation, exit, kernel, user windows.Filetime
		if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err == nil {
			return fmt.Sprintf("system-process-%08x%08x", creation.HighDateTime, creation.LowDateTime)
		}
	}
	// An empty identity retains the conservative legacy PID/grace behavior; it
	// never claims that a reboot was measured when Windows denied the query.
	return ""
}
func (windowsNative) ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	windows.CloseHandle(handle)
	return true
}
func (native windowsNative) Available(ctx context.Context) error {
	if err := vhdxstorage.EnsureAvailable(); err != nil {
		return err
	}
	elevated, err := native.backend.IsElevated(ctx)
	if err != nil {
		return err
	}
	if !elevated {
		return fmt.Errorf("%w: broker must run elevated", ErrBrokerUnavailable)
	}
	return nil
}

func (windowsNative) VerifyParent(_ context.Context, metadata ParentMetadata) error {
	info, err := os.Lstat(metadata.VHDXPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(err, ErrOwnershipMismatch)
	}
	if info.Mode().Perm()&0222 != 0 {
		return fmt.Errorf("%w: parent is writable", ErrOwnershipMismatch)
	}
	if info.Size() != metadata.LogicalBytes || !info.ModTime().UTC().Equal(metadata.FileWriteTime.UTC()) {
		return fmt.Errorf("%w: parent size/write time changed", ErrOwnershipMismatch)
	}
	identity, err := vhdxstorage.FileIdentity(metadata.VHDXPath)
	if err != nil || identity != metadata.FileIdentity.FileID {
		return errors.Join(err, fmt.Errorf("%w: parent file identity changed", ErrOwnershipMismatch))
	}
	attachment, err := vhdxstorage.Open(metadata.VHDXPath, true)
	if err != nil {
		return err
	}
	defer attachment.CloseHandle()
	diskID, err := attachment.VirtualDiskID()
	if err != nil || !strings.EqualFold(diskID, metadata.Volume.VirtualDiskID) {
		return errors.Join(err, fmt.Errorf("%w: parent virtual disk identity changed", ErrOwnershipMismatch))
	}
	size, err := attachment.Size()
	if err != nil || int64(size.VirtualSize) != metadata.VirtualBytes || int64(size.BlockSize) != metadata.BlockBytes || int64(size.SectorSize) != metadata.SectorBytes {
		return errors.Join(err, fmt.Errorf("%w: parent geometry changed", ErrOwnershipMismatch))
	}
	return nil
}

type windowsParentSession struct {
	pending    *PendingParent
	attachment *vhdxstorage.Attachment
	evidence   ParentEvidence
	closed     bool
}

func (s *windowsParentSession) Evidence() ParentEvidence { return s.evidence }

func (native windowsNative) BeginParent(ctx context.Context, pending *PendingParent) (ParentSession, error) {
	if pending == nil || pending.StagingPath == "" || pending.MountPath == "" {
		return nil, ErrInvalidInput
	}
	if err := os.MkdirAll(filepath.Dir(pending.StagingPath), 0700); err != nil {
		return nil, err
	}
	if err := ensureEmptyMount(pending.MountPath); err != nil {
		return nil, err
	}
	if err := vhdxstorage.CreateDynamicWithOptions(pending.StagingPath, vhdxstorage.CreateOptions{
		MaximumSize: pending.Key.VirtualBytes, BlockSizeInBytes: uint32(pending.Key.BlockBytes),
		SectorSizeInBytes: uint32(pending.Key.SectorBytes),
	}); err != nil {
		return nil, err
	}
	attachment, err := vhdxstorage.Open(pending.StagingPath, false)
	if err != nil {
		_ = os.Remove(pending.StagingPath)
		return nil, err
	}
	fail := func(primary error) (ParentSession, error) {
		cleanup := attachment.Close(context.Background())
		if cleanup == nil {
			cleanup = os.Remove(pending.StagingPath)
		}
		_ = os.Remove(pending.MountPath)
		return nil, errors.Join(primary, cleanup)
	}
	size, err := attachment.Size()
	if err != nil || int64(size.VirtualSize) != pending.Key.VirtualBytes || int64(size.BlockSize) != pending.Key.BlockBytes || int64(size.SectorSize) != pending.Key.SectorBytes {
		return fail(errors.Join(err, fmt.Errorf("parent geometry virtual=%d block=%d sector=%d", size.VirtualSize, size.BlockSize, size.SectorSize)))
	}
	virtualDiskID, err := attachment.VirtualDiskID()
	if err != nil {
		return fail(err)
	}
	if err := attachment.Attach(false); err != nil {
		return fail(err)
	}
	if _, err := attachment.ResolvePhysicalPath(); err != nil {
		return fail(err)
	}
	if err := attachment.InitializeAndMount(ctx, pending.MountPath); err != nil {
		return fail(err)
	}
	volume, err := inspectVolume(ctx, pending.MountPath, attachment.VolumeGUIDPath())
	if err != nil {
		return fail(err)
	}
	if !strings.EqualFold(volume.Filesystem, "NTFS") {
		return fail(fmt.Errorf("parent filesystem=%s, want NTFS", volume.Filesystem))
	}
	volume.VirtualDiskID = virtualDiskID
	return &windowsParentSession{pending: pending, attachment: attachment, evidence: ParentEvidence{Volume: volume}}, nil
}

func (s *windowsParentSession) Finalize(ctx context.Context) (ParentEvidence, error) {
	if s.closed {
		return s.evidence, fmt.Errorf("parent session is closed")
	}
	if err := s.attachment.Flush(ctx); err != nil {
		return s.evidence, err
	}
	if _, _, err := s.attachment.Unmount(ctx); err != nil {
		return s.evidence, err
	}
	if err := s.attachment.Detach(); err != nil {
		return s.evidence, err
	}
	if _, _, err := s.attachment.WaitDetached(ctx); err != nil {
		return s.evidence, err
	}
	if err := s.attachment.CloseHandle(); err != nil {
		return s.evidence, err
	}
	s.closed = true
	_ = os.Remove(s.pending.MountPath)

	verifyMount := s.pending.MountPath + ".verify-" + s.pending.TransactionID
	if err := ensureEmptyMount(verifyMount); err != nil {
		return s.evidence, err
	}
	verify, err := vhdxstorage.OpenAndAttach(s.pending.StagingPath, true)
	if err != nil {
		return s.evidence, err
	}
	verifiedDiskID, diskIDErr := verify.VirtualDiskID()
	if diskIDErr != nil || !strings.EqualFold(verifiedDiskID, s.evidence.Volume.VirtualDiskID) {
		return s.evidence, errors.Join(diskIDErr, fmt.Errorf("virtual disk identity changed: got=%s want=%s", verifiedDiskID, s.evidence.Volume.VirtualDiskID), verify.Close(ctx), os.Remove(verifyMount))
	}
	if err := verify.MountExisting(ctx, verifyMount, true); err != nil {
		return s.evidence, errors.Join(err, verify.Close(ctx))
	}
	volume, volumeErr := inspectVolume(ctx, verifyMount, verify.VolumeGUIDPath())
	entries, listingErr := os.ReadDir(verifyMount)
	closeErr := verify.Close(ctx)
	removeErr := os.Remove(verifyMount)
	if volumeErr != nil || listingErr != nil || len(entries) == 0 || !strings.EqualFold(volume.Filesystem, "NTFS") {
		return s.evidence, errors.Join(volumeErr, listingErr, fmt.Errorf("verified parent Library root is empty or non-NTFS"), closeErr, removeErr)
	}
	if closeErr != nil || removeErr != nil {
		return s.evidence, errors.Join(closeErr, removeErr)
	}
	identity, err := vhdxstorage.FileIdentity(s.pending.StagingPath)
	if err != nil {
		return s.evidence, err
	}
	usage, err := vhdxstorage.FileUsageOf(s.pending.StagingPath)
	if err != nil {
		return s.evidence, err
	}
	digest, err := hashFile(s.pending.StagingPath)
	if err != nil {
		return s.evidence, err
	}
	fileInfo, err := os.Stat(s.pending.StagingPath)
	if err != nil {
		return s.evidence, err
	}
	s.evidence = ParentEvidence{
		FileIdentity: FileIdentity{FileID: identity}, Volume: volume,
		LogicalBytes: usage.LogicalBytes, AllocatedBytes: usage.AllocatedBytes, SHA256: digest, FileWriteTime: fileInfo.ModTime().UTC(),
	}
	s.evidence.Volume.VirtualDiskID = verifiedDiskID
	return s.evidence, nil
}

func (s *windowsParentSession) Abort(ctx context.Context) error {
	var err error
	if !s.closed && s.attachment != nil {
		err = s.attachment.Close(ctx)
		s.closed = err == nil
	}
	if err == nil {
		err = os.Remove(s.pending.StagingPath)
		if os.IsNotExist(err) {
			err = nil
		}
	}
	_ = os.Remove(s.pending.MountPath)
	return err
}

type windowsChildSession struct {
	lease    Lease
	backend  vhdxstorage.Lease
	path     string
	identity FileIdentity
}

func (s *windowsChildSession) Info() Lease                { return s.lease }
func (s *windowsChildSession) FileIdentity() FileIdentity { return s.identity }
func (s *windowsChildSession) Usage() (int64, error) {
	usage, err := vhdxstorage.FileUsageOf(s.path)
	return usage.AllocatedBytes, err
}

func (s *windowsChildSession) Release(ctx context.Context, deleteChild bool) (Metrics, error) {
	started := time.Now()
	metrics := Metrics{}
	_, err := s.backend.Release(ctx, deleteChild, nil)
	metrics.ChildReleaseMs = time.Since(started).Milliseconds()
	metrics.CleanupState = CleanupReleased
	if !deleteChild {
		metrics.CleanupState = CleanupRetained
	}
	if err != nil {
		metrics.CleanupState = CleanupUncertain
		return metrics, err
	}
	if usage, usageErr := vhdxstorage.FileUsageOf(s.path); usageErr == nil {
		metrics.ChildReleasedBytes = usage.AllocatedBytes
		metrics.ChildReleasedMeasured = true
	} else if deleteChild && !os.IsNotExist(usageErr) {
		return metrics, usageErr
	} else if deleteChild && os.IsNotExist(usageErr) {
		metrics.ChildReleasedMeasured = true
	}
	return metrics, nil
}

func (native windowsNative) AcquireChild(ctx context.Context, parent ParentMetadata, journal LeaseJournal, transition func(string, string, string) error) (ChildSession, Metrics, error) {
	progress := func(value vhdxstorage.Progress) error {
		if transition == nil {
			return nil
		}
		return transition(value.State, value.PhysicalPath, value.VolumeGUIDPath)
	}
	lease, raw, err := native.backend.Acquire(ctx, vhdxstorage.AcquireRequest{
		ParentPath: parent.VHDXPath, ChildPath: journal.ChildPath, MountPath: journal.MountPath,
		StoreRoot: filepath.Dir(filepath.Dir(journal.ChildPath)), LeaseID: journal.LeaseID,
	}, progress)
	metrics := Metrics{ParentStatus: ParentStatusValid, ParentReused: true, ParentVirtualBytes: parent.VirtualBytes, ParentAllocatedBytes: parent.AllocatedBytes}
	if raw.ChildCreateMs != nil {
		metrics.ChildCreateMs = *raw.ChildCreateMs
	}
	if raw.AttachCallMs != nil {
		metrics.ChildAttachMs = *raw.AttachCallMs
	}
	if raw.MountCallMs != nil {
		metrics.ChildMountMs = *raw.MountCallMs
	}
	if raw.ChildReadyAllocatedBytes != nil {
		metrics.ChildReadyBytes = *raw.ChildReadyAllocatedBytes
		metrics.ChildPeakBytes = *raw.ChildReadyAllocatedBytes
		metrics.ChildReadyMeasured = true
		metrics.ChildPeakMeasured = true
	}
	if err != nil {
		return nil, metrics, err
	}
	info := lease.Info()
	identity, identityErr := vhdxstorage.FileIdentity(info.ChildPath)
	if identityErr != nil {
		_, _ = lease.Release(context.Background(), true, nil)
		return nil, metrics, identityErr
	}
	return &windowsChildSession{lease: Lease{
		LeaseID: journal.LeaseID, RunID: journal.RunID, ParentKey: journal.ParentKey,
		MountPath: journal.MountPath, State: "ready", CreatedAt: journal.CreatedAt,
		PhysicalPath: info.PhysicalPath, VolumeGUID: info.VolumeGUIDPath,
	}, backend: lease, path: journal.ChildPath, identity: FileIdentity{FileID: identity}}, metrics, nil
}

func (native windowsNative) AttachChild(ctx context.Context, parent ParentMetadata, journal LeaseJournal) (ChildSession, Metrics, error) {
	identity, err := vhdxstorage.FileIdentity(journal.ChildPath)
	if err != nil {
		return nil, Metrics{}, err
	}
	actual := FileIdentity{FileID: identity}
	if actual != journal.FileIdentity {
		return nil, Metrics{}, ErrOwnershipMismatch
	}
	if _, err := vhdxstorage.PrepareDetachedStaleMount(ctx, journal.ChildPath, journal.MountPath, journal.VolumeGUID); err != nil {
		return nil, Metrics{}, err
	}
	lease, raw, err := vhdxstorage.AttachExisting(ctx, vhdxstorage.AcquireRequest{ParentPath: parent.VHDXPath, ChildPath: journal.ChildPath, MountPath: journal.MountPath, StoreRoot: filepath.Dir(filepath.Dir(journal.ChildPath)), LeaseID: journal.LeaseID}, nil)
	metrics := Metrics{ParentStatus: ParentStatusValid, ParentReused: true, ParentVirtualBytes: parent.VirtualBytes, ParentAllocatedBytes: parent.AllocatedBytes}
	if raw.AttachCallMs != nil {
		metrics.ChildAttachMs = *raw.AttachCallMs
	}
	if raw.MountCallMs != nil {
		metrics.ChildMountMs = *raw.MountCallMs
	}
	if raw.ChildReadyAllocatedBytes != nil {
		metrics.ChildReadyBytes = *raw.ChildReadyAllocatedBytes
		metrics.ChildPeakBytes = *raw.ChildReadyAllocatedBytes
		metrics.ChildReadyMeasured = true
		metrics.ChildPeakMeasured = true
	}
	if err != nil {
		return nil, metrics, err
	}
	info := lease.Info()
	return &windowsChildSession{lease: Lease{LeaseID: journal.LeaseID, RunID: journal.RunID, ParentKey: journal.ParentKey, MountPath: journal.MountPath, State: "ready", CreatedAt: journal.CreatedAt, Retained: true, PhysicalPath: info.PhysicalPath, VolumeGUID: info.VolumeGUIDPath}, backend: lease, path: journal.ChildPath, identity: actual}, metrics, nil
}

func (windowsNative) HostFreeBytes(path string) (int64, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &available, nil, nil); err != nil {
		return 0, err
	}
	return int64(available), nil
}

func ensureEmptyMount(path string) error {
	if err := os.Mkdir(path, 0700); err != nil {
		return err
	}
	return nil
}

func inspectVolume(ctx context.Context, mountPath, volumeGUID string) (VolumeIdentity, error) {
	const script = `$ErrorActionPreference='Stop'; $volume=Get-Volume -FilePath $env:TESTPLAY_VHDX_VOLUME_FILE -ErrorAction Stop; [pscustomobject]@{ clusterBytes=[int64]$volume.AllocationUnitSize } | ConvertTo-Json -Compress`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "TESTPLAY_VHDX_VOLUME_FILE="+mountPath)
	output, err := command.CombinedOutput()
	if err != nil {
		return VolumeIdentity{}, fmt.Errorf("inspect mounted parent volume: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var result struct {
		ClusterBytes int64 `json:"clusterBytes"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return VolumeIdentity{}, err
	}
	root, err := windows.UTF16PtrFromString(strings.TrimSuffix(volumeGUID, `\`) + `\`)
	if err != nil {
		return VolumeIdentity{}, err
	}
	filesystem := make([]uint16, 64)
	var serial uint32
	if err := windows.GetVolumeInformation(root, nil, 0, &serial, nil, nil, &filesystem[0], uint32(len(filesystem))); err != nil {
		return VolumeIdentity{}, err
	}
	return VolumeIdentity{VolumeGUID: volumeGUID, VolumeSerial: fmt.Sprintf("%08X", serial), Filesystem: windows.UTF16ToString(filesystem), ClusterBytes: result.ClusterBytes}, nil
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
