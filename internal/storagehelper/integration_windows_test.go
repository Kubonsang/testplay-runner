//go:build windows && vhdx_helper_integration

package storagehelper_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/storagehelper"
	"github.com/Kubonsang/testplay-runner/internal/vhdxprobe"
	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

const helperProcessEnv = "TESTPLAY_VHDX_HELPER_PROCESS"

func TestVHDXStorageHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		t.Skip("helper subprocess only")
	}
	server := storagehelper.NewServer(vhdxstorage.NewBackend())
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(20)
	}
	os.Exit(0)
}

type helperClient struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  strings.Builder
}

func startHelper(t *testing.T) *helperClient {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestVHDXStorageHelperProcess$")
	command.Env = append(os.Environ(), helperProcessEnv+"=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	client := &helperClient{command: command, stdin: stdin, stdout: bufio.NewReader(stdout)}
	command.Stderr = &client.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	return client
}
func (c *helperClient) call(t *testing.T, request storagehelper.Request) storagehelper.Response {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := c.stdout.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v stderr=%s", err, c.stderr.String())
	}
	var response storagehelper.Response
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return response
}
func (c *helperClient) wait(t *testing.T) {
	t.Helper()
	if err := c.command.Wait(); err != nil {
		t.Fatalf("helper exit: %v stderr=%s", err, c.stderr.String())
	}
}

type integrationPaths struct{ root, store, workspace, parent, child, mount string }

func prepareIntegrationPaths(t *testing.T) integrationPaths {
	t.Helper()
	configured := os.Getenv("TESTPLAY_VHDX_HELPER_ROOT")
	if configured == "" {
		t.Skip("TESTPLAY_VHDX_HELPER_ROOT is not set")
	}
	if !filepath.IsAbs(configured) {
		t.Fatal("helper root must be absolute")
	}
	root := filepath.Clean(configured)
	if root == filepath.VolumeName(root)+string(os.PathSeparator) {
		t.Fatal("drive root is forbidden")
	}
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatal("helper root must be a real directory")
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("helper root must be empty; found %d item(s)", len(entries))
		}
	} else if os.IsNotExist(err) {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if entries, err := os.ReadDir(root); err == nil && len(entries) == 0 {
			_ = os.Remove(root)
		}
	})
	caseRoot := filepath.Join(root, "case")
	store := filepath.Join(caseRoot, "store")
	workspace := filepath.Join(caseRoot, "workspace")
	for _, path := range []string{filepath.Join(store, "parents"), filepath.Join(store, "children"), workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return integrationPaths{root, store, workspace, filepath.Join(store, "parents", "base.vhdx"), filepath.Join(store, "children", "child.vhdx"), filepath.Join(workspace, "Library")}
}

func requireElevated(t *testing.T) {
	t.Helper()
	elevated, err := vhdxstorage.IsElevated(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !elevated {
		t.Fatal("elevated PowerShell is required")
	}
}
func fileBackedDisks(t *testing.T) string {
	t.Helper()
	script := `@(Get-Disk | Where-Object { $_.BusType.ToString() -eq 'File Backed Virtual' } | Sort-Object Number | Select-Object Number,SerialNumber) | ConvertTo-Json -Compress`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		t.Fatalf("snapshot disks: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
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
func readJournal(t *testing.T, store, leaseID string) storagehelper.Journal {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(store, "leases", leaseID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal storagehelper.Journal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	return journal
}
func cleanupSuccessfulCase(t *testing.T, paths integrationPaths) {
	t.Helper()
	caseRoot := filepath.Dir(paths.store)
	if filepath.Dir(caseRoot) != paths.root || filepath.Base(caseRoot) != "case" {
		t.Fatalf("unsafe integration cleanup target: %s", caseRoot)
	}
	if err := os.RemoveAll(caseRoot); err != nil {
		t.Fatal(err)
	}
}

func TestVHDXStorageHelperAcquireRelease(t *testing.T) {
	paths := prepareIntegrationPaths(t)
	requireElevated(t)
	before := fileBackedDisks(t)
	fixture, err := vhdxprobe.PrepareHelperParentFixture(context.Background(), paths.parent, filepath.Join(paths.root, "parent-mount"))
	if err != nil {
		t.Fatal(err)
	}
	client := startHelper(t)
	hello := client.call(t, storagehelper.Request{SchemaVersion: 1, Operation: "hello", RequestID: "req-hello"})
	if !hello.OK || hello.Elevated == nil || !*hello.Elevated {
		t.Fatalf("hello=%#v", hello)
	}
	request := storagehelper.Request{SchemaVersion: 1, Operation: "acquire", RequestID: "req-acquire", StoreRoot: paths.store, WorkspaceRoot: paths.workspace, ParentPath: paths.parent, ChildPath: paths.child, MountPath: paths.mount, DeleteChildOnRelease: true}
	acquired := client.call(t, request)
	if !acquired.OK {
		t.Fatalf("acquire=%#v", acquired)
	}
	payloadHash, err := hashFile(filepath.Join(paths.mount, "baseline", "payload.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if payloadHash != fixture.PayloadHash {
		t.Fatalf("payload hash=%s want=%s", payloadHash, fixture.PayloadHash)
	}
	if err := os.WriteFile(filepath.Join(paths.mount, "helper-mutation.txt"), []byte("helper mutation"), 0600); err != nil {
		t.Fatal(err)
	}
	duplicate := client.call(t, request)
	if !duplicate.OK || duplicate.Lease.LeaseID != acquired.Lease.LeaseID {
		t.Fatalf("duplicate=%#v", duplicate)
	}
	released := client.call(t, storagehelper.Request{SchemaVersion: 1, Operation: "release", RequestID: "req-release", LeaseID: acquired.Lease.LeaseID})
	if !released.OK || !released.Released {
		t.Fatalf("release=%#v", released)
	}
	if _, err := os.Stat(paths.mount); !os.IsNotExist(err) {
		t.Fatalf("mount remains: %v", err)
	}
	if _, err := os.Stat(paths.child); !os.IsNotExist(err) {
		t.Fatalf("child remains: %v", err)
	}
	if journal := readJournal(t, paths.store, acquired.Lease.LeaseID); journal.State != storagehelper.StateReleased {
		t.Fatalf("journal=%#v", journal)
	}
	shutdown := client.call(t, storagehelper.Request{SchemaVersion: 1, Operation: "shutdown", RequestID: "req-shutdown"})
	if !shutdown.OK {
		t.Fatalf("shutdown=%#v", shutdown)
	}
	_ = client.stdin.Close()
	client.wait(t)
	after := fileBackedDisks(t)
	if before != after {
		t.Fatalf("File Backed Virtual disks changed: before=%s after=%s", before, after)
	}
	cleanupSuccessfulCase(t, paths)
}

func TestVHDXStorageHelperEOFCleanup(t *testing.T) {
	paths := prepareIntegrationPaths(t)
	requireElevated(t)
	before := fileBackedDisks(t)
	_, err := vhdxprobe.PrepareHelperParentFixture(context.Background(), paths.parent, filepath.Join(paths.root, "parent-mount"))
	if err != nil {
		t.Fatal(err)
	}
	client := startHelper(t)
	request := storagehelper.Request{SchemaVersion: 1, Operation: "acquire", RequestID: "req-eof-acquire", StoreRoot: paths.store, WorkspaceRoot: paths.workspace, ParentPath: paths.parent, ChildPath: paths.child, MountPath: paths.mount, DeleteChildOnRelease: true}
	acquired := client.call(t, request)
	if !acquired.OK {
		t.Fatalf("acquire=%#v", acquired)
	}
	if err := client.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	client.wait(t)
	if _, err := os.Stat(paths.mount); !os.IsNotExist(err) {
		t.Fatalf("mount remains: %v", err)
	}
	if _, err := os.Stat(paths.child); !os.IsNotExist(err) {
		t.Fatalf("child remains: %v", err)
	}
	if journal := readJournal(t, paths.store, acquired.Lease.LeaseID); journal.State != storagehelper.StateReleased {
		t.Fatalf("journal=%#v", journal)
	}
	after := fileBackedDisks(t)
	if before != after {
		t.Fatalf("File Backed Virtual disks changed: before=%s after=%s", before, after)
	}
	cleanupSuccessfulCase(t, paths)
}
