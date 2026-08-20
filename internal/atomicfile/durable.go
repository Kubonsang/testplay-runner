package atomicfile

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteDurable writes data through a uniquely named temporary file, flushes
// the file before the atomic rename, and verifies the renamed bytes. The
// caller still owns any filesystem or volume-level durability barrier needed
// for directory-entry deletion and transaction commit.
func WriteDurable(path string, data []byte, perm os.FileMode) (returnErr error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			returnErr = errorsJoin(returnErr, temporary.Close())
		}
		if returnErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(perm); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := Rename(temporaryPath, path); err != nil {
		return err
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, data) {
		return fmt.Errorf("durable atomic write read-back mismatch: wrote=%d read=%d", len(data), len(actual))
	}
	return nil
}

// WriteExclusiveDurable creates path exactly once, flushes it, and verifies
// its contents. Existing paths are never reused or replaced.
func WriteExclusiveDurable(path string, data []byte, perm os.FileMode) (returnErr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			returnErr = errorsJoin(returnErr, file.Close())
		}
		if returnErr != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	actual, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, data) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func errorsJoin(primary, cleanup error) error {
	if primary != nil {
		if cleanup != nil {
			return fmt.Errorf("%w; cleanup: %v", primary, cleanup)
		}
		return primary
	}
	return cleanup
}
