//go:build windows

package vhdxstorage

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func TestVirtDiskStructureLayouts(t *testing.T) {
	if got := unsafe.Sizeof(virtualStorageType{}); got != 20 {
		t.Fatalf("VIRTUAL_STORAGE_TYPE size=%d", got)
	}
	if got := unsafe.Sizeof(createVirtualDiskParametersV2{}); got != 128 {
		t.Fatalf("CREATE_VIRTUAL_DISK_PARAMETERS_V2 size=%d", got)
	}
	if got := unsafe.Offsetof(createVirtualDiskParametersV2{}.ParentPath); got != 48 {
		t.Fatalf("ParentPath offset=%d", got)
	}
	if got := unsafe.Offsetof(createVirtualDiskParametersV2{}.ParentVirtualStorageType); got != 68 {
		t.Fatalf("ParentVirtualStorageType offset=%d", got)
	}
	if got := unsafe.Sizeof(openVirtualDiskParametersV1{}); got != 8 {
		t.Fatalf("OPEN_VIRTUAL_DISK_PARAMETERS_V1 size=%d", got)
	}
	if got := unsafe.Sizeof(attachVirtualDiskParametersV1{}); got != 8 {
		t.Fatalf("ATTACH_VIRTUAL_DISK_PARAMETERS_V1 size=%d", got)
	}
}

func TestUTF16ParentPathParameter(t *testing.T) {
	parent := `C:\테스트 작업\parent.vhdx`
	pointer, err := syscall.UTF16PtrFromString(parent)
	if err != nil {
		t.Fatal(err)
	}
	words := unsafe.Slice(pointer, len([]rune(parent))+2)
	if got := syscall.UTF16ToString(words); got != parent {
		t.Fatalf("round trip=%q", got)
	}
	parameters := createVirtualDiskParametersV2{Version: createVirtualDiskVersion2, ParentPath: pointer, ParentVirtualStorageType: vhdxStorageType()}
	if parameters.ParentPath == nil {
		t.Fatal("ParentPath was nil")
	}
}

func TestPhysicalDiskSafetyGate(t *testing.T) {
	number, err := DiskNumberFromPhysicalPath(`\\.\PhysicalDrive42`)
	if err != nil || number != 42 {
		t.Fatalf("number=%d err=%v", number, err)
	}
	for _, path := range []string{`C:\`, `\\.\C:`, `\\?\Volume{01234567-89ab-cdef-0123-456789abcdef}\`, `\\.\PhysicalDrive1\partition0`} {
		if _, err := DiskNumberFromPhysicalPath(path); err == nil {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
	busCheck := strings.Index(initializeDiskScript, "'File Backed Virtual'")
	initialize := strings.Index(initializeDiskScript, "Initialize-Disk")
	if busCheck < 0 || initialize < 0 || busCheck >= initialize {
		t.Fatal("File Backed Virtual gate must precede Initialize-Disk")
	}
}

func TestCloseZeroHandle(t *testing.T) {
	attachment := &Attachment{}
	if err := attachment.CloseHandle(); err != nil {
		t.Fatal(err)
	}
}

func TestStoragePowerShellParses(t *testing.T) {
	parser := `
$tokens = $null
$parseErrors = $null
[System.Management.Automation.Language.Parser]::ParseInput(
  $env:TESTPLAY_VHDX_BOOTSTRAP_SCRIPT,
  [ref]$tokens,
  [ref]$parseErrors
) | Out-Null
if ($parseErrors.Count -ne 0) {
  $parseErrors | ForEach-Object { Write-Error $_.Message }
  exit 1
}
`
	scripts := map[string]string{"initialize": initializeDiskScript, "resolve": resolveVolumeScript, "mount": mountDiskScript, "unmount": unmountDiskScript, "wait-detach": waitDetachScript}
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(script, "$diskNumber:") {
				t.Fatal("ambiguous PowerShell interpolation")
			}
			command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", parser)
			command.Env = append(os.Environ(), "TESTPLAY_VHDX_BOOTSTRAP_SCRIPT="+script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("parse failed: %v\n%s", err, output)
			}
		})
	}
}

func TestUnmountUsesNormalizedExactPartitionAccessPath(t *testing.T) {
	busCheck := strings.Index(unmountDiskScript, "'File Backed Virtual'")
	ownershipPoll := strings.Index(unmountDiskScript, "$ownershipDeadline")
	remove := strings.Index(unmountDiskScript, "Remove-PartitionAccessPath")
	visibilityPoll := strings.Index(unmountDiskScript, "$remainingAccessPaths")
	if busCheck < 0 || ownershipPoll < 0 || remove < 0 || visibilityPoll < 0 {
		t.Fatal("unmount script is missing an ownership or visibility boundary")
	}
	if !(busCheck < ownershipPoll && ownershipPoll < remove && remove < visibilityPoll) {
		t.Fatal("unmount safety gates must precede removal and post-remove visibility polling")
	}
	for _, contract := range []string{
		"[IO.Path]::GetFullPath($path)",
		"[StringComparison]::OrdinalIgnoreCase",
		"$ownedAccessPaths.Count -eq 1",
		"-AccessPath $ownedAccessPath",
		"$remainingAccessPaths.Count -ne 0",
	} {
		if !strings.Contains(unmountDiskScript, contract) {
			t.Fatalf("unmount script does not enforce %q", contract)
		}
	}
	if strings.Contains(unmountDiskScript, "Remove-PartitionAccessPath -InputObject $partition -AccessPath $mountPath") {
		t.Fatal("unmount must use the exact access path returned by the owned partition")
	}
}

func TestAllocatedFileSize(t *testing.T) {
	path := t.TempDir() + `\file.bin`
	if err := os.WriteFile(path, make([]byte, 4096), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := allocatedFileSize(path)
	if err != nil {
		t.Fatal(err)
	}
	if value == nil || *value <= 0 {
		t.Fatalf("allocated=%v", value)
	}
}
