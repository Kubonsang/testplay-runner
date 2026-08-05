//go:build windows

package refsworkspace

import (
	"errors"
	"fmt"
	"testing"

	"golang.org/x/sys/windows"
)

func createPathReparseFixture(t *testing.T, target, link string) {
	t.Helper()
	junction := nativeJunctioner{}
	if err := junction.Create(target, link); err != nil {
		t.Fatalf("create non-privileged junction fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := junction.Remove(target, link); err != nil {
			t.Errorf("remove junction fixture: %v", err)
		}
	})
}

func symlinkFixtureUnavailableReason(err error) string {
	if !errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
		return ""
	}
	evidence, evidenceErr := currentWindowsTokenEvidence()
	if evidenceErr != nil {
		return fmt.Sprintf("symlink fixture unavailable: SeCreateSymbolicLinkPrivilege is not held; token evidence: %v", evidenceErr)
	}
	return fmt.Sprintf(
		"symlink fixture unavailable: SeCreateSymbolicLinkPrivilege present=%t enabled=%t (this skips fixture construction only; junction rejection remains covered)",
		evidence.SymlinkPrivilegePresent,
		evidence.SymlinkPrivilegeEnabled,
	)
}
