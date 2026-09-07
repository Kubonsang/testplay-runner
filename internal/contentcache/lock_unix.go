//go:build !windows

package contentcache

import (
	"errors"
	"golang.org/x/sys/unix"
	"io/fs"
	"os"
)

func lockFile(f *os.File, write bool) error {
	mode := unix.LOCK_SH
	if write {
		mode = unix.LOCK_EX
	}
	return unix.Flock(int(f.Fd()), mode|unix.LOCK_NB)
}
func unlockFile(f *os.File) error   { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
func lockBusy(err error) bool       { return errors.Is(err, unix.EWOULDBLOCK) }
func canonicalPath(p string) string { return p }
func isLink(info fs.FileInfo) bool  { return info.Mode()&os.ModeSymlink != 0 }

func restoreMode(path string, mode fs.FileMode) error { return os.Chmod(path, mode) }
