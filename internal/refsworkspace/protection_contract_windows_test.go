//go:build windows

package refsworkspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsProtectedBaselineACLContract(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "Library")
	child := filepath.Join(root, "ArtifactDB")
	file := filepath.Join(child, "artifact.bin")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte("readable immutable baseline")
	if err := os.WriteFile(file, content, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = makeWritableTree(root) })

	evidence, err := protectBaselineTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.RegularFileCount != 1 || evidence.ReadOnlyFileCount != 1 || evidence.DirectoryCount != 2 || evidence.NonInheritingEntryCount != 3 {
		t.Fatalf("protection evidence=%+v", evidence)
	}
	if err := verifyBaselineProtection(root, evidence); err != nil {
		t.Fatalf("protected baseline was not verifiable: %v", err)
	}
	for _, path := range []string{root, child, file} {
		assertProtectedBaselineDACL(t, path)
	}

	readBack, err := os.ReadFile(file)
	if err != nil || string(readBack) != string(content) {
		t.Fatalf("protected baseline is not readable: data=%q err=%v", readBack, err)
	}
	assertAccessDenied(t, "write protected file", func() error {
		return os.WriteFile(file, []byte("modified"), 0600)
	})
	assertAccessDenied(t, "delete protected file", func() error {
		return os.Remove(file)
	})
	assertAccessDenied(t, "create protected child", func() error {
		return os.WriteFile(filepath.Join(child, "replacement.bin"), []byte("replacement"), 0600)
	})
	replacement := filepath.Join(base, "replacement.bin")
	if err := os.WriteFile(replacement, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	assertAccessDenied(t, "replace protected child", func() error {
		return os.Rename(replacement, file)
	})

	if err := makeWritableTree(root); err != nil {
		t.Fatalf("administrative recovery reset failed: %v", err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED != 0 {
		t.Fatal("recovery left DACL protected")
	}
	if err := os.Remove(file); err != nil {
		t.Fatalf("recovery did not restore delete access: %v", err)
	}
}

func assertAccessDenied(t *testing.T, operation string, run func() error) {
	t.Helper()
	err := run()
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded", operation)
	}
	if !errors.Is(err, os.ErrPermission) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("%s returned non-access error: %v", operation, err)
	}
}

func assertProtectedBaselineDACL(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || owner.String() == "" {
		t.Fatalf("owner SID unavailable for %s: %v", path, err)
	}
	group, _, err := descriptor.Group()
	if err != nil || group == nil || group.String() == "" {
		t.Fatalf("group SID unavailable for %s: %v", path, err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("DACL inherits at %s", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("DACL unavailable for %s: %v", path, err)
	}
	if dacl.AceCount != 3 {
		t.Fatalf("ACE count=%d path=%s sddl=%s", dacl.AceCount, path, descriptor.String())
	}
	system, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	administrators, _ := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	users, _ := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	wantSIDs := []*windows.SID{system, administrators, users}
	const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	wantMasks := []windows.ACCESS_MASK{fileAllAccess, fileAllAccess, windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE}
	for index := range wantSIDs {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			t.Fatal(err)
		}
		actualSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || !actualSID.Equals(wantSIDs[index]) || ace.Mask != wantMasks[index] {
			t.Fatalf("ACE[%d] type=%d flags=%d mask=%#x sid=%s path=%s sddl=%s", index, ace.Header.AceType, ace.Header.AceFlags, ace.Mask, actualSID.String(), path, descriptor.String())
		}
	}
	t.Logf("protected ACL path=%s owner=%s group=%s sddl=%s", path, owner.String(), group.String(), descriptor.String())
}
