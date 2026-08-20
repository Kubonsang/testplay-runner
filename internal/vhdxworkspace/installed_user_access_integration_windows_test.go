//go:build windows && vhdx_integration

package vhdxworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const installedUserAccessTestEnv = "TESTPLAY_VHDX_INSTALLED_USER_ACCESS"

func TestInstalledUserCanWriteMountedParent(t *testing.T) {
	if os.Getenv(installedUserAccessTestEnv) != "1" {
		t.Skip(installedUserAccessTestEnv + "=1 is required; installed-broker VHDX integration is opt-in")
	}
	if IsElevated() {
		t.Fatal("write-access regression must run with a non-elevated user token")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := DefaultClient()
	unique := fmt.Sprintf("installed-user-write-%d", time.Now().UnixNano())

	hello, err := client.Call(ctx, NewRequest(OperationHello, unique+"-hello"))
	if err != nil {
		t.Fatalf("connect to installed broker: %v", err)
	}
	if !filepath.IsAbs(hello.WorkspaceRoot) {
		t.Fatalf("broker returned invalid workspace root %q", hello.WorkspaceRoot)
	}
	before := requireIntegrationStatus(t, ctx, client, unique+"-status-before")

	workspacePath := filepath.Join(hello.WorkspaceRoot, unique)
	if err := os.Mkdir(workspacePath, 0700); err != nil {
		t.Fatalf("create workspace shell: %v", err)
	}
	transactionID := ""
	defer func() {
		if transactionID != "" {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
			defer cleanupCancel()
			abort := NewRequest(OperationAbortParent, unique+"-abort-cleanup")
			abort.TransactionID = transactionID
			if _, abortErr := client.Call(cleanupCtx, abort); abortErr != nil {
				t.Errorf("abort parent after failure: %v", abortErr)
				return
			}
		}
		libraryPath := filepath.Join(workspacePath, "Library")
		if _, statErr := os.Lstat(libraryPath); !os.IsNotExist(statErr) {
			t.Errorf("mounted Library remained after abort: %v", statErr)
			return
		}
		if removeErr := os.Remove(workspacePath); removeErr != nil && !os.IsNotExist(removeErr) {
			t.Errorf("remove workspace shell: %v", removeErr)
		}
	}()

	digest := sha256.Sum256([]byte(unique))
	key := CompatibilityKey{
		SchemaVersion: ParentSchemaVersion,
		Digest:        hex.EncodeToString(digest[:]),
		Provider:      Provider,
		Filesystem:    "NTFS",
		VirtualBytes:  DefaultVirtualBytes,
		BlockBytes:    DefaultBlockBytes,
		SectorBytes:   DefaultSectorBytes,
	}
	begin := NewRequest(OperationBeginParentBuild, unique+"-begin")
	begin.WorkspaceID = unique
	begin.ParentKey = &key
	begin.Source = &SourceSnapshot{}
	begin.ClientPID = os.Getpid()
	started, err := client.Call(ctx, begin)
	if err != nil {
		t.Fatalf("begin parent build: %v", err)
	}
	if started.ParentBuild == nil || started.ParentBuild.State != "mounted" || started.ParentBuild.TransactionID == "" {
		t.Fatalf("unexpected parent-build response: %+v", started.ParentBuild)
	}
	transactionID = started.ParentBuild.TransactionID
	if want := filepath.Join(workspacePath, "Library"); !strings.EqualFold(filepath.Clean(started.ParentBuild.MountPath), filepath.Clean(want)) {
		t.Fatalf("mount path=%q, want %q", started.ParentBuild.MountPath, want)
	}

	probePath := filepath.Join(started.ParentBuild.MountPath, "installed-user-write-probe.txt")
	const payload = "installed user write access\n"
	if err := os.WriteFile(probePath, []byte(payload), 0600); err != nil {
		t.Fatalf("write mounted VHDX as installed user: %v", err)
	}
	got, err := os.ReadFile(probePath)
	if err != nil || string(got) != payload {
		t.Fatalf("read mounted VHDX: value=%q err=%v", got, err)
	}
	if err := os.Remove(probePath); err != nil {
		t.Fatalf("remove mounted VHDX probe: %v", err)
	}

	abort := NewRequest(OperationAbortParent, unique+"-abort")
	abort.TransactionID = transactionID
	if _, err := client.Call(ctx, abort); err != nil {
		t.Fatalf("abort parent: %v", err)
	}
	transactionID = ""
	after := requireIntegrationStatus(t, ctx, client, unique+"-status-after")
	if after.ParentCount != before.ParentCount ||
		after.ActiveChildCount != before.ActiveChildCount ||
		after.RetainedChildCount != before.RetainedChildCount ||
		after.PendingCount != before.PendingCount ||
		after.QuarantineCount != before.QuarantineCount {
		t.Fatalf("broker residual changed: before=%+v after=%+v", before, after)
	}
}

func requireIntegrationStatus(t *testing.T, ctx context.Context, client Client, requestID string) Status {
	t.Helper()
	response, err := client.Call(ctx, NewRequest(OperationStatus, requestID))
	if err != nil {
		t.Fatalf("broker status: %v", err)
	}
	if response.Status == nil {
		t.Fatal("broker status response omitted status")
	}
	return *response.Status
}
