//go:build windows

package vhdxworkspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestPipeSecurityDescriptorAllowsOnlySystemAdminsAndInstalledUser(t *testing.T) {
	sid := "S-1-5-21-1-2-3-1001"
	sddl := pipeSDDL(sid)
	for _, required := range []string{"D:P", ";;;SY", ";;;BA", ";;;" + sid} {
		if !strings.Contains(sddl, required) {
			t.Fatalf("SDDL %q missing %q", sddl, required)
		}
	}
	for _, forbidden := range []string{";;;AN", ";;;NU", ";;;WD"} {
		if strings.Contains(sddl, forbidden) {
			t.Fatalf("SDDL %q includes %q", sddl, forbidden)
		}
	}
	if _, err := windows.SecurityDescriptorFromString(sddl); err != nil {
		t.Fatalf("invalid SDDL: %v", err)
	}
}

func TestNamedPipeRoundTripAuthenticatesCurrentUser(t *testing.T) {
	sid, err := CurrentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspaces")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	broker, err := NewBroker(BrokerConfig{StoreRoot: filepath.Join(root, "store"), WorkspaceRoot: workspace, UserSID: sid}, &fakeNative{})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf(`\\.\pipe\testplay-storage-broker-test-%d`, time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (&PipeServer{Name: name, AllowedSID: sid, Broker: broker}).Serve(ctx) }()
	client := PipeClient{Name: name}
	var response Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err = client.Call(context.Background(), NewRequest(OperationHello, "pipe-hello"))
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || !response.OK || response.WorkspaceRoot != workspace {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	cancel()
	go func() { _, _ = client.Call(context.Background(), NewRequest(OperationHello, "pipe-stop")) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pipe server did not stop")
	}
}
