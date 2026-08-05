package refsworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

type BaselineState string

const (
	BaselineMissing   BaselineState = "missing"
	BaselineValid     BaselineState = "valid"
	BaselineCorrupt   BaselineState = "corrupt"
	BaselineStale     BaselineState = "stale"
	BaselineAvailable BaselineState = "available"
	BaselineInUse     BaselineState = "in-use"
	BaselineMutating  BaselineState = "mutating"
)

const (
	baselineMetadataFile = "metadata.json"
	baselineCompleteFile = "COMPLETE"
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type BaselineMetadata struct {
	SchemaVersion  int                `json:"schemaVersion"`
	Key            CompatibilityKey   `json:"key"`
	CreatedAt      time.Time          `json:"createdAt"`
	Library        TreeInfo           `json:"library"`
	OwnershipToken string             `json:"ownershipToken"`
	Protection     ProtectionEvidence `json:"protection"`
}

type Baseline struct {
	Path        string           `json:"path"`
	LibraryPath string           `json:"libraryPath"`
	Metadata    BaselineMetadata `json:"metadata"`
}

type BaselineResolution struct {
	State             BaselineState `json:"state"`
	Baseline          *Baseline     `json:"baseline,omitempty"`
	Reason            string        `json:"reason,omitempty"`
	CoordinationState BaselineState `json:"coordinationState,omitempty"`
}

type BaselineMetrics struct {
	BaselineKeyMs          int64 `json:"baselineKeyMs,omitempty"`
	BaselineCreationMs     int64 `json:"baselineCreationMs,omitempty"`
	BaselineVerifyMs       int64 `json:"baselineVerifyMs,omitempty"`
	BaselineLogicalBytes   int64 `json:"baselineLogicalBytes,omitempty"`
	BaselineAllocatedBytes int64 `json:"baselineAllocatedBytes,omitempty"`
}

// BaselineBuilder must create the canonical Library directly at libraryPath.
// The path is already on the ReFS volume; copying a separate Image payload into
// it is deliberately not part of this contract.
type BaselineBuilder func(ctx context.Context, libraryPath string) error

type LibraryBaselineStore struct {
	paths            Paths
	now              func() time.Time
	coordinationHook func(string)
}

func NewLibraryBaselineStore(paths Paths) *LibraryBaselineStore {
	return &LibraryBaselineStore{paths: paths, now: time.Now}
}

func (s *LibraryBaselineStore) baselinePath(key CompatibilityKey) string {
	return filepath.Join(s.paths.Baselines, key.Digest)
}

func (s *LibraryBaselineStore) Resolve(ctx context.Context, key CompatibilityKey) (BaselineResolution, BaselineMetrics, error) {
	started := time.Now()
	metrics := BaselineMetrics{}
	if err := validateKey(key); err != nil {
		return BaselineResolution{}, metrics, err
	}
	path := s.baselinePath(key)
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return BaselineResolution{State: BaselineMissing, Reason: "baseline does not exist"}, metrics, nil
		}
		return BaselineResolution{}, metrics, newError(CodeBaselineCorrupt, "stat-baseline", path, err)
	}
	baseline, reason, err := s.verifyPath(ctx, path, key)
	metrics.BaselineVerifyMs = time.Since(started).Milliseconds()
	if err != nil {
		return BaselineResolution{}, metrics, err
	}
	if baseline == nil {
		return BaselineResolution{State: BaselineCorrupt, Reason: reason}, metrics, nil
	}
	metrics.BaselineLogicalBytes = baseline.Metadata.Library.LogicalBytes
	if usage, usageErr := shadow.MeasureDirectoryUsage(baseline.LibraryPath); usageErr == nil {
		metrics.BaselineAllocatedBytes = usage.AllocatedBytes
	}
	return BaselineResolution{State: BaselineValid, Baseline: baseline}, metrics, nil
}

func (s *LibraryBaselineStore) Ensure(ctx context.Context, key CompatibilityKey, build BaselineBuilder) (returnBaseline *Baseline, returnState BaselineState, metrics BaselineMetrics, returnErr error) {
	started := time.Now()
	if build == nil {
		return nil, "", metrics, newError(CodeInvalidConfiguration, "ensure-baseline", "", fmt.Errorf("builder is required"))
	}
	lock, err := s.acquireCreationLock(key)
	if err != nil {
		return nil, "", metrics, err
	}
	defer func() { returnErr = errors.Join(returnErr, lock.release()) }()

	resolution, verifyMetrics, err := s.Resolve(ctx, key)
	metrics.BaselineVerifyMs += verifyMetrics.BaselineVerifyMs
	if err != nil {
		return nil, "", metrics, err
	}
	if resolution.State == BaselineValid {
		return resolution.Baseline, resolution.State, verifyMetrics, nil
	}
	if resolution.State == BaselineCorrupt {
		if _, err := s.Quarantine(ctx, key, "corrupt"); err != nil {
			return nil, resolution.State, metrics, err
		}
	}

	if err := os.MkdirAll(s.paths.Baselines, 0700); err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "create-baselines-root", s.paths.Baselines, err)
	}
	token, err := randomToken()
	if err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "baseline-token", "", err)
	}
	staging := filepath.Join(s.paths.Baselines, "."+key.Digest+".staging-"+token[:16])
	if err := os.Mkdir(staging, 0700); err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "create-baseline-staging", staging, err)
	}
	committed := false
	defer func() {
		if !committed {
			if cleanupErr := makeWritableTree(staging); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
				returnErr = errors.Join(returnErr, newError(CodeCleanupFailed, "restore-baseline-staging-access", staging, cleanupErr))
			}
			if cleanupErr := os.RemoveAll(staging); cleanupErr != nil {
				returnErr = errors.Join(returnErr, newError(CodeCleanupFailed, "cleanup-baseline-staging", staging, cleanupErr))
			}
		}
	}()
	libraryPath := filepath.Join(staging, "Library")
	if err := os.Mkdir(libraryPath, 0700); err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "create-staging-library", libraryPath, err)
	}
	if err := build(ctx, libraryPath); err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "build-baseline", libraryPath, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, resolution.State, metrics, cancelled("build-baseline", libraryPath, err)
	}
	if err := validateLibraryTree(libraryPath); err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "validate-baseline-tree", libraryPath, err)
	}
	protection, err := protectBaselineTree(libraryPath)
	if err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "protect-baseline", libraryPath, err)
	}
	tree, err := HashTree(ctx, libraryPath)
	if err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "hash-baseline", libraryPath, err)
	}
	metadata := BaselineMetadata{
		SchemaVersion:  BaselineSchemaVersion,
		Key:            key,
		CreatedAt:      s.now().UTC(),
		Library:        tree,
		OwnershipToken: token,
		Protection:     protection,
	}
	if err := writeJSONAtomic(filepath.Join(staging, baselineMetadataFile), metadata, 0600); err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "write-baseline-metadata", staging, err)
	}
	// COMPLETE is deliberately the final staging write.
	if err := os.WriteFile(filepath.Join(staging, baselineCompleteFile), []byte("complete\n"), 0400); err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "write-baseline-complete", staging, err)
	}
	verified, reason, err := s.verifyPath(ctx, staging, key)
	if err != nil {
		return nil, resolution.State, metrics, err
	}
	if verified == nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "verify-staging-baseline", staging, fmt.Errorf("%s", reason))
	}
	finalPath := s.baselinePath(key)
	if _, err := os.Lstat(finalPath); err == nil {
		return nil, resolution.State, metrics, newError(CodeLeaseConflict, "commit-baseline", finalPath, fmt.Errorf("destination appeared while creation lock was held"))
	} else if !os.IsNotExist(err) {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "stat-baseline-destination", finalPath, err)
	}
	if err := os.Rename(staging, finalPath); err != nil {
		return nil, resolution.State, metrics, newError(CodeBaselineCorrupt, "commit-baseline", finalPath, err)
	}
	committed = true
	verified.Path = finalPath
	verified.LibraryPath = filepath.Join(finalPath, "Library")
	metrics.BaselineCreationMs = time.Since(started).Milliseconds()
	metrics.BaselineLogicalBytes = tree.LogicalBytes
	if usage, usageErr := shadow.MeasureDirectoryUsage(verified.LibraryPath); usageErr == nil {
		metrics.BaselineAllocatedBytes = usage.AllocatedBytes
	}
	return verified, resolution.State, metrics, nil
}

func validateLibraryTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("reparse points and symlinks are forbidden: %s", path)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported Library entry type %s: %s", info.Mode(), path)
		}
		return nil
	})
}

func (s *LibraryBaselineStore) Verify(ctx context.Context, baseline *Baseline) (BaselineResolution, BaselineMetrics, error) {
	if baseline == nil {
		return BaselineResolution{State: BaselineCorrupt, Reason: "baseline is nil"}, BaselineMetrics{}, nil
	}
	return s.Resolve(ctx, baseline.Metadata.Key)
}

func (s *LibraryBaselineStore) Clear(ctx context.Context, key CompatibilityKey) error {
	path := s.baselinePath(key)
	if !PathWithin(s.paths.Baselines, path) || filepath.Dir(path) != s.paths.Baselines {
		return newError(CodeOwnershipMismatch, "clear-baseline", path, fmt.Errorf("unsafe baseline path"))
	}
	quarantined, err := s.quarantineCoordinated(ctx, key, "clear", "clear-baseline")
	if err != nil {
		return err
	}
	if err := makeWritableTree(quarantined); err != nil {
		return newError(CodeCleanupFailed, "make-baseline-writable", quarantined, err)
	}
	if err := os.RemoveAll(quarantined); err != nil {
		return newError(CodeCleanupFailed, "delete-baseline", quarantined, err)
	}
	return nil
}

func (s *LibraryBaselineStore) Quarantine(ctx context.Context, key CompatibilityKey, reason string) (string, error) {
	return s.quarantineCoordinated(ctx, key, reason, "quarantine-baseline")
}

func (s *LibraryBaselineStore) quarantineCoordinated(ctx context.Context, key CompatibilityKey, reason, operation string) (destination string, returnErr error) {
	if err := ctx.Err(); err != nil {
		return "", cancelled(operation, s.baselinePath(key), err)
	}
	lock, err := s.acquireBaselineCoordination(ctx, key, operation)
	if err != nil {
		return "", err
	}
	defer func() { returnErr = errors.Join(returnErr, lock.release()) }()
	if s.coordinationHook != nil {
		s.coordinationHook(operation)
	}
	mutationMarker := s.baselineMutationPath(key)
	if err := createJSONExclusive(mutationMarker, map[string]any{"schemaVersion": LeaseSchemaVersion, "keyDigest": key.Digest, "operation": operation}); err != nil {
		return "", newError(CodeLeaseConflict, "create-baseline-mutation", mutationMarker, err)
	}
	defer func() {
		if err := os.Remove(mutationMarker); err != nil && !os.IsNotExist(err) {
			returnErr = errors.Join(returnErr, newError(CodeCleanupFailed, "remove-baseline-mutation", mutationMarker, err))
		}
	}()
	if err := s.ensureNotInUse(key); err != nil {
		return "", err
	}
	path := s.baselinePath(key)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", newError(CodeBaselineMissing, "quarantine-baseline", path, err)
		}
		return "", newError(CodeBaselineCorrupt, "stat-baseline", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !PathWithin(s.paths.Baselines, path) || filepath.Dir(path) != s.paths.Baselines {
		return "", newError(CodeOwnershipMismatch, "quarantine-baseline", path, fmt.Errorf("baseline is not an owned direct child"))
	}
	if err := os.MkdirAll(s.paths.Quarantine, 0700); err != nil {
		return "", newError(CodeCleanupFailed, "create-quarantine", s.paths.Quarantine, err)
	}
	token, err := randomToken()
	if err != nil {
		return "", newError(CodeCleanupFailed, "quarantine-token", path, err)
	}
	safeReason := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(reason))
	destination = filepath.Join(s.paths.Quarantine, key.Digest+"-"+safeReason+"-"+token[:12])
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return "", newError(CodeCleanupFailed, "quarantine-no-replace", destination, fmt.Errorf("destination already exists"))
	}
	if err := os.Rename(path, destination); err != nil {
		return "", newError(CodeCleanupFailed, "quarantine-baseline", path, err)
	}
	return destination, nil
}

func (s *LibraryBaselineStore) verifyPath(ctx context.Context, path string, key CompatibilityKey) (*Baseline, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", cancelled("verify-baseline", path, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, "baseline path is missing", nil
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "baseline path is not a real directory", nil
	}
	if _, err := os.Stat(filepath.Join(path, baselineCompleteFile)); err != nil {
		return nil, "COMPLETE marker is missing", nil
	}
	data, err := os.ReadFile(filepath.Join(path, baselineMetadataFile))
	if err != nil {
		return nil, "metadata is missing", nil
	}
	var metadata BaselineMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, "metadata is invalid JSON", nil
	}
	if metadata.SchemaVersion != BaselineSchemaVersion || metadata.Key.Digest != key.Digest || metadata.Key.SchemaVersion != key.SchemaVersion || metadata.OwnershipToken == "" {
		return nil, "metadata identity does not match", nil
	}
	libraryPath := filepath.Join(path, "Library")
	tree, err := HashTree(ctx, libraryPath)
	if err != nil {
		return nil, "Library is unreadable", nil
	}
	if tree != metadata.Library {
		return nil, "Library integrity verification failed", nil
	}
	if err := verifyBaselineProtection(libraryPath, metadata.Protection); err != nil {
		return nil, "Library protection verification failed: " + err.Error(), nil
	}
	return &Baseline{Path: path, LibraryPath: libraryPath, Metadata: metadata}, "", nil
}

type creationLock struct{ path string }

func (s *LibraryBaselineStore) acquireCreationLock(key CompatibilityKey) (*creationLock, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.paths.Leases, 0700); err != nil {
		return nil, newError(CodeLeaseConflict, "create-lease-root", s.paths.Leases, err)
	}
	path := filepath.Join(s.paths.Leases, "baseline-"+key.Digest+".lock")
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, newError(CodeLeaseConflict, "acquire-baseline-lock", path, err)
	}
	return &creationLock{path: path}, nil
}

func (lock *creationLock) release() error {
	if err := os.Remove(lock.path); err != nil {
		return newError(CodeCleanupFailed, "release-baseline-creation-lock", lock.path, err)
	}
	return nil
}

type activeUse struct {
	SchemaVersion  int    `json:"schemaVersion"`
	KeyDigest      string `json:"keyDigest"`
	LeaseID        string `json:"leaseId"`
	OwnershipToken string `json:"ownershipToken"`
}

func (s *LibraryBaselineStore) AcquireUse(ctx context.Context, key CompatibilityKey, leaseID string) (release func() error, returnErr error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if !leaseIDPattern.MatchString(leaseID) {
		return nil, newError(CodeLeaseConflict, "acquire-baseline-use", leaseID, fmt.Errorf("invalid lease id"))
	}
	token, err := randomToken()
	if err != nil {
		return nil, newError(CodeLeaseConflict, "active-use-token", leaseID, err)
	}
	if err := os.MkdirAll(s.paths.Leases, 0700); err != nil {
		return nil, newError(CodeLeaseConflict, "create-lease-root", s.paths.Leases, err)
	}
	lock, err := s.acquireBaselineCoordination(ctx, key, "acquire-baseline-use")
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, lock.release()) }()
	if s.coordinationHook != nil {
		s.coordinationHook("acquire-baseline-use")
	}
	if _, err := os.Lstat(s.baselineMutationPath(key)); err == nil {
		return nil, newError(CodeBaselineInUse, "acquire-baseline-use", s.baselinePath(key), fmt.Errorf("baseline is mutating"))
	} else if !os.IsNotExist(err) {
		return nil, newError(CodeLeaseConflict, "inspect-baseline-mutation", s.baselineMutationPath(key), err)
	}
	resolution, _, err := s.Resolve(ctx, key)
	if err != nil {
		return nil, err
	}
	if resolution.State == BaselineMissing {
		return nil, newError(CodeBaselineMissing, "acquire-baseline-use", s.baselinePath(key), nil)
	}
	if resolution.State != BaselineValid {
		return nil, newError(CodeBaselineCorrupt, "acquire-baseline-use", s.baselinePath(key), fmt.Errorf("state=%s reason=%s", resolution.State, resolution.Reason))
	}
	marker := filepath.Join(s.paths.Leases, "active-"+key.Digest+"-"+leaseID+".json")
	data, _ := json.Marshal(activeUse{SchemaVersion: LeaseSchemaVersion, KeyDigest: key.Digest, LeaseID: leaseID, OwnershipToken: token})
	file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, newError(CodeLeaseConflict, "acquire-baseline-use", marker, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(marker)
		return nil, newError(CodeLeaseConflict, "write-baseline-use", marker, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(marker)
		return nil, newError(CodeLeaseConflict, "close-baseline-use", marker, err)
	}
	released := false
	return func() error {
		if released {
			return nil
		}
		contents, err := os.ReadFile(marker)
		if err != nil {
			if os.IsNotExist(err) && released {
				return nil
			}
			return newError(CodeOwnershipMismatch, "read-baseline-use", marker, err)
		}
		var current activeUse
		if json.Unmarshal(contents, &current) != nil || current.OwnershipToken != token || current.KeyDigest != key.Digest || current.LeaseID != leaseID {
			return newError(CodeOwnershipMismatch, "release-baseline-use", marker, fmt.Errorf("active-use ownership changed"))
		}
		if err := os.Remove(marker); err != nil {
			return newError(CodeCleanupFailed, "release-baseline-use", marker, err)
		}
		released = true
		return nil
	}, nil
}

func (s *LibraryBaselineStore) acquireBaselineCoordination(ctx context.Context, key CompatibilityKey, operation string) (*coordinationLock, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.paths.Leases, 0700); err != nil {
		return nil, newError(CodeLeaseConflict, "create-lease-root", s.paths.Leases, err)
	}
	return acquireCoordinationLock(ctx, filepath.Join(s.paths.Leases, "baseline-"+key.Digest+".coord"), operation)
}

func (s *LibraryBaselineStore) baselineMutationPath(key CompatibilityKey) string {
	return filepath.Join(s.paths.Leases, "baseline-"+key.Digest+".mutation.json")
}

func (s *LibraryBaselineStore) Status(ctx context.Context, key CompatibilityKey) (BaselineResolution, BaselineMetrics, error) {
	if _, err := os.Lstat(s.baselineMutationPath(key)); err == nil {
		return BaselineResolution{State: BaselineMutating, CoordinationState: BaselineMutating, Reason: "baseline mutation marker exists"}, BaselineMetrics{}, nil
	} else if !os.IsNotExist(err) {
		return BaselineResolution{}, BaselineMetrics{}, newError(CodeLeaseConflict, "inspect-baseline-mutation", s.baselineMutationPath(key), err)
	}
	resolution, metrics, err := s.Resolve(ctx, key)
	if err != nil || resolution.State != BaselineValid {
		return resolution, metrics, err
	}
	if err := s.ensureNotInUse(key); ErrorCode(err) == CodeBaselineInUse {
		resolution.CoordinationState = BaselineInUse
	} else if err != nil {
		return BaselineResolution{}, metrics, err
	} else {
		resolution.CoordinationState = BaselineAvailable
	}
	return resolution, metrics, nil
}

func (s *LibraryBaselineStore) ensureNotInUse(key CompatibilityKey) error {
	entries, err := os.ReadDir(s.paths.Leases)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return newError(CodeBaselineInUse, "list-active-use", s.paths.Leases, err)
	}
	prefix := "active-" + key.Digest + "-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".json") {
			return newError(CodeBaselineInUse, "baseline-active-use", filepath.Join(s.paths.Leases, entry.Name()), nil)
		}
	}
	return nil
}

func validateKey(key CompatibilityKey) error {
	if key.SchemaVersion != BaselineSchemaVersion || !digestPattern.MatchString(key.Digest) {
		return newError(CodeInvalidConfiguration, "validate-compatibility-key", key.Digest, fmt.Errorf("unsupported schema or digest"))
	}
	return nil
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.Write(path, data, mode)
}
