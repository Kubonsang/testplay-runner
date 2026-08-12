package vhdxworkspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
)

const completeContents = "testplay-vhdx-parent-v2\n"

type Store struct {
	paths Paths
	now   func() time.Time
}

type ParentResolution struct {
	Status   string
	Path     string
	Metadata *ParentMetadata
	Reason   string
}

type PendingParent struct {
	TransactionID  string           `json:"transactionId"`
	UserSID        string           `json:"userSid"`
	OwnershipToken string           `json:"ownershipToken"`
	Key            CompatibilityKey `json:"compatibilityKey"`
	Source         SourceSnapshot   `json:"sourceSnapshot"`
	StagingPath    string           `json:"stagingPath"`
	MountPath      string           `json:"mountPath"`
	CreatedAt      time.Time        `json:"createdAt"`
	UpdatedAt      time.Time        `json:"updatedAt"`
	ClientPID      int              `json:"clientPid,omitempty"`
}

func NewStore(root, userSID string) (*Store, error) {
	paths, err := NewPaths(root, userSID)
	if err != nil {
		return nil, err
	}
	return &Store{paths: paths, now: time.Now}, nil
}

func (s *Store) Paths() Paths { return s.paths }

func (s *Store) EnsureLayout() error {
	for _, path := range []string{s.paths.Root, s.paths.UserRoot, s.paths.Parents, s.paths.Pending, s.paths.Children, s.paths.Leases, s.paths.Retained, s.paths.Quarantine, s.paths.Receipts} {
		if err := ensureRealDirectory(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ResolveParent(key CompatibilityKey) (ParentResolution, error) {
	if key.SchemaVersion != ParentSchemaVersion || !digestPattern.MatchString(key.Digest) {
		return ParentResolution{}, fmt.Errorf("%w: invalid parent key", ErrInvalidInput)
	}
	dir, _ := s.paths.Parent(key.Digest)
	metadataPath := filepath.Join(dir, "metadata.json")
	completePath := filepath.Join(dir, "COMPLETE")
	parentPath := filepath.Join(dir, "parent.vhdx")
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return ParentResolution{Status: ParentStatusMissing, Path: parentPath}, nil
	} else if err != nil {
		return ParentResolution{}, err
	}
	if err := validateRegular(completePath); err != nil {
		return ParentResolution{Status: ParentStatusCorrupt, Path: parentPath, Reason: err.Error()}, nil
	}
	complete, err := os.ReadFile(completePath)
	if err != nil || string(complete) != completeContents {
		return ParentResolution{Status: ParentStatusCorrupt, Path: parentPath, Reason: "invalid COMPLETE marker"}, nil
	}
	var metadata ParentMetadata
	if err := readJSON(metadataPath, &metadata); err != nil {
		return ParentResolution{Status: ParentStatusCorrupt, Path: parentPath, Reason: err.Error()}, nil
	}
	if metadata.SchemaVersion != ParentSchemaVersion || metadata.Provider != Provider || metadata.CompatibilityKey.Digest != key.Digest || !metadata.Immutable || filepath.Clean(metadata.VHDXPath) != filepath.Clean(parentPath) {
		return ParentResolution{Status: ParentStatusCorrupt, Path: parentPath, Metadata: &metadata, Reason: "parent metadata identity mismatch"}, nil
	}
	if err := validateRegular(parentPath); err != nil {
		return ParentResolution{Status: ParentStatusCorrupt, Path: parentPath, Metadata: &metadata, Reason: err.Error()}, nil
	}
	return ParentResolution{Status: ParentStatusValid, Path: parentPath, Metadata: &metadata}, nil
}

func (s *Store) BeginParent(userSID string, key CompatibilityKey, source SourceSnapshot, mountPath string, clientPID int) (*PendingParent, error) {
	if err := s.EnsureLayout(); err != nil {
		return nil, err
	}
	resolved, err := s.ResolveParent(key)
	if err != nil {
		return nil, err
	}
	if resolved.Status == ParentStatusValid {
		return nil, ErrParentConflict
	}
	transactionID, err := randomID("parent")
	if err != nil {
		return nil, err
	}
	token, err := randomID("token")
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.paths.Pending, key.Digest)
	if err := os.Mkdir(dir, 0700); err != nil {
		if os.IsExist(err) {
			return nil, ErrParentConflict
		}
		return nil, err
	}
	pending := &PendingParent{
		TransactionID: transactionID, UserSID: userSID, OwnershipToken: token,
		Key: key, Source: source, StagingPath: filepath.Join(dir, "parent.staging.vhdx"),
		MountPath: mountPath, CreatedAt: s.now().UTC(), UpdatedAt: s.now().UTC(), ClientPID: clientPID,
	}
	if err := writeJSONExclusive(filepath.Join(dir, "pending.json"), pending); err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	return pending, nil
}

func (s *Store) RecoverPending(now time.Time, grace time.Duration, processAlive func(int) bool) (preserved, quarantined int, err error) {
	entries, err := os.ReadDir(s.paths.Pending)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !digestPattern.MatchString(entry.Name()) {
			continue
		}
		pending, readErr := s.ReadPending(entry.Name())
		if readErr != nil {
			preserved++
			continue
		}
		if pending.UpdatedAt.After(now.Add(-grace)) || (processAlive != nil && processAlive(pending.ClientPID)) {
			preserved++
			continue
		}
		dir := filepath.Dir(pending.StagingPath)
		if _, statErr := os.Stat(pending.StagingPath); os.IsNotExist(statErr) {
			if removeErr := os.Remove(filepath.Join(dir, "pending.json")); removeErr != nil {
				return preserved, quarantined, removeErr
			}
			if removeErr := os.Remove(dir); removeErr != nil {
				return preserved, quarantined, removeErr
			}
			continue
		}
		destination := filepath.Join(s.paths.Quarantine, pending.TransactionID+"-parent")
		if _, existsErr := os.Stat(destination); existsErr == nil {
			preserved++
			continue
		}
		if renameErr := os.Rename(dir, destination); renameErr != nil {
			preserved++
			continue
		}
		quarantined++
	}
	return preserved, quarantined, nil
}

func (s *Store) ReadPending(keyDigest string) (*PendingParent, error) {
	if !digestPattern.MatchString(keyDigest) {
		return nil, ErrInvalidInput
	}
	path := filepath.Join(s.paths.Pending, keyDigest, "pending.json")
	var pending PendingParent
	if err := readJSON(path, &pending); err != nil {
		return nil, err
	}
	if pending.Key.Digest != keyDigest || pending.StagingPath != filepath.Join(s.paths.Pending, keyDigest, "parent.staging.vhdx") {
		return nil, ErrOwnershipMismatch
	}
	return &pending, nil
}

// CommitParent publishes metadata and COMPLETE only after the detached staging
// VHDX has been verified by the platform backend. The VHDX rename occurs first;
// without COMPLETE a crash leaves an invalid, non-resolvable parent.
func (s *Store) CommitParent(pending *PendingParent, metadata ParentMetadata) (*ParentMetadata, error) {
	if pending == nil || metadata.OwnershipToken != pending.OwnershipToken || metadata.CompatibilityKey.Digest != pending.Key.Digest {
		return nil, ErrOwnershipMismatch
	}
	actual, err := s.ReadPending(pending.Key.Digest)
	if err != nil || actual.TransactionID != pending.TransactionID || actual.OwnershipToken != pending.OwnershipToken {
		return nil, ErrOwnershipMismatch
	}
	parentDir, _ := s.paths.Parent(pending.Key.Digest)
	if err := os.Mkdir(parentDir, 0700); err != nil {
		return nil, err
	}
	finalPath := filepath.Join(parentDir, "parent.vhdx")
	if _, err := os.Lstat(finalPath); err == nil {
		return nil, ErrParentConflict
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := atomicfile.Rename(pending.StagingPath, finalPath); err != nil {
		return nil, err
	}
	rollback := func(primary error) error {
		_ = os.Chmod(finalPath, 0600)
		_ = os.Remove(filepath.Join(parentDir, "COMPLETE"))
		_ = os.Remove(filepath.Join(parentDir, "metadata.json"))
		renameErr := os.Rename(finalPath, pending.StagingPath)
		removeErr := os.Remove(parentDir)
		return errors.Join(primary, renameErr, removeErr)
	}
	if err := os.Chmod(finalPath, 0400); err != nil {
		return nil, rollback(err)
	}
	metadata.SchemaVersion = ParentSchemaVersion
	metadata.Provider = Provider
	metadata.VHDXPath = finalPath
	metadata.Immutable = true
	metadata.CreatedAt = s.now().UTC()
	metadata.LastUsedAt = metadata.CreatedAt
	if err := writeJSONDurable(filepath.Join(parentDir, "metadata.json"), metadata); err != nil {
		return nil, rollback(err)
	}
	if err := atomicfile.WriteExclusiveDurable(filepath.Join(parentDir, "COMPLETE"), []byte(completeContents), 0600); err != nil {
		return nil, rollback(err)
	}
	_ = os.Remove(filepath.Join(filepath.Dir(pending.StagingPath), "pending.json"))
	_ = os.Remove(filepath.Dir(pending.StagingPath))
	return &metadata, nil
}

func (s *Store) AbortParent(pending *PendingParent) error {
	if pending == nil {
		return ErrInvalidInput
	}
	actual, err := s.ReadPending(pending.Key.Digest)
	if err != nil {
		return err
	}
	if actual.TransactionID != pending.TransactionID || actual.OwnershipToken != pending.OwnershipToken {
		return ErrOwnershipMismatch
	}
	dir := filepath.Dir(actual.StagingPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "pending.json" {
			return fmt.Errorf("%w: pending directory contains %s", ErrOwnershipMismatch, entry.Name())
		}
	}
	if err := os.Remove(filepath.Join(dir, "pending.json")); err != nil {
		return err
	}
	return os.Remove(dir)
}

func (s *Store) WriteLease(journal LeaseJournal) error {
	if err := s.EnsureLayout(); err != nil {
		return err
	}
	path, err := s.paths.Lease(journal.LeaseID)
	if err != nil {
		return err
	}
	journal.SchemaVersion = ProtocolSchemaVersion
	journal.UpdatedAt = s.now().UTC()
	if journal.CreatedAt.IsZero() {
		journal.CreatedAt = journal.UpdatedAt
	}
	return writeJSONDurable(path, journal)
}

func (s *Store) ReadLease(leaseID string) (*LeaseJournal, error) {
	path, err := s.paths.Lease(leaseID)
	if err != nil {
		return nil, err
	}
	var journal LeaseJournal
	if err := readJSON(path, &journal); err != nil {
		return nil, err
	}
	if journal.LeaseID != leaseID || journal.SchemaVersion != ProtocolSchemaVersion {
		return nil, ErrOwnershipMismatch
	}
	return &journal, nil
}

func (s *Store) ListLeases() ([]LeaseJournal, error) {
	entries, err := os.ReadDir(s.paths.Leases)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]LeaseJournal, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		leaseID := strings.TrimSuffix(entry.Name(), ".json")
		journal, readErr := s.ReadLease(leaseID)
		if readErr != nil {
			continue
		}
		result = append(result, *journal)
	}
	return result, nil
}

// ProtectedParentKeys returns every parent referenced by broker-owned state
// that GC must not remove. Namespace ambiguity is an error: skipping a corrupt
// journal or unknown quarantine entry could otherwise turn incomplete recovery
// evidence into an apparently unused parent.
func (s *Store) ProtectedParentKeys() (map[string]bool, error) {
	protected := map[string]bool{}

	leaseEntries, err := os.ReadDir(s.paths.Leases)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range leaseEntries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			return nil, fmt.Errorf("%w: unknown lease artifact %s", ErrOwnershipMismatch, entry.Name())
		}
		leaseID := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		journal, readErr := s.ReadLease(leaseID)
		if readErr != nil || !digestPattern.MatchString(journal.ParentKey) {
			return nil, errors.Join(readErr, fmt.Errorf("%w: invalid lease parent reference %s", ErrOwnershipMismatch, entry.Name()))
		}
		protected[journal.ParentKey] = true
	}

	pendingEntries, err := os.ReadDir(s.paths.Pending)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range pendingEntries {
		if !entry.IsDir() || !digestPattern.MatchString(entry.Name()) {
			return nil, fmt.Errorf("%w: unknown pending artifact %s", ErrOwnershipMismatch, entry.Name())
		}
		pending, readErr := s.ReadPending(entry.Name())
		if readErr != nil {
			return nil, errors.Join(readErr, fmt.Errorf("%w: invalid pending parent reference %s", ErrOwnershipMismatch, entry.Name()))
		}
		protected[pending.Key.Digest] = true
	}

	retainedEntries, err := os.ReadDir(s.paths.Retained)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range retainedEntries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			return nil, fmt.Errorf("%w: unknown retained artifact %s", ErrOwnershipMismatch, entry.Name())
		}
		runID := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		record, readErr := s.ReadRetained(runID)
		if readErr != nil || !digestPattern.MatchString(record.ParentKey) {
			return nil, errors.Join(readErr, fmt.Errorf("%w: invalid retained parent reference %s", ErrOwnershipMismatch, entry.Name()))
		}
		protected[record.ParentKey] = true
	}

	quarantineEntries, err := os.ReadDir(s.paths.Quarantine)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(quarantineEntries) != 0 {
		return nil, fmt.Errorf("%w: quarantine is non-empty", ErrOwnershipMismatch)
	}
	return protected, nil
}

func (s *Store) RemoveLease(journal LeaseJournal) error {
	actual, err := s.ReadLease(journal.LeaseID)
	if err != nil {
		return err
	}
	if actual.OwnershipToken != journal.OwnershipToken || actual.ChildPath != journal.ChildPath || actual.ParentKey != journal.ParentKey {
		return ErrOwnershipMismatch
	}
	path, _ := s.paths.Lease(journal.LeaseID)
	return os.Remove(path)
}

func (s *Store) WriteRetained(journal LeaseJournal) error {
	path, err := s.paths.RetainedRecord(journal.RunID)
	if err != nil {
		return err
	}
	record := RetainedRecord{SchemaVersion: ProtocolSchemaVersion, RunID: journal.RunID, LeaseID: journal.LeaseID, OwnershipToken: journal.OwnershipToken, ParentKey: journal.ParentKey, ChildPath: journal.ChildPath, CreatedAt: s.now().UTC()}
	return writeJSONExclusive(path, record)
}

func (s *Store) ReadRetained(runID string) (*RetainedRecord, error) {
	path, err := s.paths.RetainedRecord(runID)
	if err != nil {
		return nil, err
	}
	var record RetainedRecord
	if err := readJSON(path, &record); err != nil {
		return nil, err
	}
	if record.SchemaVersion != ProtocolSchemaVersion || record.RunID != runID {
		return nil, ErrOwnershipMismatch
	}
	return &record, nil
}

func (s *Store) RemoveRetained(record RetainedRecord) error {
	actual, err := s.ReadRetained(record.RunID)
	if err != nil {
		return err
	}
	if actual.LeaseID != record.LeaseID || actual.OwnershipToken != record.OwnershipToken || actual.ChildPath != record.ChildPath {
		return ErrOwnershipMismatch
	}
	path, _ := s.paths.RetainedRecord(record.RunID)
	return os.Remove(path)
}

func (s *Store) ListParents() ([]ParentMetadata, error) {
	entries, err := os.ReadDir(s.paths.Parents)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []ParentMetadata
	for _, entry := range entries {
		if !entry.IsDir() || !digestPattern.MatchString(entry.Name()) {
			continue
		}
		var metadata ParentMetadata
		if readJSON(filepath.Join(s.paths.Parents, entry.Name(), "metadata.json"), &metadata) == nil {
			result = append(result, metadata)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastUsedAt.Before(result[j].LastUsedAt) })
	return result, nil
}

func (s *Store) TouchParent(metadata ParentMetadata) error {
	resolved, err := s.ResolveParent(metadata.CompatibilityKey)
	if err != nil || resolved.Status != ParentStatusValid || resolved.Metadata == nil {
		return errors.Join(err, ErrOwnershipMismatch)
	}
	if resolved.Metadata.OwnershipToken != metadata.OwnershipToken || resolved.Metadata.FileIdentity != metadata.FileIdentity {
		return ErrOwnershipMismatch
	}
	resolved.Metadata.LastUsedAt = s.now().UTC()
	dir, _ := s.paths.Parent(metadata.CompatibilityKey.Digest)
	return writeJSONDurable(filepath.Join(dir, "metadata.json"), resolved.Metadata)
}

func (s *Store) Status(hostFree, quota, floor int64) (Status, error) {
	status := Status{Provider: Provider, UserSID: filepath.Base(s.paths.UserRoot)}
	status.ParentCount = countDirectories(s.paths.Parents)
	status.PendingCount = countDirectories(s.paths.Pending)
	status.QuarantineCount = countEntries(s.paths.Quarantine)
	if parents, listErr := s.ListParents(); listErr == nil {
		for _, parent := range parents {
			if bytes, measureErr := fileAllocatedBytes(parent.VHDXPath); measureErr == nil {
				status.ParentAllocatedBytes += bytes
			}
			if info, measureErr := os.Stat(parent.VHDXPath); measureErr == nil {
				status.ParentLogicalBytes += info.Size()
			}
		}
	}
	entries, _ := os.ReadDir(s.paths.Leases)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var journal LeaseJournal
		if readJSON(filepath.Join(s.paths.Leases, entry.Name()), &journal) != nil {
			status.QuarantineCount++
			continue
		}
		if journal.State == "quarantined" {
			status.QuarantineCount++
			if bytes, measureErr := fileAllocatedBytes(journal.ChildPath); measureErr == nil {
				status.QuarantineAllocatedBytes += bytes
			}
			if info, measureErr := os.Stat(journal.ChildPath); measureErr == nil {
				status.QuarantineLogicalBytes += info.Size()
			}
		} else if journal.Retained {
			status.RetainedChildCount++
			if bytes, measureErr := fileAllocatedBytes(journal.ChildPath); measureErr == nil {
				status.RetainedChildAllocatedBytes += bytes
			}
			if info, measureErr := os.Stat(journal.ChildPath); measureErr == nil {
				status.RetainedChildLogicalBytes += info.Size()
			}
		} else if journal.State != "released" {
			status.ActiveChildCount++
			if bytes, measureErr := fileAllocatedBytes(journal.ChildPath); measureErr == nil {
				status.ActiveChildAllocatedBytes += bytes
			}
			if info, measureErr := os.Stat(journal.ChildPath); measureErr == nil {
				status.ActiveChildLogicalBytes += info.Size()
			}
		}
	}
	quarantineAllocated, _ := allocatedVHDXBytes(s.paths.Quarantine)
	quarantineLogical, _ := logicalVHDXBytes(s.paths.Quarantine)
	status.QuarantineAllocatedBytes += quarantineAllocated
	status.QuarantineLogicalBytes += quarantineLogical
	status.ManualRecoveryRequired = status.QuarantineCount > 0
	allocated, err := allocatedVHDXBytes(s.paths.UserRoot)
	if err != nil && !os.IsNotExist(err) {
		return status, err
	}
	status.Capacity = Capacity{QuotaBytes: quota, AllocatedBytes: allocated, HostFreeBytes: hostFree, HostFloorBytes: floor}
	return status, nil
}

func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			if parentErr := os.MkdirAll(filepath.Dir(path), 0700); parentErr != nil {
				return parentErr
			}
			return os.Mkdir(path, 0700)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("directory is not a real directory: %s", path)
	}
	return nil
}

func validateRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a regular file: %s", path)
	}
	return nil
}

func writeJSONDurable(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteDurable(path, append(data, '\n'), 0600)
}

func writeJSONExclusive(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteExclusiveDurable(path, append(data, '\n'), 0600)
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}

func countDirectories(path string) int {
	entries, _ := os.ReadDir(path)
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			count++
		}
	}
	return count
}

func countEntries(path string) int { entries, _ := os.ReadDir(path); return len(entries) }

func allocatedVHDXBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".vhdx") {
			return nil
		}
		allocated, err := fileAllocatedBytes(path)
		if err != nil {
			return err
		}
		total += allocated
		return nil
	})
	return total, err
}

func logicalVHDXBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".vhdx") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func (s *Store) RemoveParentIfUnused(key string, active map[string]bool) (int64, error) {
	if active[key] {
		return 0, ErrParentConflict
	}
	dir, err := s.paths.Parent(key)
	if err != nil {
		return 0, err
	}
	resolution, err := s.ResolveParent(CompatibilityKey{SchemaVersion: ParentSchemaVersion, Digest: key})
	if err != nil || resolution.Status != ParentStatusValid || resolution.Metadata == nil {
		return 0, errors.Join(err, ErrOwnershipMismatch)
	}
	allocated := resolution.Metadata.AllocatedBytes
	if err := os.RemoveAll(dir); err != nil {
		return 0, err
	}
	return allocated, nil
}
