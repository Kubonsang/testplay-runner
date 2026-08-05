//go:build !windows

package refsworkspace

import "os"

func damageFileWritableForTest(path string) error { return os.Chmod(path, 0600) }
func damageFileContentForTest(path string, content []byte) error {
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0400)
}
func damageDirectoryProtectionForTest(path string) error { return os.Chmod(path, 0700) }
func damageFileProtectionForTest(path string) error      { return os.Chmod(path, 0600) }
