//go:build !windows

package refsworkspace

import (
	"os"
	"testing"
)

func createPathReparseFixture(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
}

func symlinkFixtureUnavailableReason(error) string { return "" }
