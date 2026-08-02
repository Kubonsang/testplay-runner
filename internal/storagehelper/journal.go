package storagehelper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
)

type Journal struct {
	SchemaVersion        int       `json:"schemaVersion"`
	LeaseID              string    `json:"leaseId"`
	RequestID            string    `json:"requestId"`
	State                string    `json:"state"`
	HelperPID            int       `json:"helperPid"`
	ParentPath           string    `json:"parentPath"`
	ChildPath            string    `json:"childPath"`
	PhysicalPath         string    `json:"physicalPath,omitempty"`
	VolumeGUIDPath       string    `json:"volumeGuidPath,omitempty"`
	MountPath            string    `json:"mountPath"`
	DeleteChildOnRelease bool      `json:"deleteChildOnRelease"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type journalWriter func(string, []byte, os.FileMode) error
type JournalStore struct{ write journalWriter }

func NewJournalStore() *JournalStore { return &JournalStore{write: writeAtomicSynced} }

func (s *JournalStore) Write(storeRoot string, journal Journal) error {
	dir := filepath.Join(storeRoot, "leases")
	if err := ensureJournalDirectory(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, journal.LeaseID+".json")
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return helperError(CodeJournalWriteFailed, "encode-journal", path, err)
	}
	if err := s.write(path, data, 0600); err != nil {
		return helperError(CodeJournalWriteFailed, "write-journal", path, err)
	}
	return nil
}

func (s *JournalStore) FindOrphans(storeRoot string) ([]Journal, error) {
	dir := filepath.Join(storeRoot, "leases")
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return nil, nil
	}
	if err := ensureJournalDirectory(dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, helperError(CodeOrphanFound, "read-journals", dir, err)
	}
	var values []Journal
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, helperError(CodeOrphanFound, "read-journal", path, err)
		}
		var journal Journal
		if err := json.Unmarshal(data, &journal); err != nil {
			return nil, helperError(CodeOrphanFound, "decode-journal", path, err)
		}
		if journal.State != StateReleased {
			values = append(values, journal)
		}
	}
	return values, nil
}

func writeAtomicSynced(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".journal-*.tmp")
	if err != nil {
		return err
	}
	temp := file.Name()
	cleanup := func() { _ = file.Close(); _ = os.Remove(temp) }
	if err := file.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := atomicfile.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func ensureJournalDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err := os.Mkdir(dir, 0700); err != nil {
			return helperError(CodeJournalWriteFailed, "create-journal-directory", dir, err)
		}
		return nil
	}
	if err != nil {
		return helperError(CodeJournalWriteFailed, "stat-journal-directory", dir, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return helperError(CodeJournalWriteFailed, "validate-journal-directory", dir, fmt.Errorf("journal directory must be a real directory"))
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return helperError(CodeJournalWriteFailed, "resolve-journal-directory", dir, err)
	}
	if !samePath(dir, resolved) {
		return helperError(CodeJournalWriteFailed, "validate-journal-directory", dir, fmt.Errorf("journal directory is a symlink or reparse point"))
	}
	return nil
}

func journalPath(storeRoot, leaseID string) string {
	return filepath.Join(storeRoot, "leases", fmt.Sprintf("%s.json", leaseID))
}
