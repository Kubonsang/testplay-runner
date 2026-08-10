//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
	"github.com/Kubonsang/testplay-runner/internal/vhdxworkspace"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const installReceiptSchema = 1

type storageInstallReceipt struct {
	SchemaVersion int    `json:"schemaVersion"`
	ServiceName   string `json:"serviceName"`
	StoreRoot     string `json:"storeRoot"`
	WorkspaceRoot string `json:"workspaceRoot"`
	ConfigPath    string `json:"configPath"`
	UserSID       string `json:"userSid"`
	Executable    string `json:"executable"`
}

func platformInstallStorage(requestedRoot string) (any, error) {
	if !vhdxworkspace.IsElevated() {
		return nil, fmt.Errorf("storage install requires an elevated Administrator process")
	}
	requestedRoot, err := requireAbsoluteOptionalRoot(requestedRoot)
	if err != nil {
		return nil, err
	}
	if receipt, receiptErr := loadInstallReceipt(); receiptErr == nil {
		if requestedRoot != "" && !strings.EqualFold(filepath.Clean(requestedRoot), filepath.Clean(receipt.StoreRoot)) {
			return nil, fmt.Errorf("preserved storage root is %s, not %s", receipt.StoreRoot, requestedRoot)
		}
		return reinstallPreservedStorage(receipt)
	} else if !os.IsNotExist(receiptErr) {
		return nil, fmt.Errorf("read existing install receipt: %w", receiptErr)
	}
	programData := os.Getenv("ProgramData")
	localAppData := os.Getenv("LOCALAPPDATA")
	if requestedRoot == "" {
		requestedRoot = filepath.Join(programData, "TestPlay", "Storage")
	}
	workspaceRoot := filepath.Join(localAppData, "TestPlay", "Workspaces")
	if err := requireNewOrEmptyDirectory(requestedRoot); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(requestedRoot, 0700); err != nil {
		return nil, err
	}
	if err := validateStorageVolume(requestedRoot); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(workspaceRoot, 0700); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	installedExecutable := filepath.Join(requestedRoot, "broker", "testplay.exe")
	if err := os.Mkdir(filepath.Dir(installedExecutable), 0700); err != nil {
		return nil, err
	}
	if err := copyExecutableDurable(executable, installedExecutable); err != nil {
		return nil, err
	}
	sid, err := vhdxworkspace.CurrentUserSID()
	if err != nil {
		return nil, err
	}
	if err := applyBrokerACL(requestedRoot, sid, false); err != nil {
		return nil, err
	}
	if err := applyBrokerACL(workspaceRoot, sid, true); err != nil {
		return nil, err
	}
	configPath := filepath.Join(requestedRoot, "broker-config.json")
	config := vhdxworkspace.ServiceConfig{SchemaVersion: vhdxworkspace.ServiceConfigSchemaVersion, StoreRoot: requestedRoot, WorkspaceRoot: workspaceRoot, UserSID: sid, QuotaBytes: vhdxworkspace.DefaultQuotaBytes, HostFloorBytes: vhdxworkspace.DefaultHostFloor, ChildReserveBytes: vhdxworkspace.DefaultChildReserve, PipeName: vhdxworkspace.DefaultPipeName}
	if err := vhdxworkspace.SaveServiceConfig(configPath, config); err != nil {
		return nil, err
	}
	receipt := storageInstallReceipt{SchemaVersion: installReceiptSchema, ServiceName: vhdxworkspace.WindowsServiceName, StoreRoot: requestedRoot, WorkspaceRoot: workspaceRoot, ConfigPath: configPath, UserSID: sid, Executable: installedExecutable}
	if err := saveInstallReceipt(receipt); err != nil {
		return nil, err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return nil, err
	}
	defer manager.Disconnect()
	if existing, openErr := manager.OpenService(vhdxworkspace.WindowsServiceName); openErr == nil {
		existing.Close()
		return nil, fmt.Errorf("service %s already exists", vhdxworkspace.WindowsServiceName)
	}
	service, err := manager.CreateService(vhdxworkspace.WindowsServiceName, installedExecutable, mgr.Config{DisplayName: "TestPlay Storage Broker", Description: "Owns TestPlay differencing VHDX workspace lifecycle", StartType: mgr.StartAutomatic}, "storage", "broker-run", "--service-config", configPath)
	if err != nil {
		return nil, err
	}
	defer service.Close()
	if err := service.Start(); err != nil {
		_ = service.Delete()
		return nil, err
	}
	return map[string]any{"status": "INSTALLED", "service": vhdxworkspace.WindowsServiceName, "storeRoot": requestedRoot, "workspaceRoot": workspaceRoot, "quotaBytes": config.QuotaBytes, "userSid": sid}, nil
}

func platformUpgradeStorage() (any, error) {
	if !vhdxworkspace.IsElevated() {
		return nil, fmt.Errorf("storage upgrade requires elevation")
	}
	receipt, err := loadInstallReceipt()
	if err != nil {
		return nil, err
	}
	config, err := vhdxworkspace.LoadServiceConfig(receipt.ConfigPath)
	if err != nil || !sameReceiptConfig(receipt, config) {
		return nil, fmt.Errorf("install receipt identity mismatch: %w", err)
	}
	if err := validateInstallReceiptPaths(receipt); err != nil {
		return nil, err
	}
	manager, service, err := openBrokerService()
	if err != nil {
		return nil, err
	}
	defer manager.Disconnect()
	defer service.Close()
	if err := stopService(service); err != nil {
		return nil, err
	}
	currentExecutable, err := os.Executable()
	if err != nil {
		_ = service.Start()
		return nil, err
	}
	if err := copyExecutableDurable(currentExecutable, receipt.Executable); err != nil {
		_ = service.Start()
		return nil, err
	}
	if err := service.Start(); err != nil {
		return nil, err
	}
	return map[string]any{"status": "UPGRADED", "service": vhdxworkspace.WindowsServiceName}, nil
}

func platformUninstallStorage(preserve bool) (any, error) {
	if !vhdxworkspace.IsElevated() {
		return nil, fmt.Errorf("storage uninstall requires elevation")
	}
	receipt, err := loadInstallReceipt()
	if err != nil {
		return nil, err
	}
	if !preserve {
		response, statusErr := callStorageBroker(context.Background(), vhdxworkspace.OperationStatus, false, "", "")
		if statusErr != nil {
			return nil, fmt.Errorf("refusing data removal without broker status: %w", statusErr)
		}
		if response.Status == nil || response.Status.ActiveChildCount != 0 || response.Status.RetainedChildCount != 0 || response.Status.PendingCount != 0 || response.Status.QuarantineCount != 0 {
			return nil, fmt.Errorf("refusing data removal while protected broker state remains")
		}
		config, loadErr := vhdxworkspace.LoadServiceConfig(receipt.ConfigPath)
		if loadErr != nil || !sameReceiptConfig(receipt, config) {
			return nil, fmt.Errorf("refusing data removal: install receipt identity mismatch: %w", loadErr)
		}
		if err := validateInstallReceiptPaths(receipt); err != nil {
			return nil, fmt.Errorf("refusing data removal: %w", err)
		}
	}
	manager, service, err := openBrokerService()
	if err != nil {
		return nil, err
	}
	defer manager.Disconnect()
	defer service.Close()
	_ = stopService(service)
	if err := service.Delete(); err != nil {
		return nil, err
	}
	dataPreserved := true
	if !preserve {
		if entries, readErr := os.ReadDir(receipt.WorkspaceRoot); readErr != nil && !os.IsNotExist(readErr) {
			return nil, readErr
		} else if len(entries) != 0 {
			return nil, fmt.Errorf("refusing to remove non-empty workspace root: %s", receipt.WorkspaceRoot)
		}
		if err := os.RemoveAll(receipt.StoreRoot); err != nil {
			return nil, err
		}
		if err := os.Remove(receipt.WorkspaceRoot); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err := os.Remove(installReceiptPath()); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		dataPreserved = false
	}
	return map[string]any{"status": "UNINSTALLED", "dataPreserved": dataPreserved, "storeRoot": receipt.StoreRoot}, nil
}

func openBrokerService() (*mgr.Mgr, *mgr.Service, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return nil, nil, err
	}
	service, err := manager.OpenService(vhdxworkspace.WindowsServiceName)
	if err != nil {
		manager.Disconnect()
		return nil, nil, err
	}
	return manager, service, nil
}

func stopService(service *mgr.Service) error {
	status, err := service.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err := service.Control(svc.Stop); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		status, err = service.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Stopped {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return context.DeadlineExceeded
}

func requireNewOrEmptyDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("storage root already exists and is not empty: %s", path)
	}
	return nil
}

func validateStorageVolume(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pathPtr)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("storage root must not be a reparse point: %s", path)
	}
	volumeRoot := filepath.VolumeName(path) + `\`
	if filepath.VolumeName(path) == "" {
		return fmt.Errorf("storage root must be on a local Windows volume")
	}
	rootPtr, err := windows.UTF16PtrFromString(volumeRoot)
	if err != nil {
		return err
	}
	filesystem := make([]uint16, 64)
	if err := windows.GetVolumeInformation(rootPtr, nil, 0, nil, nil, nil, &filesystem[0], uint32(len(filesystem))); err != nil {
		return err
	}
	if !strings.EqualFold(windows.UTF16ToString(filesystem), "NTFS") {
		return fmt.Errorf("storage root filesystem must be NTFS")
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(rootPtr, &free, nil, nil); err != nil {
		return err
	}
	required := uint64(vhdxworkspace.DefaultHostFloor + vhdxworkspace.DefaultChildReserve)
	if free < required {
		return fmt.Errorf("storage host free bytes %d below install floor %d", free, required)
	}
	return nil
}

func applyBrokerACL(path, userSID string, userModify bool) error {
	permission := "(OI)(CI)RX"
	if userModify {
		permission = "(OI)(CI)M"
	}
	command := exec.Command("icacls.exe", path, "/inheritance:r", "/grant:r", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F", "*"+userSID+":"+permission)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set broker ACL: %w: %s", err, output)
	}
	return nil
}

func installReceiptPath() string {
	return filepath.Join(os.Getenv("ProgramData"), "TestPlay", "storage-install.json")
}

func saveInstallReceipt(receipt storageInstallReceipt) error {
	if !filepath.IsAbs(receipt.StoreRoot) || !filepath.IsAbs(receipt.WorkspaceRoot) || !filepath.IsAbs(receipt.ConfigPath) || receipt.UserSID == "" {
		return fmt.Errorf("invalid install receipt")
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	path := installReceiptPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return atomicfile.WriteExclusiveDurable(path, append(data, '\n'), 0600)
}

func loadInstallReceipt() (storageInstallReceipt, error) {
	data, err := os.ReadFile(installReceiptPath())
	if err != nil {
		return storageInstallReceipt{}, err
	}
	var receipt storageInstallReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return storageInstallReceipt{}, err
	}
	if receipt.SchemaVersion != installReceiptSchema || receipt.ServiceName != vhdxworkspace.WindowsServiceName || !filepath.IsAbs(receipt.StoreRoot) || !filepath.IsAbs(receipt.WorkspaceRoot) || !filepath.IsAbs(receipt.ConfigPath) {
		return storageInstallReceipt{}, fmt.Errorf("invalid install receipt")
	}
	return receipt, nil
}

func sameReceiptConfig(receipt storageInstallReceipt, config vhdxworkspace.ServiceConfig) bool {
	return strings.EqualFold(filepath.Clean(receipt.StoreRoot), filepath.Clean(config.StoreRoot)) && strings.EqualFold(filepath.Clean(receipt.WorkspaceRoot), filepath.Clean(config.WorkspaceRoot)) && receipt.UserSID == config.UserSID
}

func reinstallPreservedStorage(receipt storageInstallReceipt) (any, error) {
	config, err := vhdxworkspace.LoadServiceConfig(receipt.ConfigPath)
	if err != nil || !sameReceiptConfig(receipt, config) {
		return nil, fmt.Errorf("preserved install identity mismatch: %w", err)
	}
	if err := validateInstallReceiptPaths(receipt); err != nil {
		return nil, err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return nil, err
	}
	defer manager.Disconnect()
	if existing, openErr := manager.OpenService(vhdxworkspace.WindowsServiceName); openErr == nil {
		existing.Close()
		return nil, fmt.Errorf("service %s already exists", vhdxworkspace.WindowsServiceName)
	}
	currentExecutable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := copyExecutableDurable(currentExecutable, receipt.Executable); err != nil {
		return nil, err
	}
	service, err := manager.CreateService(vhdxworkspace.WindowsServiceName, receipt.Executable, mgr.Config{DisplayName: "TestPlay Storage Broker", Description: "Owns TestPlay differencing VHDX workspace lifecycle", StartType: mgr.StartAutomatic}, "storage", "broker-run", "--service-config", receipt.ConfigPath)
	if err != nil {
		return nil, err
	}
	defer service.Close()
	if err := service.Start(); err != nil {
		_ = service.Delete()
		return nil, err
	}
	return map[string]any{"status": "REINSTALLED", "service": vhdxworkspace.WindowsServiceName, "storeRoot": receipt.StoreRoot, "workspaceRoot": receipt.WorkspaceRoot, "quotaBytes": config.QuotaBytes, "userSid": receipt.UserSID}, nil
}

func validateInstallReceiptPaths(receipt storageInstallReceipt) error {
	if receipt.SchemaVersion != installReceiptSchema || receipt.ServiceName != vhdxworkspace.WindowsServiceName {
		return fmt.Errorf("invalid install receipt schema")
	}
	if !filepath.IsAbs(receipt.StoreRoot) || !filepath.IsAbs(receipt.WorkspaceRoot) || !filepath.IsAbs(receipt.ConfigPath) || !filepath.IsAbs(receipt.Executable) {
		return fmt.Errorf("install receipt paths must be absolute")
	}
	volumeRoot := filepath.VolumeName(receipt.StoreRoot) + `\`
	if filepath.VolumeName(receipt.StoreRoot) == "" || strings.EqualFold(filepath.Clean(receipt.StoreRoot), filepath.Clean(volumeRoot)) {
		return fmt.Errorf("unsafe storage root: %s", receipt.StoreRoot)
	}
	expectedConfig := filepath.Join(receipt.StoreRoot, "broker-config.json")
	expectedExecutable := filepath.Join(receipt.StoreRoot, "broker", "testplay.exe")
	if !strings.EqualFold(filepath.Clean(receipt.ConfigPath), filepath.Clean(expectedConfig)) || !strings.EqualFold(filepath.Clean(receipt.Executable), filepath.Clean(expectedExecutable)) {
		return fmt.Errorf("install receipt paths are outside the registered layout")
	}
	for _, path := range []string{receipt.StoreRoot, receipt.WorkspaceRoot} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("registered root is not a real directory: %s: %w", path, err)
		}
		pointer, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(pointer)
		if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("registered root is a reparse point: %s: %w", path, err)
		}
	}
	return nil
}

func copyExecutableDurable(source, destination string) error {
	if strings.EqualFold(filepath.Clean(source), filepath.Clean(destination)) {
		return nil
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("broker source is not a regular file: %w", err)
	}
	temporary := fmt.Sprintf("%s.install-%d", destination, time.Now().UnixNano())
	// The store DACL, not the DOS read-only attribute, protects the installed
	// broker. Keeping the file writable to LocalSystem/Administrators allows a
	// stopped service to be atomically upgraded later.
	output, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return err
	}
	cleanup := func() { _ = os.Remove(temporary) }
	sourceHash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, sourceHash), input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		cleanup()
		return err
	}
	if err := atomicfile.Rename(temporary, destination); err != nil {
		cleanup()
		return err
	}
	installed, err := os.Open(destination)
	if err != nil {
		return err
	}
	installedHash := sha256.New()
	_, hashErr := io.Copy(installedHash, installed)
	closeErr = installed.Close()
	if err := errors.Join(hashErr, closeErr); err != nil {
		return err
	}
	if !bytes.Equal(sourceHash.Sum(nil), installedHash.Sum(nil)) {
		return fmt.Errorf("installed broker binary hash mismatch")
	}
	return nil
}
