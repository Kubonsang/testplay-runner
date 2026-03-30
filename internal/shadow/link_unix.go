//go:build !windows

package shadow

import "os"

// linkPackages creates a symlink at dst pointing to src.
// Used for Packages/ — write probability is negligible, so a symlink
// is safe and avoids copying potentially large package caches.
func linkPackages(src, dst string) error {
	return os.Symlink(src, dst)
}
