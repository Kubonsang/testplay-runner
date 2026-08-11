//go:build windows

package vhdxworkspace

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const DefaultPipeName = `\\.\pipe\testplay-storage-broker-v2`

const pipeMode = windows.PIPE_TYPE_MESSAGE | windows.PIPE_READMODE_MESSAGE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS

const (
	pipeOpenTimeout = 5 * time.Second
	pipeWaitSlice   = 100 * time.Millisecond
)

type PipeClient struct{ Name string }

func DefaultClient() Client { return PipeClient{Name: DefaultPipeName} }

func (client PipeClient) Call(ctx context.Context, request Request) (Response, error) {
	name := client.Name
	if name == "" {
		name = DefaultPipeName
	}
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	default:
	}
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return Response{}, err
	}
	handle, err := openNamedPipeWithRetry(ctx, path, pipeOpenTimeout, createPipeFile, waitNamedPipe)
	if err != nil {
		return Response{}, fmt.Errorf("%w: open named pipe: %w", ErrBrokerUnavailable, err)
	}
	file := os.NewFile(uintptr(handle), name)
	defer file.Close()
	if err := json.NewEncoder(file).Encode(request); err != nil {
		return Response{}, err
	}
	var response Response
	if err := json.NewDecoder(bufio.NewReader(file)).Decode(&response); err != nil {
		return Response{}, err
	}
	if !response.OK {
		if response.Error != nil {
			return response, response.Error
		}
		return response, fmt.Errorf("broker request failed")
	}
	return response, nil
}

type pipeOpenFunc func(*uint16) (windows.Handle, error)
type pipeWaitFunc func(*uint16, uint32) error

func createPipeFile(path *uint16) (windows.Handle, error) {
	return windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IMPERSONATION, 0)
}

func openNamedPipeWithRetry(ctx context.Context, path *uint16, maxWait time.Duration, open pipeOpenFunc, wait pipeWaitFunc) (windows.Handle, error) {
	deadline := time.Now().Add(maxWait)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		handle, err := open(path)
		if err == nil {
			return handle, nil
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return 0, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, windows.ERROR_SEM_TIMEOUT
		}
		waitFor := min(remaining, pipeWaitSlice)
		waitMilliseconds := uint32((waitFor + time.Millisecond - 1) / time.Millisecond)
		if err := wait(path, waitMilliseconds); err != nil && !errors.Is(err, windows.ERROR_SEM_TIMEOUT) {
			return 0, err
		}
	}
}

type PipeServer struct {
	Name       string
	AllowedSID string
	Broker     *Broker
	wait       sync.WaitGroup
}

func (server *PipeServer) Serve(ctx context.Context) error {
	if server.Broker == nil || strings.TrimSpace(server.AllowedSID) == "" {
		return ErrInvalidInput
	}
	name := server.Name
	if name == "" {
		name = DefaultPipeName
	}
	securityDescriptor, err := windows.SecurityDescriptorFromString(pipeSDDL(server.AllowedSID))
	if err != nil {
		return err
	}
	security := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: securityDescriptor}
	for {
		select {
		case <-ctx.Done():
			server.wait.Wait()
			return ctx.Err()
		default:
		}
		namePtr, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return err
		}
		handle, err := windows.CreateNamedPipe(namePtr, windows.PIPE_ACCESS_DUPLEX, pipeMode, windows.PIPE_UNLIMITED_INSTANCES, 1<<20, 1<<20, 5000, security)
		if err != nil {
			return err
		}
		connectErr := windows.ConnectNamedPipe(handle, nil)
		if connectErr != nil && !errors.Is(connectErr, windows.ERROR_PIPE_CONNECTED) {
			windows.CloseHandle(handle)
			if ctx.Err() != nil {
				server.wait.Wait()
				return ctx.Err()
			}
			return connectErr
		}
		server.wait.Add(1)
		go func(pipe windows.Handle) {
			defer server.wait.Done()
			defer windows.DisconnectNamedPipe(pipe)
			file := os.NewFile(uintptr(pipe), name)
			defer file.Close()
			server.serveConnection(ctx, pipe, file)
		}(handle)
	}
}

func (server *PipeServer) serveConnection(ctx context.Context, pipe windows.Handle, file *os.File) {
	response := Response{SchemaVersion: ProtocolSchemaVersion, OK: false}
	callerSID, err := authenticatedPipeCallerSID(pipe)
	if err != nil {
		response.Error = &Error{Code: "unauthorized-client", Operation: "authenticate-pipe", Message: err.Error()}
		_ = json.NewEncoder(file).Encode(response)
		return
	}
	var request Request
	decoder := json.NewDecoder(bufio.NewReader(file))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		response.Error = &Error{Code: "invalid-request", Operation: "decode", Message: err.Error()}
		_ = json.NewEncoder(file).Encode(response)
		return
	}
	response = server.Broker.Handle(ctx, callerSID, request)
	_ = json.NewEncoder(file).Encode(response)
}

func pipeSDDL(userSID string) string {
	return "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;" + userSID + ")"
}

var (
	advapi32                       = windows.NewLazySystemDLL("advapi32.dll")
	procImpersonateNamedPipeClient = advapi32.NewProc("ImpersonateNamedPipeClient")
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procWaitNamedPipeW             = kernel32.NewProc("WaitNamedPipeW")
)

func waitNamedPipe(path *uint16, timeoutMilliseconds uint32) error {
	success, _, callErr := procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(path)), uintptr(timeoutMilliseconds))
	if success != 0 {
		return nil
	}
	if callErr != nil && !errors.Is(callErr, syscall.Errno(0)) {
		return callErr
	}
	return windows.ERROR_SEM_TIMEOUT
}

func authenticatedPipeCallerSID(pipe windows.Handle) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	success, _, callErr := procImpersonateNamedPipeClient.Call(uintptr(pipe))
	if success == 0 {
		return "", callErr
	}
	defer windows.RevertToSelf()
	var token windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token); err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}
