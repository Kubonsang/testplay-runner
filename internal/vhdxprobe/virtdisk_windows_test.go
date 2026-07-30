//go:build windows

package vhdxprobe

import (
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func TestVirtDiskStructureLayouts(t *testing.T) {
	if got := unsafe.Sizeof(virtualStorageType{}); got != 20 {
		t.Fatalf("VIRTUAL_STORAGE_TYPE size = %d, want 20", got)
	}
	if got := unsafe.Sizeof(createVirtualDiskParametersV2{}); got != 128 {
		t.Fatalf("CREATE_VIRTUAL_DISK_PARAMETERS_V2 size = %d, want 128", got)
	}
	if got := unsafe.Offsetof(createVirtualDiskParametersV2{}.ParentPath); got != 48 {
		t.Fatalf("ParentPath offset = %d, want 48", got)
	}
	if got := unsafe.Offsetof(createVirtualDiskParametersV2{}.ParentVirtualStorageType); got != 68 {
		t.Fatalf("ParentVirtualStorageType offset = %d, want 68", got)
	}
	if got := unsafe.Sizeof(openVirtualDiskParametersV1{}); got != 8 {
		t.Fatalf("OPEN_VIRTUAL_DISK_PARAMETERS_V1 size = %d, want 8", got)
	}
	if got := unsafe.Sizeof(attachVirtualDiskParametersV1{}); got != 8 {
		t.Fatalf("ATTACH_VIRTUAL_DISK_PARAMETERS_V1 size = %d, want 8", got)
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
		t.Fatalf("UTF-16 round trip = %q", got)
	}
	parameters := createVirtualDiskParametersV2{
		Version:                  createVirtualDiskVersion2,
		ParentPath:               pointer,
		ParentVirtualStorageType: vhdxStorageType(),
	}
	if parameters.ParentPath == nil {
		t.Fatal("ParentPath was nil")
	}
}

func TestPhysicalDiskSafetyGate(t *testing.T) {
	number, err := diskNumberFromPhysicalPath(`\\.\PhysicalDrive42`)
	if err != nil || number != 42 {
		t.Fatalf("number=%d err=%v", number, err)
	}
	for _, path := range []string{
		`C:\`,
		`\\.\C:`,
		`\\?\Volume{01234567-89ab-cdef-0123-456789abcdef}\`,
		`\\.\PhysicalDrive1\partition0`,
	} {
		if _, err := diskNumberFromPhysicalPath(path); err == nil {
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
	if err := closeVirtualDiskHandle(0); err != nil {
		t.Fatal(err)
	}
}
