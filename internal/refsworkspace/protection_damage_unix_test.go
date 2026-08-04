//go:build !windows

package refsworkspace

import "os"

func damageDirectoryProtectionForTest(path string) error { return os.Chmod(path, 0700) }
func damageFileProtectionForTest(path string) error      { return os.Chmod(path, 0600) }
