//go:build windows

package refsworkspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	command := exec.Command("icacls.exe", root, "/inheritance:r", "/grant:r", "*S-1-5-18:(OI)(CI)(F)", "/grant:r", "*S-1-5-32-544:(OI)(CI)(F)", "/grant:r", "*S-1-5-32-545:(OI)(CI)(RX)", "/T", "/C", "/Q")
	if output, err := command.CombinedOutput(); err != nil {
		return ProtectionEvidence{}, fmt.Errorf("icacls protect: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return inspectWindowsProtection(root)
}

func verifyBaselineProtection(root string, expected ProtectionEvidence) error {
	if expected.SchemaVersion != protectionSchemaVersion || expected.FilePolicy != "all-regular-files-read-only" || expected.DirectoryPolicy != "root-acl-non-inheriting-read-only-users" {
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
	result := ProtectionEvidence{SchemaVersion: protectionSchemaVersion, FilePolicy: "all-regular-files-read-only", DirectoryPolicy: "root-acl-non-inheriting-read-only-users"}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result.ProtectedDirectoryCount++
			return nil
		}
		if entry.Type().IsRegular() {
			result.RegularFileCount++
			attributes, err := fileAttributes(path)
			if err != nil {
				return err
			}
			if attributes&uint32(1) == 0 {
				return fmt.Errorf("writable file: %s", path)
			}
			result.ReadOnlyFileCount++
		}
		return nil
	})
	if err != nil {
		return ProtectionEvidence{}, err
	}
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "(Get-Acl -LiteralPath $env:TESTPLAY_REFS_PROTECTION_PATH).Sddl")
	command.Env = append(os.Environ(), "TESTPLAY_REFS_PROTECTION_PATH="+root)
	output, err := command.Output()
	if err != nil {
		return ProtectionEvidence{}, fmt.Errorf("read baseline ACL: %w", err)
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(string(output))))
	result.RootDescriptorSHA256 = hex.EncodeToString(digest[:])
	return result, nil
}

func makeWritableTree(root string) error {
	command := exec.Command("icacls.exe", root, "/inheritance:e", "/reset", "/T", "/C", "/Q")
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
