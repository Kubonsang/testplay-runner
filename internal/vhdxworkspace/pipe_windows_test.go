//go:build windows

package vhdxworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestNamedPipeRejectsRemoteClients(t *testing.T) {
	if pipeMode&windows.PIPE_REJECT_REMOTE_CLIENTS == 0 {
		t.Fatal("named pipe must reject remote clients")
	}
}

func TestOpenNamedPipeRetriesTransientBusyInstance(t *testing.T) {
	path, err := windows.UTF16PtrFromString(`\\.\pipe\busy-test`)
	if err != nil {
		t.Fatal(err)
	}
	openCalls := 0
	waitCalls := 0
	handle, err := openNamedPipeWithRetry(context.Background(), path, time.Second, func(*uint16) (windows.Handle, error) {
		openCalls++
		if openCalls == 1 {
			return 0, windows.ERROR_PIPE_BUSY
		}
		return windows.Handle(42), nil
	}, func(*uint16, uint32) error {
		waitCalls++
		return nil
	})
	if err != nil || handle != windows.Handle(42) || openCalls != 2 || waitCalls != 1 {
		t.Fatalf("handle=%v err=%v openCalls=%d waitCalls=%d", handle, err, openCalls, waitCalls)
	}
}

func TestOpenNamedPipeRetriesBusyInstanceWhenWaitTemporarilyLosesName(t *testing.T) {
	path, err := windows.UTF16PtrFromString(`\\.\pipe\busy-missing-test`)
	if err != nil {
		t.Fatal(err)
	}
	openCalls := 0
	handle, err := openNamedPipeWithRetry(context.Background(), path, time.Second, func(*uint16) (windows.Handle, error) {
		openCalls++
		if openCalls == 1 {
			return 0, windows.ERROR_PIPE_BUSY
		}
		return windows.Handle(44), nil
	}, func(*uint16, uint32) error {
		return windows.ERROR_FILE_NOT_FOUND
	})
	if err != nil || handle != windows.Handle(44) || openCalls != 2 {
		t.Fatalf("handle=%v err=%v openCalls=%d", handle, err, openCalls)
	}
}

func TestOpenNamedPipeRetriesTransientMissingInstance(t *testing.T) {
	path, _ := windows.UTF16PtrFromString(`\\.\pipe\missing-test`)
	openCalls := 0
	waitCalls := 0
	handle, err := openNamedPipeWithRetry(context.Background(), path, time.Second, func(*uint16) (windows.Handle, error) {
		openCalls++
		if openCalls == 1 {
			return 0, windows.ERROR_FILE_NOT_FOUND
		}
		return windows.Handle(43), nil
	}, func(*uint16, uint32) error {
		waitCalls++
		return nil
	})
	if err != nil || handle != windows.Handle(43) || openCalls != 2 || waitCalls != 0 {
		t.Fatalf("handle=%v err=%v openCalls=%d waitCalls=%d", handle, err, openCalls, waitCalls)
	}
}

func TestOpenNamedPipeDoesNotRetryPermanentFailure(t *testing.T) {
	path, _ := windows.UTF16PtrFromString(`\\.\pipe\denied-test`)
	openCalls := 0
	_, err := openNamedPipeWithRetry(context.Background(), path, time.Second, func(*uint16) (windows.Handle, error) {
		openCalls++
		return 0, windows.ERROR_ACCESS_DENIED
	}, func(*uint16, uint32) error {
		t.Fatal("wait must not be called for a permanent error")
		return nil
	})
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || openCalls != 1 {
		t.Fatalf("err=%v openCalls=%d", err, openCalls)
	}
}

func TestOpenNamedPipeBusyHonorsContextDeadline(t *testing.T) {
	path, _ := windows.UTF16PtrFromString(`\\.\pipe\deadline-test`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := openNamedPipeWithRetry(ctx, path, time.Second, func(*uint16) (windows.Handle, error) {
		return 0, windows.ERROR_PIPE_BUSY
	}, func(_ *uint16, timeout uint32) error {
		time.Sleep(time.Duration(timeout) * time.Millisecond)
		return windows.ERROR_SEM_TIMEOUT
	})
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, windows.ERROR_SEM_TIMEOUT) {
		t.Fatalf("err=%v, want deadline or bounded timeout", err)
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

func TestNamedPipeConcurrentRoundTripsRetryBusyInstances(t *testing.T) {
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
	name := fmt.Sprintf(`\\.\pipe\testplay-storage-broker-concurrent-%d`, time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (&PipeServer{Name: name, AllowedSID: sid, Broker: broker}).Serve(ctx) }()
	client := PipeClient{Name: name}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err = client.Call(context.Background(), NewRequest(OperationHello, "pipe-prime")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("prime broker connection: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	const clients = 8
	start := make(chan struct{})
	errorsByClient := make([]error, clients)
	var wait sync.WaitGroup
	for index := range clients {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			response, callErr := client.Call(context.Background(), NewRequest(OperationHello, fmt.Sprintf("pipe-concurrent-%d", index)))
			if callErr != nil {
				errorsByClient[index] = callErr
			} else if !response.OK || response.WorkspaceRoot != workspace {
				errorsByClient[index] = fmt.Errorf("unexpected response: %+v", response)
			}
		}()
	}
	close(start)
	wait.Wait()
	for index, callErr := range errorsByClient {
		if callErr != nil {
			t.Errorf("client %d: %v", index, callErr)
		}
	}

	cancel()
	go func() { _, _ = client.Call(context.Background(), NewRequest(OperationHello, "pipe-stop")) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pipe server did not stop")
	}
}

func TestRetryableStalePipeWrite(t *testing.T) {
	for _, err := range []error{
		windows.ERROR_NO_DATA,
		windows.ERROR_PIPE_NOT_CONNECTED,
		windows.ERROR_BROKEN_PIPE,
		fmt.Errorf("wrapped: %w", windows.ERROR_NO_DATA),
	} {
		if !retryableStalePipeWrite(err) {
			t.Fatalf("expected retryable error: %v", err)
		}
	}
	if retryableStalePipeWrite(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("access denied must not be retried")
	}
}
