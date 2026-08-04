//go:build !windows

package refsworkspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func protectBaselineTree(root string) (ProtectionEvidence, error) {
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0500)
		}
		if entry.Type().IsRegular() {
			return os.Chmod(path, 0400)
		}
		return nil
	}); err != nil {
		return ProtectionEvidence{}, err
	}
	return inspectUnixProtection(root)
}

func verifyBaselineProtection(root string, expected ProtectionEvidence) error {
	if expected.SchemaVersion != protectionSchemaVersion || expected.FilePolicy != "all-regular-files-read-only" || expected.DirectoryPolicy != "all-directories-non-writable" {
		return fmt.Errorf("missing or unsupported protection policy")
	}
	actual, err := inspectUnixProtection(root)
	if err != nil {
		return err
	}
	if actual != expected || actual.RegularFileCount != actual.ReadOnlyFileCount {
		return fmt.Errorf("protection evidence changed")
	}
	return nil
}

func inspectUnixProtection(root string) (ProtectionEvidence, error) {
	result := ProtectionEvidence{SchemaVersion: protectionSchemaVersion, FilePolicy: "all-regular-files-read-only", DirectoryPolicy: "all-directories-non-writable"}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if info.Mode().Perm()&0222 != 0 {
				return fmt.Errorf("writable directory: %s", path)
			}
			result.ProtectedDirectoryCount++
		} else if entry.Type().IsRegular() {
			result.RegularFileCount++
			if info.Mode().Perm()&0222 != 0 {
				return fmt.Errorf("writable file: %s", path)
			}
			result.ReadOnlyFileCount++
		}
		if path == root {
			digest := sha256.Sum256([]byte(fmt.Sprintf("mode=%#o", info.Mode().Perm())))
			result.RootDescriptorSHA256 = hex.EncodeToString(digest[:])
		}
		return nil
	})
	return result, err
}

func makeWritableTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0700)
		}
		if entry.Type().IsRegular() {
			return os.Chmod(path, 0600)
		}
		return nil
	})
}
