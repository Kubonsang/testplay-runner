//go:build windows

package shadow

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// linkPackages creates a directory link at dst pointing to src.
//
// Strategy:
//  1. Try os.Symlink first — works when Developer Mode is enabled or the
//     process has SeCreateSymbolicLinkPrivilege (e.g. GitHub Actions runners).
//  2. Fall back to mklink /J (directory junction) which does not require
//     elevated privileges. Paths are resolved via filepath.EvalSymlinks to
//     expand 8.3 short names (e.g. RUNNER~1) that mklink /J may reject.
func linkPackages(src, dst string) error {
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	// Resolve 8.3 short names in src (must exist).
	if long, err := filepath.EvalSymlinks(src); err == nil {
		src = long
	}
	// Resolve 8.3 short names in dst's parent (dst itself does not exist yet).
	if long, err := filepath.EvalSymlinks(filepath.Dir(dst)); err == nil {
		dst = filepath.Join(long, filepath.Base(dst))
	}
	cmd := exec.Command("cmd", "/c", fmt.Sprintf(`mklink /J "%s" "%s"`, dst, src))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mklink /J %q %q: %w: %s", dst, src, err, strings.TrimSpace(string(out)))
	}
	return nil
}
