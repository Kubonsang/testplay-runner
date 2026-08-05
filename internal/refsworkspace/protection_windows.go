//go:build windows

package refsworkspace

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// protectBaselineTree applies read-only attributes and a non-inheriting ACL.
// Administrators can still recover the pool; immutability is enforced jointly
// by this ACL, integrity verification, and active-use markers.
func protectBaselineTree(root string) (ProtectionEvidence, error) {
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			return os.Chmod(path, 0400)
		}
		return nil
	}); err != nil {
		return ProtectionEvidence{}, err
	}
	acl, err := protectedBaselineACL()
	if err != nil {
		return ProtectionEvidence{}, err
	}
	// Every entry receives a direct, protected DACL. Applying only inheritable
	// (OI)(CI) ACEs recursively leaves regular files with an empty DACL after
	// inheritance is removed, which also makes the baseline unreadable to the
	// testplay identity. Direct ACEs preserve read/execute access without
	// granting write, delete, or delete-child rights.
	if err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := windows.SetNamedSecurityInfo(
			path,
			windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
			nil,
			nil,
			acl,
			nil,
		); err != nil {
			return fmt.Errorf("protect DACL %s: %w", path, err)
		}
		return nil
	}); err != nil {
		return ProtectionEvidence{}, err
	}
	return inspectWindowsProtection(root)
}

func protectedBaselineACL() (*windows.ACL, error) {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, fmt.Errorf("create SYSTEM SID: %w", err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, fmt.Errorf("create Administrators SID: %w", err)
	}
	users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		return nil, fmt.Errorf("create Users SID: %w", err)
	}
	entries := []windows.EXPLICIT_ACCESS{
		protectedBaselineAccess(system, windows.GENERIC_ALL, windows.TRUSTEE_IS_USER),
		protectedBaselineAccess(administrators, windows.GENERIC_ALL, windows.TRUSTEE_IS_GROUP),
		protectedBaselineAccess(users, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_EXECUTE, windows.TRUSTEE_IS_GROUP),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return nil, fmt.Errorf("build protected baseline DACL: %w", err)
	}
	return acl, nil
}

func protectedBaselineAccess(sid *windows.SID, permissions windows.ACCESS_MASK, trusteeType windows.TRUSTEE_TYPE) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: permissions,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func verifyBaselineProtection(root string, expected ProtectionEvidence) error {
	if expected.SchemaVersion != protectionSchemaVersion || expected.FilePolicy != "all-regular-files-read-only" || expected.DirectoryPolicy != "recursive-acl-non-inheriting-read-only-users" {
		return fmt.Errorf("missing or unsupported protection policy")
	}
	actual, err := inspectWindowsProtection(root)
	if err != nil {
		return err
	}
	if actual != expected || actual.RegularFileCount != actual.ReadOnlyFileCount {
		return fmt.Errorf("protection evidence changed")
	}
	return nil
}

func inspectWindowsProtection(root string) (ProtectionEvidence, error) {
	result := ProtectionEvidence{SchemaVersion: protectionSchemaVersion, FilePolicy: "all-regular-files-read-only", DirectoryPolicy: "recursive-acl-non-inheriting-read-only-users"}
	var records []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		kind := "O"
		readOnly := false
		if entry.IsDir() {
			kind = "D"
			result.DirectoryCount++
			result.ProtectedDirectoryCount++
		}
		if entry.Type().IsRegular() {
			kind = "F"
			result.RegularFileCount++
			attributes, err := fileAttributes(path)
			if err != nil {
				return err
			}
			if attributes&uint32(1) == 0 {
				return fmt.Errorf("writable file: %s", path)
			}
			result.ReadOnlyFileCount++
			readOnly = true
		}
		descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			return fmt.Errorf("read security descriptor %s: %w", path, err)
		}
		control, _, err := descriptor.Control()
		if err != nil {
			return fmt.Errorf("read security descriptor control %s: %w", path, err)
		}
		nonInheriting := control&windows.SE_DACL_PROTECTED != 0
		if !nonInheriting {
			return fmt.Errorf("inheriting ACL: %s", path)
		}
		result.NonInheritingEntryCount++
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sddl := descriptor.String()
		if sddl == "" {
			return fmt.Errorf("empty security descriptor: %s", path)
		}
		record := fmt.Sprintf("%s|%s|readonly=%t|noninheriting=%t|%s", kind, filepath.ToSlash(relative), readOnly, nonInheriting, sddl)
		records = append(records, record)
		if path == root {
			result.RootDescriptorSHA256 = protectionStringSHA256(sddl)
		}
		return nil
	})
	if err != nil {
		return ProtectionEvidence{}, err
	}
	result.TreeDescriptorSHA256 = protectionRecordsSHA256(records)
	return result, nil
}

func makeWritableTree(root string) error {
	// /reset already replaces each DACL with the inherited parent policy and
	// clears DACL protection. Combining /reset and /inheritance:e in one icacls
	// invocation is rejected by Windows with ERROR_INVALID_PARAMETER.
	command := exec.Command("icacls.exe", root, "/reset", "/T", "/C", "/Q")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("icacls reset: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			return os.Chmod(path, 0600)
		}
		return nil
	})
}
