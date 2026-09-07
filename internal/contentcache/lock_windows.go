//go:build windows

package contentcache

import (
	"errors"
	"golang.org/x/sys/windows"
	"io/fs"
	"os"
	"strings"
	"syscall"
)

func lockFile(f *os.File, write bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if write {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{})
}
func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
func lockBusy(err error) bool       { return errors.Is(err, windows.ERROR_LOCK_VIOLATION) }
func canonicalPath(p string) string { return strings.ToLower(p) }
func isLink(info fs.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || (ok && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0)
}

func restoreMode(path string, mode fs.FileMode) error {
	if mode&0200 == 0 {
		return os.Chmod(path, mode)
	}
	return nil
}
