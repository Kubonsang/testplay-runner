//go:build darwin || linux

package storagehelper

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnixAcquirePathsAcceptDirectoryParentAndChild(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, "store")
	workspace := filepath.Join(root, "workspace")
	parent := filepath.Join(store, "parents", "base-library")
	child := filepath.Join(store, "children", "worker-library")
	for _, path := range []string{parent, filepath.Dir(child), workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	request := Request{
		StoreRoot:     store,
		WorkspaceRoot: workspace,
		ParentPath:    parent,
		ChildPath:     child,
		MountPath:     filepath.Join(workspace, "Library"),
	}
	paths, err := validateAcquirePaths(request, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if paths.ParentPath != parent || paths.ChildPath != child {
		t.Fatalf("paths=%+v", paths)
	}
}

func TestUnixAcquirePathsRejectSymlinkedParent(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, "store")
	workspace := filepath.Join(root, "workspace")
	realParent := filepath.Join(store, "parents", "real-library")
	parent := filepath.Join(store, "parents", "linked-library")
	child := filepath.Join(store, "children", "worker-library")
	for _, path := range []string{realParent, filepath.Dir(child), workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(realParent, parent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = validateAcquirePaths(Request{
		StoreRoot: store, WorkspaceRoot: workspace, ParentPath: parent,
		ChildPath: child, MountPath: filepath.Join(workspace, "Library"),
	}, runtime.GOOS)
	if errorCode(err) != CodeParentInvalid {
		t.Fatalf("error=%v", err)
	}
}
