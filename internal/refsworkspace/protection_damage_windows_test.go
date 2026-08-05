//go:build windows

package refsworkspace

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
)

func damageFileWritableForTest(path string) error {
	// The protected testplay identity correctly lacks FILE_WRITE_ATTRIBUTES.
	// Temporarily grant the current owner full access, change only the read-only
	// attribute, then restore the production DACL so the test isolates drift.
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	writable, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		protectedBaselineAccess(user.User.Sid, windows.GENERIC_ALL, windows.TRUSTEE_IS_USER),
	}, nil)
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		writable,
		nil,
	); err != nil {
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	acl, err := protectedBaselineACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
}

func damageFileContentForTest(path string, content []byte) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	writable, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		protectedBaselineAccess(user.User.Sid, windows.GENERIC_ALL, windows.TRUSTEE_IS_USER),
	}, nil)
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, writable, nil); err != nil {
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0400); err != nil {
		return err
	}
	acl, err := protectedBaselineACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func damageDirectoryProtectionForTest(path string) error {
	return runProtectionDamage("icacls.exe", path, "/inheritance:e")
}

func damageFileProtectionForTest(path string) error {
	return runProtectionDamage("icacls.exe", path, "/grant", "*S-1-5-32-545:(W)")
}

func runProtectionDamage(name string, arguments ...string) error {
	output, err := exec.Command(name, arguments...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("damage test ACL: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
