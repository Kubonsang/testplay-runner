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
	"unsafe"

	"golang.org/x/sys/windows"
)

const DefaultPipeName = `\\.\pipe\testplay-storage-broker-v2`

const pipeMode = windows.PIPE_TYPE_MESSAGE | windows.PIPE_READMODE_MESSAGE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS

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
	handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IMPERSONATION, 0)
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
)

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
