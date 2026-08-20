//go:build !windows

package refsworkspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	var records []string
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
			result.DirectoryCount++
			result.ProtectedDirectoryCount++
			result.NonInheritingEntryCount++
		} else if entry.Type().IsRegular() {
			result.RegularFileCount++
			if info.Mode().Perm()&0222 != 0 {
				return fmt.Errorf("writable file: %s", path)
			}
			result.ReadOnlyFileCount++
			result.NonInheritingEntryCount++
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		kind := "O"
		if entry.IsDir() {
			kind = "D"
		} else if entry.Type().IsRegular() {
			kind = "F"
		}
		record := fmt.Sprintf("%s|%s|mode=%#o|readonly=%t|noninheriting=true", kind, filepath.ToSlash(relative), info.Mode().Perm(), info.Mode().Perm()&0222 == 0)
		records = append(records, record)
		if path == root {
			result.RootDescriptorSHA256 = protectionStringSHA256(strings.TrimSpace(record))
		}
		return nil
	})
	result.TreeDescriptorSHA256 = protectionRecordsSHA256(records)
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
