//go:build windows

package shadow

import (
	"fmt"
	"os/exec"
	"strings"
)

// linkPackages creates a Directory Junction at dst pointing to src.
// Junctions do not require elevated privileges on Windows (unlike symlinks).
//
// The mklink command is passed as a single string argument to "cmd /c" rather
// than as separate tokens. When Go passes individual arguments containing
// spaces, cmd.exe strips the quotes it receives and misparses the paths.
// Embedding the full command in one quoted string bypasses that behaviour.
//
// CombinedOutput is used instead of Run so that cmd.exe's error message
// (written to stdout by mklink) is included in the returned error, making
// Windows-specific failures diagnosable without a separate log capture.
func linkPackages(src, dst string) error {
	cmd := exec.Command("cmd", "/c", fmt.Sprintf(`mklink /J "%s" "%s"`, dst, src))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mklink /J %q %q: %w: %s", dst, src, err, strings.TrimSpace(string(out)))
	}
	return nil
}
