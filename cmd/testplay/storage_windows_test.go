//go:build windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/vhdxworkspace"
)

func TestCopyExecutableDurableReplacesAndVerifiesBytes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.exe")
	destination := filepath.Join(root, "broker", "testplay.exe")
	if err := os.Mkdir(filepath.Dir(destination), 0700); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("testplay-broker\n"), 1024)
	if err := os.WriteFile(source, want, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyExecutableDurable(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("installed bytes differ: got=%d want=%d", len(got), len(want))
	}
}

func TestValidateInstallReceiptPathsRejectsLayoutEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	workspace := filepath.Join(t.TempDir(), "workspaces")
	for _, path := range []string{root, workspace} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	receipt := storageInstallReceipt{
		SchemaVersion: installReceiptSchema,
		ServiceName:   vhdxworkspace.WindowsServiceName,
		StoreRoot:     root,
		WorkspaceRoot: workspace,
		ConfigPath:    filepath.Join(root, "broker-config.json"),
		Executable:    filepath.Join(root, "broker", "testplay.exe"),
		UserSID:       "S-1-5-21-test",
	}
	if err := validateInstallReceiptPaths(receipt); err != nil {
		t.Fatalf("valid receipt: %v", err)
	}
	receipt.Executable = filepath.Join(filepath.Dir(root), "outside.exe")
	if err := validateInstallReceiptPaths(receipt); err == nil {
		t.Fatal("expected executable layout escape rejection")
	}
}

func TestSameReceiptConfigUsesWindowsCaseInsensitivePaths(t *testing.T) {
	receipt := storageInstallReceipt{StoreRoot: `C:\ProgramData\TestPlay\Storage`, WorkspaceRoot: `C:\Users\User\AppData\Local\TestPlay\Workspaces`, UserSID: "S-1-5-21-test"}
	config := vhdxworkspace.ServiceConfig{StoreRoot: `c:\programdata\testplay\storage`, WorkspaceRoot: `c:\users\user\appdata\local\testplay\workspaces`, UserSID: receipt.UserSID}
	if !sameReceiptConfig(receipt, config) {
		t.Fatal("expected case-insensitive Windows receipt/config match")
	}
}
