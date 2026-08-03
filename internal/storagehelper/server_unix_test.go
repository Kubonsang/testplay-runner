//go:build darwin || linux

package storagehelper

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

func TestUnixStorageHelperUsesSameAcquireReleaseProtocol(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, "store")
	workspace := filepath.Join(root, "workspace")
	parent := filepath.Join(store, "parents", "base-library")
	child := filepath.Join(store, "children", "worker-library")
	mount := filepath.Join(workspace, "Library")
	for _, path := range []string{parent, filepath.Dir(child), workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(parent, "payload.bin"), []byte("parent"), 0600); err != nil {
		t.Fatal(err)
	}

	server := NewServer(vhdxstorage.NewBackend())
	server.leaseID = func() (string, error) { return "lease-unix", nil }
	responses, stderr, serveErr := runServer(t, server, encodeLines(t,
		Request{SchemaVersion: SchemaVersion, Operation: OperationHello, RequestID: "req-hello"},
		Request{
			SchemaVersion: SchemaVersion, Operation: OperationAcquire, RequestID: "req-acquire",
			StoreRoot: store, WorkspaceRoot: workspace, ParentPath: parent,
			ChildPath: child, MountPath: mount, DeleteChildOnRelease: true,
		},
		Request{SchemaVersion: SchemaVersion, Operation: OperationRelease, RequestID: "req-release", LeaseID: "lease-unix"},
	))
	if serveErr != nil {
		t.Fatal(serveErr)
	}
	if len(responses) >= 2 && responses[1].Error != nil && responses[1].Error.Code == vhdxstorage.CodeCoWUnavailable {
		t.Skipf("filesystem does not support required CoW provider: %s", responses[1].Error)
	}
	if stderr != "" || len(responses) != 3 {
		t.Fatalf("responses=%#v stderr=%q", responses, stderr)
	}
	hello := responses[0]
	if !hello.OK || hello.HelperVersion != HelperVersion || hello.Platform != runtime.GOOS || hello.Provider != expectedUnixProvider() || hello.Elevated == nil || hello.RequiresElevation == nil || *hello.RequiresElevation {
		t.Fatalf("hello=%#v", hello)
	}
	acquired := responses[1]
	if !acquired.OK || acquired.Lease == nil || acquired.Lease.Provider != expectedUnixProvider() || acquired.Lease.LeaseID != "lease-unix" {
		t.Fatalf("acquire=%#v", acquired)
	}
	if !responses[2].OK || !responses[2].Released {
		t.Fatalf("release=%#v", responses[2])
	}
	if _, err := os.Lstat(child); !os.IsNotExist(err) {
		t.Fatalf("child remains after protocol release: %v", err)
	}
	if _, err := os.Lstat(mount); !os.IsNotExist(err) {
		t.Fatalf("mount remains after protocol release: %v", err)
	}
}

func expectedUnixProvider() string {
	if runtime.GOOS == "darwin" {
		return vhdxstorage.ProviderAPFS
	}
	return vhdxstorage.ProviderReflink
}
