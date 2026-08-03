//go:build darwin || linux

package vhdxstorage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixBackendAcquireIsolationAndRelease(t *testing.T) {
	root := resolvedTempDir(t)
	parent := filepath.Join(root, "store", "parents", "base-library")
	child := filepath.Join(root, "store", "children", "worker-library")
	workspace := filepath.Join(root, "workspace")
	mount := filepath.Join(workspace, "Library")
	for _, path := range []string{filepath.Join(parent, "nested"), filepath.Dir(child), workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	sourceFile := filepath.Join(parent, "nested", "payload.bin")
	if err := os.WriteFile(sourceFile, []byte("immutable parent"), 0600); err != nil {
		t.Fatal(err)
	}

	backend := NewBackend()
	if !backend.Supported() || backend.RequiresElevation() {
		t.Fatalf("backend capabilities: supported=%v requiresElevation=%v", backend.Supported(), backend.RequiresElevation())
	}
	lease, metrics, err := backend.Acquire(context.Background(), AcquireRequest{
		ParentPath: parent,
		ChildPath:  child,
		MountPath:  mount,
	}, nil)
	if err != nil {
		var storageErr *Error
		if errors.As(err, &storageErr) && storageErr.Code == CodeCoWUnavailable {
			t.Skipf("filesystem does not support required CoW provider %s: %v", backend.Provider(), err)
		}
		t.Fatal(err)
	}
	if metrics.ChildReadyLogicalBytes == nil || *metrics.ChildReadyLogicalBytes != int64(len("immutable parent")) {
		t.Fatalf("ready logical bytes=%v", metrics.ChildReadyLogicalBytes)
	}
	linkInfo, err := os.Lstat(mount)
	if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("workspace mount is not a symlink: info=%v err=%v", linkInfo, err)
	}
	childFile := filepath.Join(mount, "nested", "payload.bin")
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
	if _, err := os.Lstat(mount); !os.IsNotExist(err) {
		t.Fatalf("mount remains after release: %v", err)
	}
	if _, err := os.Lstat(child); !os.IsNotExist(err) {
		t.Fatalf("child remains after release: %v", err)
	}
}

func TestUnixBackendRestoresExistingMountDirectory(t *testing.T) {
	root := resolvedTempDir(t)
	parent := filepath.Join(root, "store", "parents", "base-library")
	child := filepath.Join(root, "store", "children", "worker-library")
	mount := filepath.Join(root, "workspace", "Library")
	for _, path := range []string{parent, filepath.Dir(child), mount} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(mount, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "payload.bin"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, _, err := NewBackend().Acquire(context.Background(), AcquireRequest{ParentPath: parent, ChildPath: child, MountPath: mount}, nil)
	if err != nil {
		var storageErr *Error
		if errors.As(err, &storageErr) && storageErr.Code == CodeCoWUnavailable {
			t.Skipf("filesystem does not support required CoW provider: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := lease.Release(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(mount)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0750 {
		t.Fatalf("mount directory was not restored: info=%v err=%v", info, err)
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
	var storageErr *Error
	if !errors.As(err, &storageErr) || storageErr.Code != CodeMountOwnershipLost {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Lstat(mount); err != nil {
		t.Fatalf("unowned mount was removed: %v", err)
	}
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
