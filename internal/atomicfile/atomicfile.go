// Package atomicfile provides crash-safe, Windows-tolerant atomic file writes
// shared across the codebase (testplay-status.json, run results, artifacts,
// list cache, and the bridge protocol). Centralizing the tmp+rename discipline
// here ensures the Windows sharing-violation retry applies uniformly instead of
// living in only one of the previously-duplicated copies.
package atomicfile

import (
	"os"
	"time"
)

const (
	renameAttempts = 100 // ~1s total at renameBackoff spacing
	renameBackoff  = 10 * time.Millisecond
)

// Rename renames oldpath to newpath, retrying transient failures. On Windows,
// os.Rename's MoveFileEx(REPLACE_EXISTING) fails with a sharing violation
// ("Access is denied") while a concurrent reader has the destination briefly
// open (Go does not open files with FILE_SHARE_DELETE). The reader's handle is
// short-lived, so a bounded backoff resolves the race. On POSIX, rename is
// atomic and succeeds on the first attempt.
func Rename(oldpath, newpath string) error {
	var err error
	for i := 0; i < renameAttempts; i++ {
		if err = os.Rename(oldpath, newpath); err == nil {
			return nil
		}
		if i < renameAttempts-1 {
			time.Sleep(renameBackoff)
		}
	}
	return err
}

// Write writes data to path via a temp file (path+".tmp") + atomic Rename,
// removing the temp file if the rename ultimately fails. The parent directory
// must already exist.
func Write(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
