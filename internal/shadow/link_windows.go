//go:build windows

package shadow

import "os/exec"

// linkPackages creates a Directory Junction at dst pointing to src.
// Junctions do not require elevated privileges on Windows (unlike symlinks).
func linkPackages(src, dst string) error {
	return exec.Command("cmd", "/c", "mklink", "/J", dst, src).Run()
}
