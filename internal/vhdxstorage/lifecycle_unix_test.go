//go:build darwin || linux

package vhdxstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixBackendAcquireIsolationAndRelease(t *testing.T) {
	paths := makeUnixLifecyclePaths(t)
	sourceFile := filepath.Join(paths.parent, "nested", "payload.bin")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceFile, []byte("immutable parent"), 0600); err != nil {
		t.Fatal(err)
	}

	backend := NewBackend()
	if !backend.Supported() || backend.RequiresElevation() {
		t.Fatalf("backend capabilities: supported=%v requiresElevation=%v", backend.Supported(), backend.RequiresElevation())
	}
	lease, metrics := acquireUnixLease(t, backend, paths)
	if metrics.ChildReadyLogicalBytes == nil || *metrics.ChildReadyLogicalBytes < int64(len("immutable parent")) {
		t.Fatalf("ready logical bytes=%v", metrics.ChildReadyLogicalBytes)
	}
	markerPath := filepath.Join(paths.child, ownershipMarkerName)
	marker, err := readOwnershipMarker(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if marker.SchemaVersion != 1 || marker.LeaseID != paths.leaseID || len(marker.OwnerToken) != ownershipTokenBytes*2 {
		t.Fatalf("marker=%#v", marker)
	}
	markerInfo, err := os.Lstat(markerPath)
	if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 || markerInfo.Mode().Perm()&0077 != 0 {
		t.Fatalf("ownership marker is not a regular file: info=%v err=%v", markerInfo, err)
	}

	linkInfo, err := os.Lstat(paths.mount)
	if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("workspace mount is not a symlink: info=%v err=%v", linkInfo, err)
	}
	childFile := filepath.Join(paths.mount, "nested", "payload.bin")
	if err := os.WriteFile(childFile, []byte("child mutation"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "immutable parent" {
		t.Fatalf("parent changed through child: %q", data)
	}
	if _, err := lease.Release(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(paths.mount); !os.IsNotExist(err) {
		t.Fatalf("mount remains after release: %v", err)
	}
	if _, err := os.Lstat(paths.child); !os.IsNotExist(err) {
		t.Fatalf("child remains after release: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(paths.child))
	if err != nil || len(entries) != 0 {
		t.Fatalf("child or quarantine residue remains: entries=%v err=%v", entries, err)
	}
}

func TestUnixBackendRestoresExistingMountDirectoryPermissionBits(t *testing.T) {
	paths := makeUnixLifecyclePaths(t)
	if err := os.MkdirAll(paths.mount, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.mount, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.parent, "payload.bin"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, _ := acquireUnixLease(t, NewBackend(), paths)
	if _, err := lease.Release(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(paths.mount)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0750 {
		t.Fatalf("mount directory permission bits were not recreated: info=%v err=%v", info, err)
	}
}

func TestUnixBackendRefusesReplacedChild(t *testing.T) {
	paths := makeUnixLifecyclePaths(t)
	if err := os.WriteFile(filepath.Join(paths.parent, "payload.bin"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, _ := acquireUnixLease(t, newTestUnixBackend(), paths)
	originalIdentity := lease.(*unixLease).ownership.identity
	if err := os.RemoveAll(paths.child); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.child, 0700); err != nil {
		t.Fatal(err)
	}
	replacementFile := filepath.Join(paths.child, "replacement.txt")
	if err := os.WriteFile(replacementFile, []byte("do not delete"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(paths.child)
	if err != nil {
		t.Fatal(err)
	}
	replacementIdentity, err := identityFromFileInfo(info)
	if err != nil {
		t.Fatal(err)
	}
	if replacementIdentity == originalIdentity {
		t.Skip("filesystem reused the same device/inode identity immediately")
	}
	_, err = lease.Release(context.Background(), true, nil)
	requireStorageCode(t, err, CodeChildOwnershipLost)
	if data, readErr := os.ReadFile(replacementFile); readErr != nil || string(data) != "do not delete" {
		t.Fatalf("replacement directory was modified or deleted: data=%q err=%v", data, readErr)
	}
}

func TestUnixBackendRefusesMissingOrTamperedOwnershipMarker(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, paths unixLifecyclePaths, lease *unixLease)
	}{
		{name: "missing", mutate: func(t *testing.T, paths unixLifecyclePaths, _ *unixLease) {
			if err := os.Remove(filepath.Join(paths.child, ownershipMarkerName)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "lease ID", mutate: func(t *testing.T, paths unixLifecyclePaths, lease *unixLease) {
			writeMarkerForTest(t, paths.child, ownershipMarker{SchemaVersion: 1, LeaseID: "another-lease", OwnerToken: lease.ownership.ownerToken})
		}},
		{name: "owner token", mutate: func(t *testing.T, paths unixLifecyclePaths, lease *unixLease) {
			writeMarkerForTest(t, paths.child, ownershipMarker{SchemaVersion: 1, LeaseID: lease.ownership.leaseID, OwnerToken: "forged-token"})
		}},
		{name: "symlink", mutate: func(t *testing.T, paths unixLifecyclePaths, lease *unixLease) {
			markerPath := filepath.Join(paths.child, ownershipMarkerName)
			if err := os.Remove(markerPath); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(paths.root, "forged-marker.json")
			data, _ := json.Marshal(ownershipMarker{SchemaVersion: 1, LeaseID: lease.ownership.leaseID, OwnerToken: lease.ownership.ownerToken})
			if err := os.WriteFile(target, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, markerPath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "directory", mutate: func(t *testing.T, paths unixLifecyclePaths, _ *unixLease) {
			markerPath := filepath.Join(paths.child, ownershipMarkerName)
			if err := os.Remove(markerPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(markerPath, 0700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := makeUnixLifecyclePaths(t)
			if err := os.WriteFile(filepath.Join(paths.parent, "payload.bin"), []byte("payload"), 0600); err != nil {
				t.Fatal(err)
			}
			genericLease, _ := acquireUnixLease(t, newTestUnixBackend(), paths)
			lease := genericLease.(*unixLease)
			test.mutate(t, paths, lease)
			_, err := lease.Release(context.Background(), true, nil)
			requireStorageCode(t, err, CodeChildOwnershipLost)
			if _, statErr := os.Lstat(paths.child); statErr != nil {
				t.Fatalf("child was deleted after marker ownership loss: %v", statErr)
			}
		})
	}
}

func TestUnixBackendRejectsParentOwnershipMarker(t *testing.T) {
	paths := makeUnixLifecyclePaths(t)
	if err := os.WriteFile(filepath.Join(paths.parent, ownershipMarkerName), []byte("reserved"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := NewBackend().Acquire(context.Background(), paths.request(), nil)
	requireStorageCode(t, err, CodeUnsafeSource)
	if _, statErr := os.Lstat(paths.child); !os.IsNotExist(statErr) {
		t.Fatalf("child was created from unsafe source: %v", statErr)
	}
	if _, statErr := os.Lstat(paths.mount); !os.IsNotExist(statErr) {
		t.Fatalf("mount was created from unsafe source: %v", statErr)
	}
}

func TestUnixBackendAcquireFailureUsesOwnedQuarantineCleanup(t *testing.T) {
	paths := makeUnixLifecyclePaths(t)
	if err := os.WriteFile(filepath.Join(paths.parent, "payload.bin"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	progressErr := errors.New("injected mount transition failure")
	_, _, err := newTestUnixBackend().Acquire(context.Background(), paths.request(), func(progress Progress) error {
		if progress.State == StateMounting {
			return progressErr
		}
		return nil
	})
	if !errors.Is(err, progressErr) {
		t.Fatalf("acquire error lost original failure: %v", err)
	}
	if _, statErr := os.Lstat(paths.child); !os.IsNotExist(statErr) {
		t.Fatalf("verified partial child remains after cleanup: %v", statErr)
	}
	entries, readErr := os.ReadDir(filepath.Dir(paths.child))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("quarantine residue remains: entries=%v err=%v", entries, readErr)
	}
}

func TestUnixBackendPreservesUnmarkedPartialCloneOnAcquireFailure(t *testing.T) {
	paths := makeUnixLifecyclePaths(t)
	cloneErr := errors.New("injected clone failure")
	backend := unixBackend{clone: func(_ context.Context, _, destination string) error {
		if err := os.Mkdir(destination, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, "partial"), []byte("preserve"), 0600); err != nil {
			return err
		}
		return cloneErr
	}}
	_, _, err := backend.Acquire(context.Background(), paths.request(), nil)
	if !errors.Is(err, cloneErr) {
		t.Fatalf("acquire error lost original clone failure: %v", err)
	}
	if !containsStorageCode(err, CodeChildOwnershipLost) {
		t.Fatalf("acquire error did not record refused unowned cleanup: %v", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(paths.child, "partial")); readErr != nil || string(data) != "preserve" {
		t.Fatalf("unmarked partial clone was deleted or modified: data=%q err=%v", data, readErr)
	}
}

func TestOwnershipMarkerCreationIsExclusive(t *testing.T) {
	child := filepath.Join(resolvedTempDir(t), "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(child, ownershipMarkerName)
	if err := os.WriteFile(markerPath, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	err := createOwnershipMarker(child, ownershipMarker{SchemaVersion: 1, LeaseID: "lease", OwnerToken: "token"})
	if err == nil {
		t.Fatal("exclusive marker creation unexpectedly replaced an existing file")
	}
	if data, readErr := os.ReadFile(markerPath); readErr != nil || string(data) != "existing" {
		t.Fatalf("existing marker was modified: data=%q err=%v", data, readErr)
	}
}

func TestUnixBackendQuarantineCollisionDoesNotOverwrite(t *testing.T) {
	paths := makeUnixLifecyclePaths(t)
	if err := os.WriteFile(filepath.Join(paths.parent, "payload.bin"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	genericLease, _ := acquireUnixLease(t, newTestUnixBackend(), paths)
	lease := genericLease.(*unixLease)
	collision := filepath.Join(filepath.Dir(paths.child), quarantineNamePrefix+"collision")
	if err := os.Mkdir(collision, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(collision, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	lease.removeHooks.quarantinePath = func(string) (string, error) { return collision, nil }
	_, err := lease.Release(context.Background(), true, nil)
	requireStorageCode(t, err, CodeCleanupFailed)
	if _, statErr := os.Lstat(paths.child); statErr != nil {
		t.Fatalf("owned child moved despite quarantine collision: %v", statErr)
	}
	if data, readErr := os.ReadFile(sentinel); readErr != nil || string(data) != "preserve" {
		t.Fatalf("collision destination was overwritten: data=%q err=%v", data, readErr)
	}
}

func TestUnixBackendDoesNotDeleteQuarantineReplacement(t *testing.T) {
	paths := makeUnixLifecyclePaths(t)
	if err := os.WriteFile(filepath.Join(paths.parent, "payload.bin"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	genericLease, _ := acquireUnixLease(t, newTestUnixBackend(), paths)
	lease := genericLease.(*unixLease)
	quarantine := filepath.Join(filepath.Dir(paths.child), quarantineNamePrefix+"post-rename-race")
	preservedOwnedChild := filepath.Join(paths.root, "preserved-owned-child")
	var hookErr error
	lease.removeHooks.quarantinePath = func(string) (string, error) { return quarantine, nil }
	lease.removeHooks.afterRename = func(_, quarantinePath string) {
		if err := os.Rename(quarantinePath, preservedOwnedChild); err != nil {
			hookErr = err
			return
		}
		if err := os.Mkdir(quarantinePath, 0700); err != nil {
			hookErr = err
			return
		}
		hookErr = os.WriteFile(filepath.Join(quarantinePath, "replacement"), []byte("preserve"), 0600)
	}
	_, err := lease.Release(context.Background(), true, nil)
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	requireStorageCode(t, err, CodeChildOwnershipLost)
	if data, readErr := os.ReadFile(filepath.Join(quarantine, "replacement")); readErr != nil || string(data) != "preserve" {
		t.Fatalf("quarantine replacement was deleted: data=%q err=%v", data, readErr)
	}
	if _, statErr := os.Lstat(preservedOwnedChild); statErr != nil {
		t.Fatalf("original owned child was unexpectedly deleted: %v", statErr)
	}
}

func TestUnixBackendRestoresOwnedChildWhenQuarantineMarkerChanges(t *testing.T) {
	paths := makeUnixLifecyclePaths(t)
	if err := os.WriteFile(filepath.Join(paths.parent, "payload.bin"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	genericLease, _ := acquireUnixLease(t, newTestUnixBackend(), paths)
	lease := genericLease.(*unixLease)
	quarantine := filepath.Join(filepath.Dir(paths.child), quarantineNamePrefix+"marker-race")
	lease.removeHooks.quarantinePath = func(string) (string, error) { return quarantine, nil }
	lease.removeHooks.afterRename = func(_, quarantinePath string) {
		writeMarkerForTest(t, quarantinePath, ownershipMarker{
			SchemaVersion: 1,
			LeaseID:       lease.ownership.leaseID,
			OwnerToken:    "forged-after-rename",
		})
	}
	_, err := lease.Release(context.Background(), true, nil)
	requireStorageCode(t, err, CodeChildOwnershipLost)
	if _, statErr := os.Lstat(paths.child); statErr != nil {
		t.Fatalf("owned child was not restored to its original path: %v", statErr)
	}
	if _, statErr := os.Lstat(quarantine); !os.IsNotExist(statErr) {
		t.Fatalf("quarantine path remains after successful restoration: %v", statErr)
	}
}

func TestUnixBackendChildSymlinkCannotEscapeRemovalRoot(t *testing.T) {
	paths := makeUnixLifecyclePaths(t)
	if err := os.WriteFile(filepath.Join(paths.parent, "payload.bin"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	genericLease, _ := acquireUnixLease(t, newTestUnixBackend(), paths)
	external := filepath.Join(paths.root, "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	externalFile := filepath.Join(external, "preserve")
	if err := os.WriteFile(externalFile, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(paths.child, "outside-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := genericLease.Release(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	if data, readErr := os.ReadFile(externalFile); readErr != nil || string(data) != "outside" {
		t.Fatalf("recursive removal escaped through child symlink: data=%q err=%v", data, readErr)
	}
}

func TestUnixBackendRefusesMountOwnershipLoss(t *testing.T) {
	root := resolvedTempDir(t)
	mount := filepath.Join(root, "workspace", "Library")
	child := filepath.Join(root, "store", "children", "worker-library")
	if err := os.MkdirAll(filepath.Dir(mount), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "unowned"), mount); err != nil {
		t.Fatal(err)
	}
	err := removeOwnedMount(mount, child)
	requireStorageCode(t, err, CodeMountOwnershipLost)
	if _, err := os.Lstat(mount); err != nil {
		t.Fatalf("unowned mount was removed: %v", err)
	}
}

type unixLifecyclePaths struct {
	root, store, parent, child, mount, leaseID string
}

func makeUnixLifecyclePaths(t *testing.T) unixLifecyclePaths {
	t.Helper()
	root := resolvedTempDir(t)
	store := filepath.Join(root, "store")
	parent := filepath.Join(store, "parents", "base-library")
	child := filepath.Join(store, "children", "worker-library")
	mount := filepath.Join(root, "workspace", "Library")
	for _, path := range []string{parent, filepath.Dir(child), filepath.Dir(mount)} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return unixLifecyclePaths{root: root, store: store, parent: parent, child: child, mount: mount, leaseID: "lease-test"}
}

func (p unixLifecyclePaths) request() AcquireRequest {
	return AcquireRequest{ParentPath: p.parent, ChildPath: p.child, MountPath: p.mount, StoreRoot: p.store, LeaseID: p.leaseID}
}

func acquireUnixLease(t *testing.T, backend Backend, paths unixLifecyclePaths) (Lease, Metrics) {
	t.Helper()
	lease, metrics, err := backend.Acquire(context.Background(), paths.request(), nil)
	if err != nil {
		var storageErr *Error
		if errors.As(err, &storageErr) && storageErr.Code == CodeCoWUnavailable {
			t.Skipf("filesystem does not support required CoW provider %s: %v", backend.Provider(), err)
		}
		t.Fatal(err)
	}
	return lease, metrics
}

func newTestUnixBackend() Backend {
	return unixBackend{clone: cloneTreeForOwnershipTest}
}

func cloneTreeForOwnershipTest(_ context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := destination
		if relative != "." {
			target = filepath.Join(destination, relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Mkdir(target, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func writeMarkerForTest(t *testing.T, childPath string, marker ownershipMarker) {
	t.Helper()
	path := filepath.Join(childPath, ownershipMarkerName)
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func requireStorageCode(t *testing.T, err error, code string) {
	t.Helper()
	var storageErr *Error
	if !errors.As(err, &storageErr) || storageErr.Code != code {
		t.Fatalf("error=%v; want storage code %q", err, code)
	}
}

func containsStorageCode(err error, code string) bool {
	if err == nil {
		return false
	}
	if storageErr, ok := err.(*Error); ok && storageErr.Code == code {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if containsStorageCode(child, code) {
				return true
			}
		}
		return false
	}
	return containsStorageCode(errors.Unwrap(err), code)
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(fmt.Errorf("resolve temporary directory: %w", err))
	}
	return root
}
