package refsworkspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
)

func devDrivePointer(value DevDriveEvidence) *DevDriveEvidence { return &value }
func volumePointer(value VolumeInfo) *VolumeInfo               { return &value }

func marshalJSONLine(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func createPendingJSONExclusive(path string, value any, mode os.FileMode) error {
	data, err := marshalJSONLine(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	writeErr := writeAndSync(file, data)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

// writeJSONDurableAtomic flushes the temporary file, atomically publishes it,
// then flushes and reads the final name. The mounted-volume flush and the
// detach/reattach proof in Setup are the stronger transaction barriers.
func writeJSONDurableAtomic(path string, value any, mode os.FileMode) error {
	data, err := marshalJSONLine(value)
	if err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	tmp := path + "." + token[:12] + ".tmp"
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	writeErr := writeAndSync(file, data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := atomicfile.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	final, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	syncErr := final.Sync()
	readBack, readErr := io.ReadAll(final)
	closeErr = final.Close()
	if err := errors.Join(syncErr, readErr, closeErr); err != nil {
		return err
	}
	if string(readBack) != string(data) {
		return fmt.Errorf("durable JSON read-back differs from written bytes")
	}
	return nil
}

func writeAndSync(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func commitPendingOwner(paths Paths) (bool, error) {
	if _, err := os.Lstat(paths.Owner); err == nil {
		return false, fmt.Errorf("authoritative owner already exists")
	} else if !os.IsNotExist(err) {
		return false, err
	}
	// A hard link gives us create-if-absent semantics on the same host NTFS
	// volume. Removing the pending name leaves the exact flushed file committed.
	if err := os.Link(paths.PendingOwner, paths.Owner); err != nil {
		return false, err
	}
	if err := os.Remove(paths.PendingOwner); err != nil {
		rollbackErr := os.Remove(paths.Owner)
		return rollbackErr != nil, errors.Join(err, rollbackErr)
	}
	owner, err := os.OpenFile(paths.Owner, os.O_RDWR, 0)
	if err != nil {
		return true, err
	}
	err = errors.Join(owner.Sync(), owner.Close())
	return true, err
}

func rejectPendingOwner(paths Paths) error {
	info, err := os.Lstat(paths.PendingOwner)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return newError(CodePoolCorrupt, "inspect-pending-owner", paths.PendingOwner, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return newError(CodeOwnershipMismatch, "inspect-pending-owner", paths.PendingOwner, fmt.Errorf("pending owner is not a regular file"))
	}
	return newError(CodeIncompleteSetup, "detect-incomplete-setup", paths.PendingOwner, fmt.Errorf("pending ownership record exists"))
}

func removeExactPendingOwner(path, token, vhdxPath string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var metadata PoolMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return err
	}
	if metadata.OwnershipToken != token || !strings.EqualFold(filepath.Clean(metadata.VHDXPath), filepath.Clean(vhdxPath)) {
		return fmt.Errorf("pending ownership identity changed")
	}
	return os.Remove(path)
}

func verifyRequiredPoolLayout(paths Paths) error {
	for _, path := range []string{paths.PoolRoot, paths.Baselines, paths.Workers, paths.Leases, paths.Quarantine} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("required managed path is not a real directory: %s", path)
		}
		reparse, err := inspectPathReparse(path)
		if err != nil || reparse {
			return errors.Join(err, fmt.Errorf("required managed path is a reparse point: %s", path))
		}
	}
	info, err := os.Lstat(paths.PoolFile)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("pool metadata is not a regular file")
	}
	reparse, err := inspectPathReparse(paths.PoolFile)
	if err != nil || reparse {
		return errors.Join(err, fmt.Errorf("pool metadata is a reparse point"))
	}
	return nil
}

func samePersistentVolume(initial, current VolumeInfo) bool {
	return strings.EqualFold(strings.TrimSuffix(initial.VolumeGUIDPath, `\`), strings.TrimSuffix(current.VolumeGUIDPath, `\`)) &&
		strings.EqualFold(initial.Filesystem, current.Filesystem) &&
		initial.ClusterSize == current.ClusterSize && current.SupportsBlockCloning
}
