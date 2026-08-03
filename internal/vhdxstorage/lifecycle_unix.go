//go:build darwin || linux

package vhdxstorage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

var errCoWUnavailable = errors.New("copy-on-write cloning is unavailable")

type unixBackend struct{}

func NewBackend() Backend                   { return unixBackend{} }
func (unixBackend) Platform() string        { return runtime.GOOS }
func (unixBackend) Provider() string        { return platformProvider }
func (unixBackend) Supported() bool         { return true }
func (unixBackend) RequiresElevation() bool { return false }
func (unixBackend) IsElevated(context.Context) (bool, error) {
	return os.Geteuid() == 0, nil
}

func (unixBackend) Acquire(
	ctx context.Context,
	request AcquireRequest,
	progress ProgressFunc,
) (Lease, Metrics, error) {
	started := time.Now()
	metrics := Metrics{}
	if err := ctx.Err(); err != nil {
		return nil, metrics, newError(CodeCancelled, "acquire", request.ChildPath, err)
	}
	if err := validateCloneSource(request.ParentPath); err != nil {
		return nil, metrics, err
	}
	if _, err := os.Lstat(request.ChildPath); err == nil {
		return nil, metrics, newError(CodeChildExists, "stat-child", request.ChildPath, nil)
	} else if !os.IsNotExist(err) {
		return nil, metrics, newError(CodeChildCreateFailed, "stat-child", request.ChildPath, err)
	}

	mountExisted := false
	mountMode := fs.FileMode(0700)
	if info, err := os.Lstat(request.MountPath); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, metrics, newError(CodeMountFailed, "validate-mount", request.MountPath, fmt.Errorf("mount must be a real directory"))
		}
		entries, readErr := os.ReadDir(request.MountPath)
		if readErr != nil {
			return nil, metrics, newError(CodeMountFailed, "read-mount", request.MountPath, readErr)
		}
		if len(entries) != 0 {
			return nil, metrics, newError(CodeMountFailed, "validate-mount", request.MountPath, fmt.Errorf("mount must be empty"))
		}
		mountExisted = true
		mountMode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return nil, metrics, newError(CodeMountFailed, "stat-mount", request.MountPath, err)
	}

	childCreated := false
	mountLinked := false
	fail := func(primary error) (Lease, Metrics, error) {
		var cleanupErr error
		if mountLinked {
			cleanupErr = removeOwnedMount(request.MountPath, request.ChildPath)
		}
		if mountExisted {
			if err := os.Mkdir(request.MountPath, mountMode); err != nil && !os.IsExist(err) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
		if childCreated {
			cleanupErr = errors.Join(cleanupErr, removeOwnedChild(request.ChildPath))
		}
		metrics.TotalWallClockMs = milliseconds(time.Since(started).Milliseconds())
		metrics.AcquireWallClockMs = metrics.TotalWallClockMs
		return nil, metrics, errors.Join(primary, cleanupErr)
	}

	if err := notify(progress, Progress{State: StateCreatingChild}); err != nil {
		return fail(err)
	}
	phase := time.Now()
	if err := cloneTree(ctx, request.ParentPath, request.ChildPath); err != nil {
		if _, statErr := os.Lstat(request.ChildPath); statErr == nil {
			childCreated = true
		}
		code := CodeChildCreateFailed
		if errors.Is(err, errCoWUnavailable) {
			code = CodeCoWUnavailable
		} else if errors.Is(err, context.Canceled) {
			code = CodeCancelled
		}
		return fail(newError(code, "clone-child", request.ChildPath, err))
	}
	childCreated = true
	metrics.ChildCreateMs = milliseconds(time.Since(phase).Milliseconds())
	if err := validateCloneSource(request.ChildPath); err != nil {
		return fail(newError(CodeUnsafeSource, "validate-cloned-child", request.ChildPath, err))
	}
	usage, err := shadow.MeasureDirectoryUsage(request.ChildPath)
	if err != nil {
		return fail(newError(CodeChildCreateFailed, "measure-child", request.ChildPath, err))
	}
	metrics.ChildReadyLogicalBytes = milliseconds(usage.LogicalBytes)
	metrics.ChildReadyAllocatedBytes = milliseconds(usage.AllocatedBytes)

	if err := notify(progress, Progress{State: StateMounting}); err != nil {
		return fail(err)
	}
	phase = time.Now()
	if mountExisted {
		if err := os.Remove(request.MountPath); err != nil {
			return fail(newError(CodeMountFailed, "remove-empty-mount", request.MountPath, err))
		}
	}
	if err := os.Symlink(request.ChildPath, request.MountPath); err != nil {
		return fail(newError(CodeMountFailed, "link-workspace", request.MountPath, err))
	}
	mountLinked = true
	metrics.MountCallMs = milliseconds(time.Since(phase).Milliseconds())
	metrics.WorkspaceReadyMs = milliseconds(time.Since(started).Milliseconds())
	metrics.AcquireWallClockMs = metrics.WorkspaceReadyMs
	metrics.TotalWallClockMs = metrics.WorkspaceReadyMs
	if err := notify(progress, Progress{State: StateReady}); err != nil {
		return fail(err)
	}
	return &unixLease{
		info: LeaseInfo{
			ParentPath: request.ParentPath,
			ChildPath:  request.ChildPath,
			MountPath:  request.MountPath,
		},
		mountExisted: mountExisted,
		mountMode:    mountMode,
	}, metrics, nil
}

type unixLease struct {
	mu           sync.Mutex
	info         LeaseInfo
	mountExisted bool
	mountMode    fs.FileMode
	released     bool
	metrics      Metrics
}

func (l *unixLease) Info() LeaseInfo { return l.info }

func (l *unixLease) Release(
	ctx context.Context,
	deleteChild bool,
	progress ProgressFunc,
) (Metrics, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return l.metrics, nil
	}
	started := time.Now()
	metrics := Metrics{}
	if err := ctx.Err(); err != nil {
		return metrics, newError(CodeCancelled, "release", l.info.ChildPath, err)
	}
	if err := notify(progress, Progress{State: StateUnmounting}); err != nil {
		return metrics, err
	}
	phase := time.Now()
	if err := removeOwnedMount(l.info.MountPath, l.info.ChildPath); err != nil {
		return metrics, err
	}
	if l.mountExisted {
		if err := os.Mkdir(l.info.MountPath, l.mountMode); err != nil {
			return metrics, newError(CodeCleanupFailed, "restore-mount-directory", l.info.MountPath, err)
		}
	}
	metrics.UnmountCallMs = milliseconds(time.Since(phase).Milliseconds())

	usage, err := shadow.MeasureDirectoryUsage(l.info.ChildPath)
	if err != nil {
		return metrics, newError(CodeCleanupFailed, "measure-child", l.info.ChildPath, err)
	}
	metrics.ChildReleasedLogicalBytes = milliseconds(usage.LogicalBytes)
	metrics.ChildReleasedAllocatedBytes = milliseconds(usage.AllocatedBytes)
	cleanup := time.Now()
	if deleteChild {
		if err := removeOwnedChild(l.info.ChildPath); err != nil {
			return metrics, err
		}
	}
	metrics.CleanupMs = milliseconds(time.Since(cleanup).Milliseconds())
	metrics.ReleaseWallClockMs = milliseconds(time.Since(started).Milliseconds())
	metrics.TotalWallClockMs = metrics.ReleaseWallClockMs
	if err := notify(progress, Progress{State: StateReleased}); err != nil {
		return metrics, err
	}
	l.released = true
	l.metrics = metrics
	return metrics, nil
}

func validateCloneSource(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return newError(CodeUnsafeSource, "walk-source", path, walkErr)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return newError(CodeUnsafeSource, "validate-source", path, fmt.Errorf("symbolic links are not allowed"))
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return newError(CodeUnsafeSource, "stat-source", path, err)
		}
		if !info.Mode().IsRegular() {
			return newError(CodeUnsafeSource, "validate-source", path, fmt.Errorf("special files are not allowed: %s", info.Mode()))
		}
		return nil
	})
}

func removeOwnedMount(mountPath, childPath string) error {
	info, err := os.Lstat(mountPath)
	if os.IsNotExist(err) {
		return newError(CodeMountOwnershipLost, "release-mount", mountPath, err)
	}
	if err != nil {
		return newError(CodeUnmountFailed, "stat-mount", mountPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return newError(CodeMountOwnershipLost, "validate-mount", mountPath, fmt.Errorf("mount is no longer the helper-owned symbolic link"))
	}
	target, err := os.Readlink(mountPath)
	if err != nil {
		return newError(CodeUnmountFailed, "read-mount", mountPath, err)
	}
	if filepath.Clean(target) != filepath.Clean(childPath) {
		return newError(CodeMountOwnershipLost, "validate-mount", mountPath, fmt.Errorf("target=%s want=%s", target, childPath))
	}
	if err := os.Remove(mountPath); err != nil {
		return newError(CodeUnmountFailed, "remove-mount", mountPath, err)
	}
	return nil
}

func removeOwnedChild(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return newError(CodeCleanupFailed, "stat-child", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return newError(CodeCleanupFailed, "validate-child", path, fmt.Errorf("child is no longer the helper-owned real directory"))
	}
	if err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if err := os.Chmod(current, 0700); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return newError(CodeCleanupFailed, "prepare-child-removal", path, err)
	}
	if err := os.RemoveAll(path); err != nil {
		return newError(CodeCleanupFailed, "remove-child", path, err)
	}
	return nil
}
