//go:build darwin || linux

package vhdxstorage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

const (
	ownershipMarkerName          = ".testplay-storage-owner"
	ownershipMarkerSchemaVersion = 1
	ownershipTokenBytes          = 32
	maximumOwnershipMarkerBytes  = 4096
	quarantineNamePrefix         = ".testplay-delete-"
)

var errCoWUnavailable = errors.New("copy-on-write cloning is unavailable")

type unixBackend struct {
	clone func(context.Context, string, string) error
}

func NewBackend() Backend                   { return unixBackend{clone: cloneTree} }
func (unixBackend) Platform() string        { return runtime.GOOS }
func (unixBackend) Provider() string        { return platformProvider }
func (unixBackend) Supported() bool         { return true }
func (unixBackend) RequiresElevation() bool { return false }
func (unixBackend) IsElevated(context.Context) (bool, error) {
	return os.Geteuid() == 0, nil
}

type fileIdentity struct {
	device uint64
	inode  uint64
}

type ownershipMarker struct {
	SchemaVersion int    `json:"schemaVersion"`
	LeaseID       string `json:"leaseId"`
	OwnerToken    string `json:"ownerToken"`
}

type childOwnership struct {
	storeRoot  string
	childPath  string
	leaseID    string
	ownerToken string
	identity   fileIdentity
}

type childRemovalHooks struct {
	quarantinePath func(childPath string) (string, error)
	afterRename    func(originalPath, quarantinePath string)
}

func (backend unixBackend) Acquire(
	ctx context.Context,
	request AcquireRequest,
	progress ProgressFunc,
) (Lease, Metrics, error) {
	started := time.Now()
	metrics := Metrics{}
	if err := ctx.Err(); err != nil {
		return nil, metrics, newError(CodeCancelled, "acquire", request.ChildPath, err)
	}
	if err := validateOwnershipInputs(request); err != nil {
		return nil, metrics, err
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

	var ownership *childOwnership
	unownedChildObserved := false
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
		// A partial clone without a verified marker is deliberately preserved: a
		// path-only RemoveAll could delete an object substituted by another actor.
		if ownership != nil {
			cleanupErr = errors.Join(cleanupErr, removeOwnedChild(*ownership, childRemovalHooks{}))
		} else if unownedChildObserved {
			cleanupErr = errors.Join(cleanupErr, ownershipLost(
				"preserve-unowned-partial-child",
				request.ChildPath,
				"partial child lacks a verified ownership marker and was not deleted",
				nil,
			))
		}
		metrics.TotalWallClockMs = milliseconds(time.Since(started).Milliseconds())
		metrics.AcquireWallClockMs = metrics.TotalWallClockMs
		return nil, metrics, errors.Join(primary, cleanupErr)
	}

	if err := notify(progress, Progress{State: StateCreatingChild}); err != nil {
		return fail(err)
	}
	phase := time.Now()
	clone := backend.clone
	if clone == nil {
		clone = cloneTree
	}
	if err := clone(ctx, request.ParentPath, request.ChildPath); err != nil {
		if _, statErr := os.Lstat(request.ChildPath); statErr == nil {
			unownedChildObserved = true
		}
		code := CodeChildCreateFailed
		if errors.Is(err, errCoWUnavailable) {
			code = CodeCoWUnavailable
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = CodeCancelled
		}
		return fail(newError(code, "clone-child", request.ChildPath, err))
	}
	unownedChildObserved = true
	metrics.ChildCreateMs = milliseconds(time.Since(phase).Milliseconds())
	if err := validateCloneSource(request.ChildPath); err != nil {
		return fail(newError(CodeUnsafeSource, "validate-cloned-child", request.ChildPath, err))
	}
	var err error
	ownership, err = establishChildOwnership(request)
	if err != nil {
		return fail(err)
	}
	unownedChildObserved = false
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
		ownership:    *ownership,
		mountExisted: mountExisted,
		mountMode:    mountMode,
	}, metrics, nil
}

type unixLease struct {
	mu           sync.Mutex
	info         LeaseInfo
	ownership    childOwnership
	mountExisted bool
	mountMode    fs.FileMode
	released     bool
	metrics      Metrics
	removeHooks  childRemovalHooks
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

	if deleteChild {
		if err := validateOwnedChildAt(l.ownership, l.info.ChildPath); err != nil {
			return metrics, err
		}
	}
	usage, err := shadow.MeasureDirectoryUsage(l.info.ChildPath)
	if err != nil {
		return metrics, newError(CodeCleanupFailed, "measure-child", l.info.ChildPath, err)
	}
	metrics.ChildReleasedLogicalBytes = milliseconds(usage.LogicalBytes)
	metrics.ChildReleasedAllocatedBytes = milliseconds(usage.AllocatedBytes)
	cleanup := time.Now()
	if deleteChild {
		if err := removeOwnedChild(l.ownership, l.removeHooks); err != nil {
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

func validateOwnershipInputs(request AcquireRequest) error {
	if request.StoreRoot == "" {
		return newError(CodeChildCreateFailed, "validate-child-ownership", request.ChildPath, fmt.Errorf("store root is required"))
	}
	if request.LeaseID == "" {
		return newError(CodeChildCreateFailed, "validate-child-ownership", request.ChildPath, fmt.Errorf("lease ID is required"))
	}
	storeInfo, err := os.Lstat(request.StoreRoot)
	if err != nil {
		return newError(CodeChildCreateFailed, "stat-store-root", request.StoreRoot, err)
	}
	if !storeInfo.IsDir() || storeInfo.Mode()&os.ModeSymlink != 0 {
		return newError(CodeChildCreateFailed, "validate-store-root", request.StoreRoot, fmt.Errorf("store root must be a real directory"))
	}
	if !pathWithinRoot(request.StoreRoot, request.ChildPath) {
		return ownershipLost("validate-child-boundary", request.ChildPath, "child path is not below store root", nil)
	}
	return nil
}

func validateCloneSource(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return newError(CodeUnsafeSource, "walk-source", path, walkErr)
		}
		if path != root && filepath.Base(path) == ownershipMarkerName {
			return newError(CodeUnsafeSource, "validate-source", path, fmt.Errorf("reserved ownership marker is not allowed in a clone source"))
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

func establishChildOwnership(request AcquireRequest) (*childOwnership, error) {
	info, err := os.Lstat(request.ChildPath)
	if err != nil {
		return nil, ownershipLost("stat-created-child", request.ChildPath, "created child cannot be inspected", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ownershipLost("validate-created-child", request.ChildPath, "created child is not a real directory", nil)
	}
	identity, err := identityFromFileInfo(info)
	if err != nil {
		return nil, ownershipLost("identify-created-child", request.ChildPath, "device/inode identity is unavailable", err)
	}
	token, err := randomToken()
	if err != nil {
		return nil, newError(CodeChildCreateFailed, "create-owner-token", request.ChildPath, err)
	}
	marker := ownershipMarker{SchemaVersion: ownershipMarkerSchemaVersion, LeaseID: request.LeaseID, OwnerToken: token}
	if err := createOwnershipMarker(request.ChildPath, marker); err != nil {
		return nil, newError(CodeChildCreateFailed, "create-ownership-marker", filepath.Join(request.ChildPath, ownershipMarkerName), err)
	}
	ownership := &childOwnership{
		storeRoot:  request.StoreRoot,
		childPath:  request.ChildPath,
		leaseID:    request.LeaseID,
		ownerToken: token,
		identity:   identity,
	}
	if err := validateOwnedChildAt(*ownership, request.ChildPath); err != nil {
		return nil, err
	}
	return ownership, nil
}

func createOwnershipMarker(childPath string, marker ownershipMarker) error {
	markerPath := filepath.Join(childPath, ownershipMarkerName)
	file, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	data, marshalErr := json.Marshal(marker)
	if marshalErr == nil {
		data = append(data, '\n')
		_, marshalErr = file.Write(data)
	}
	if marshalErr == nil {
		marshalErr = file.Sync()
	}
	closeErr := file.Close()
	return errors.Join(marshalErr, closeErr)
}

func validateOwnedChildAt(ownership childOwnership, path string) error {
	if !pathWithinRoot(ownership.storeRoot, path) {
		return ownershipLost("validate-child-boundary", path, "child path is not below store root", nil)
	}
	if err := validateChildIdentity(ownership, path); err != nil {
		return err
	}
	marker, err := readOwnershipMarker(filepath.Join(path, ownershipMarkerName))
	if err != nil {
		return ownershipLost("validate-ownership-marker", path, "ownership marker is missing, unsafe, or unreadable", err)
	}
	if marker.SchemaVersion != ownershipMarkerSchemaVersion {
		return ownershipLost("validate-ownership-marker", path, "ownership marker schema does not match", fmt.Errorf("got=%d want=%d", marker.SchemaVersion, ownershipMarkerSchemaVersion))
	}
	if marker.LeaseID != ownership.leaseID {
		return ownershipLost("validate-ownership-marker", path, "ownership marker lease ID does not match", fmt.Errorf("got=%q want=%q", marker.LeaseID, ownership.leaseID))
	}
	if marker.OwnerToken != ownership.ownerToken {
		return ownershipLost("validate-ownership-marker", path, "ownership marker token does not match", nil)
	}
	if err := validateChildTraversal(path); err != nil {
		return ownershipLost("validate-child-traversal", path, "child traversal escaped its root or failed", err)
	}
	return validateChildIdentity(ownership, path)
}

func validateChildIdentity(ownership childOwnership, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return ownershipLost("identify-child", path, "child path is missing or cannot be inspected", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ownershipLost("identify-child", path, "child is not the acquired real directory", nil)
	}
	identity, err := identityFromFileInfo(info)
	if err != nil {
		return ownershipLost("identify-child", path, "device/inode identity is unavailable", err)
	}
	if identity != ownership.identity {
		return ownershipLost("identify-child", path, "child device/inode identity does not match the acquired object", fmt.Errorf("got=%d:%d want=%d:%d", identity.device, identity.inode, ownership.identity.device, ownership.identity.inode))
	}
	return nil
}

func identityFromFileInfo(info fs.FileInfo) (fileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return fileIdentity{}, fmt.Errorf("unexpected stat type %T", info.Sys())
	}
	return fileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino)}, nil
}

func readOwnershipMarker(path string) (ownershipMarker, error) {
	var marker ownershipMarker
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return marker, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return marker, fmt.Errorf("marker must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return marker, err
	}
	defer file.Close()
	openInfo, err := file.Stat()
	if err != nil {
		return marker, err
	}
	currentInfo, err := os.Lstat(path)
	if err != nil {
		return marker, err
	}
	if currentInfo.Mode()&os.ModeSymlink != 0 || !currentInfo.Mode().IsRegular() || !os.SameFile(openInfo, currentInfo) {
		return marker, fmt.Errorf("marker path changed while being opened")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maximumOwnershipMarkerBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return marker, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return marker, fmt.Errorf("marker contains trailing JSON data")
		}
		return marker, err
	}
	return marker, nil
}

func validateChildTraversal(root string) error {
	return filepath.WalkDir(root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !pathWithinOrEqual(root, path) {
			return fmt.Errorf("path %q is outside root %q", path, root)
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

func removeOwnedChild(ownership childOwnership, hooks childRemovalHooks) error {
	if err := validateOwnedChildAt(ownership, ownership.childPath); err != nil {
		return err
	}
	quarantinePath, err := chooseQuarantinePath(ownership, hooks)
	if err != nil {
		return err
	}
	if err := renameNoReplace(ownership.childPath, quarantinePath); err != nil {
		return newError(CodeCleanupFailed, "quarantine-child", ownership.childPath, fmt.Errorf("destination=%s: %w", quarantinePath, err))
	}
	if hooks.afterRename != nil {
		hooks.afterRename(ownership.childPath, quarantinePath)
	}
	if err := validateOwnedChildAt(ownership, quarantinePath); err != nil {
		revalidationErr := ownershipLost(
			"revalidate-quarantined-child",
			ownership.childPath,
			"quarantine ownership verification failed",
			fmt.Errorf("quarantine=%s: %w", quarantinePath, err),
		)
		if identityErr := validateChildIdentity(ownership, quarantinePath); identityErr == nil {
			if restoreErr := renameNoReplace(quarantinePath, ownership.childPath); restoreErr != nil {
				return errors.Join(revalidationErr, newError(CodeCleanupFailed, "restore-quarantined-child", ownership.childPath, fmt.Errorf("quarantine=%s: %w", quarantinePath, restoreErr)))
			}
		}
		return revalidationErr
	}
	if err := prepareChildRemoval(quarantinePath); err != nil {
		return newError(CodeCleanupFailed, "prepare-child-removal", quarantinePath, err)
	}
	if err := os.RemoveAll(quarantinePath); err != nil {
		return newError(CodeCleanupFailed, "remove-quarantined-child", quarantinePath, err)
	}
	return nil
}

func chooseQuarantinePath(ownership childOwnership, hooks childRemovalHooks) (string, error) {
	var path string
	var err error
	if hooks.quarantinePath != nil {
		path, err = hooks.quarantinePath(ownership.childPath)
	} else {
		var token string
		token, err = randomToken()
		if err == nil {
			path = filepath.Join(filepath.Dir(ownership.childPath), quarantineNamePrefix+token)
		}
	}
	if err != nil {
		return "", newError(CodeCleanupFailed, "create-quarantine-name", ownership.childPath, err)
	}
	if filepath.Dir(filepath.Clean(path)) != filepath.Dir(filepath.Clean(ownership.childPath)) || !strings.HasPrefix(filepath.Base(path), quarantineNamePrefix) || !pathWithinRoot(ownership.storeRoot, path) {
		return "", newError(CodeCleanupFailed, "validate-quarantine-path", path, fmt.Errorf("quarantine must use the reserved name prefix beside the child and below store root"))
	}
	if _, err := os.Lstat(path); err == nil {
		return "", newError(CodeCleanupFailed, "reserve-quarantine-path", path, fmt.Errorf("quarantine destination already exists"))
	} else if !os.IsNotExist(err) {
		return "", newError(CodeCleanupFailed, "reserve-quarantine-path", path, err)
	}
	return path, nil
}

func prepareChildRemoval(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !pathWithinOrEqual(root, path) {
			return fmt.Errorf("path %q is outside root %q", path, root)
		}
		if entry.IsDir() {
			return os.Chmod(path, 0700)
		}
		return nil
	})
}

func ownershipLost(operation, path, condition string, cause error) error {
	if cause == nil {
		cause = errors.New(condition)
	} else {
		cause = fmt.Errorf("%s: %w", condition, cause)
	}
	return newError(CodeChildOwnershipLost, operation, path, cause)
}

func pathWithinRoot(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func pathWithinOrEqual(root, path string) bool {
	if filepath.Clean(root) == filepath.Clean(path) {
		return true
	}
	return pathWithinRoot(root, path)
}

func randomToken() (string, error) {
	value := make([]byte, ownershipTokenBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
