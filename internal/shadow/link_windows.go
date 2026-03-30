//go:build windows

package shadow

import (
	"fmt"
	"os/exec"
)

// linkPackages creates a Directory Junction at dst pointing to src.
// Junctions do not require elevated privileges on Windows (unlike symlinks).
//
// The mklink command is passed as a single string argument to "cmd /c" rather
// than as separate tokens. When Go passes individual arguments containing
// spaces, cmd.exe strips the quotes it receives and misparses the paths.
// Embedding the full command in one quoted string bypasses that behaviour.
func linkPackages(src, dst string) error {
	return exec.Command("cmd", "/c", fmt.Sprintf(`mklink /J "%s" "%s"`, dst, src)).Run()
}
