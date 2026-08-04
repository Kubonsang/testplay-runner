//go:build windows

package refsworkspace

import (
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
func protectBaselineTree(root string) error {
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			return os.Chmod(path, 0400)
		}
		return nil
	}); err != nil {
		return err
	}
	command := exec.Command("icacls.exe", root, "/inheritance:r", "/grant:r", "*S-1-5-18:(OI)(CI)(F)", "/grant:r", "*S-1-5-32-544:(OI)(CI)(F)", "/grant:r", "*S-1-5-32-545:(OI)(CI)(RX)", "/T", "/C", "/Q")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("icacls protect: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
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
