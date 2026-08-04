//go:build windows

package refsworkspace

import (
	"fmt"
	"os/exec"
	"strings"
)

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
