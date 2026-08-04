//go:build windows

package refsworkspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type nativeJunctioner struct{}

func NewNativeJunctioner() Junctioner { return nativeJunctioner{} }

const createJunctionScript = `
$ErrorActionPreference = 'Stop'
$target = $env:TESTPLAY_REFS_JUNCTION_TARGET
$junction = $env:TESTPLAY_REFS_JUNCTION_PATH
if (Test-Path -LiteralPath $junction) { throw "junction already exists: $junction" }
New-Item -ItemType Junction -Path $junction -Target $target -ErrorAction Stop | Out-Null
`

func (nativeJunctioner) Create(target, junction string) error {
	originalTarget, originalJunction := target, junction
	targetResolved, err := canonicalExistingPath(target)
	if err != nil {
		return newError(CodeJunctionFailed, "canonical-junction-target", originalTarget, err)
	}
	parent, err := canonicalExistingPath(filepath.Dir(junction))
	if err != nil {
		return newError(CodeJunctionFailed, "canonical-junction-parent", originalJunction, err)
	}
	junction = filepath.Join(parent, filepath.Base(junction))
	if _, err := os.Lstat(junction); !os.IsNotExist(err) {
		return fmt.Errorf("junction path already exists")
	}
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", createJunctionScript)
	command.Env = append(os.Environ(), "TESTPLAY_REFS_JUNCTION_TARGET="+targetResolved, "TESTPLAY_REFS_JUNCTION_PATH="+junction)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("create junction: %w: %s", err, strings.TrimSpace(string(output)))
	}
	resolved, err := filepath.EvalSymlinks(junction)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), targetResolved) {
		return fmt.Errorf("junction identity mismatch: resolved=%q expected=%q: %w", resolved, targetResolved, err)
	}
	return nil
}

func (nativeJunctioner) Remove(target, junction string) error {
	originalTarget := target
	info, err := os.Lstat(junction)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("path is not a junction/reparse point")
	}
	resolved, err := filepath.EvalSymlinks(junction)
	if err != nil {
		return err
	}
	expected, err := canonicalExistingPath(target)
	if err != nil {
		return newError(CodeJunctionFailed, "canonical-junction-target", originalTarget, err)
	}
	if !strings.EqualFold(filepath.Clean(resolved), expected) {
		return fmt.Errorf("junction target changed: resolved=%q expected=%q", resolved, expected)
	}
	return os.Remove(junction)
}
