package refsworkspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

var leaseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,63}$`)

const workerOwnerFile = ".testplay-worker-owner.json"

type Junctioner interface {
	Create(target, junction string) error
	Remove(target, junction string) error
}

type WorkerRequest struct {
	Key                    CompatibilityKey
	LeaseID                string
	JunctionPath           string
	ClusterSize            int64
	SoftBudgetBytes        int64
	ExpectedReserveBytes   int64
	CurrentVolumeUsedBytes int64
}

type WorkerMetadata struct {
	SchemaVersion  int          `json:"schemaVersion"`
	LeaseID        string       `json:"leaseId"`
	KeyDigest      string       `json:"keyDigest"`
	State          LeaseState   `json:"state"`
	PID            int          `json:"pid"`
	CreatedAt      time.Time    `json:"createdAt"`
	UpdatedAt      time.Time    `json:"updatedAt"`
	OwnershipToken string       `json:"ownershipToken"`
	WorkerPath     string       `json:"workerPath"`
	JunctionPath   string       `json:"junctionPath"`
	ReservedBytes  int64        `json:"reservedBytes"`
	Clone          CloneMetrics `json:"clone"`
}

type WorkerMetrics struct {
	CloneTreeMs                  int64 `json:"cloneTreeMs"`
	ClonedFileCount              int64 `json:"clonedFileCount"`
	ClonedBytes                  int64 `json:"clonedBytes"`
	PhysicalCopiedFileCount      int64 `json:"physicalCopiedFileCount"`
	PhysicalCopiedBytes          int64 `json:"physicalCopiedBytes"`
	TailCopiedBytes              int64 `json:"tailCopiedBytes"`
	WorkerReadyLogicalBytes      int64 `json:"workerReadyLogicalBytes"`
	WorkerReadyAllocatedBytes    int64 `json:"workerReadyAllocatedBytes"`
	WorkerReleasedLogicalBytes   int64 `json:"workerReleasedLogicalBytes"`
	WorkerReleasedAllocatedBytes int64 `json:"workerReleasedAllocatedBytes"`
	CleanupMs                    int64 `json:"cleanupMs"`
	WorkspacePreparationMs       int64 `json:"workspacePreparationMs,omitempty"`
	UnityWallClockMs             int64 `json:"unityWallClockMs,omitempty"`
}

type WorkerManager struct {
	paths        Paths
	baselines    *LibraryBaselineStore
	cloner       TreeCloner
	junctions    Junctioner
	now          func() time.Time
	pid          int
	processAlive func(int) bool
}

func NewWorkerManager(paths Paths, baselines *LibraryBaselineStore, cloner TreeCloner, junctions Junctioner) *WorkerManager {
	return &WorkerManager{
		paths:        paths,
		baselines:    baselines,
		cloner:       cloner,
		junctions:    junctions,
		now:          time.Now,
		pid:          os.Getpid(),
		processAlive: processIsAlive,
	}
}

type WorkerLease struct {
	mu            sync.Mutex
	manager       *WorkerManager
	metadata      WorkerMetadata
	leaseFile     string
	releaseActive func() error
	released      bool
	metrics       WorkerMetrics
}

func (lease *WorkerLease) Metadata() WorkerMetadata { return lease.metadata }

func (manager *WorkerManager) Acquire(ctx context.Context, request WorkerRequest) (*WorkerLease, WorkerMetrics, error) {
	metrics := WorkerMetrics{}
	if manager == nil || manager.baselines == nil || manager.cloner == nil || manager.junctions == nil {
		return nil, metrics, newError(CodeInvalidConfiguration, "worker-acquire", request.LeaseID, fmt.Errorf("worker dependencies are incomplete"))
	}
	if err := ctx.Err(); err != nil {
		return nil, metrics, cancelled("worker-acquire", request.LeaseID, err)
	}
	if !leaseIDPattern.MatchString(request.LeaseID) || request.JunctionPath == "" || !filepath.IsAbs(request.JunctionPath) || request.ClusterSize <= 0 {
		return nil, metrics, newError(CodeInvalidConfiguration, "worker-request", request.LeaseID, fmt.Errorf("invalid lease, junction, or cluster size"))
	}
	if request.SoftBudgetBytes <= 0 || request.ExpectedReserveBytes <= 0 {
		return nil, metrics, newError(CodeInvalidConfiguration, "worker-budget", request.LeaseID, fmt.Errorf("soft budget and reservation must be positive"))
	}
	if err := os.MkdirAll(manager.paths.Workers, 0700); err != nil {
		return nil, metrics, newError(CodeCloneFailed, "create-workers-root", manager.paths.Workers, err)
	}
	if err := os.MkdirAll(manager.paths.Leases, 0700); err != nil {
		return nil, metrics, newError(CodeLeaseConflict, "create-leases-root", manager.paths.Leases, err)
	}
	activeReservations, err := manager.checkLeases(request.LeaseID)
	if err != nil {
		return nil, metrics, err
	}
	if request.CurrentVolumeUsedBytes+activeReservations+request.ExpectedReserveBytes > request.SoftBudgetBytes {
		return nil, metrics, newError(CodeStorageBudgetExceeded, "reserve-worker", request.LeaseID, fmt.Errorf("used=%d activeReservations=%d requested=%d budget=%d", request.CurrentVolumeUsedBytes, activeReservations, request.ExpectedReserveBytes, request.SoftBudgetBytes))
	}
	resolution, _, err := manager.baselines.Resolve(ctx, request.Key)
	if err != nil {
		return nil, metrics, err
	}
	if resolution.State == BaselineMissing {
		return nil, metrics, newError(CodeBaselineMissing, "resolve-worker-baseline", request.Key.Digest, nil)
	}
	if resolution.State != BaselineValid || resolution.Baseline == nil {
		return nil, metrics, newError(CodeBaselineCorrupt, "resolve-worker-baseline", request.Key.Digest, fmt.Errorf("%s", resolution.Reason))
	}
	releaseActive, err := manager.baselines.AcquireUse(request.Key, request.LeaseID)
	if err != nil {
		return nil, metrics, err
	}
	releaseOnFailure := true
	defer func() {
		if releaseOnFailure {
			_ = releaseActive()
		}
	}()

	token, err := randomToken()
	if err != nil {
		return nil, metrics, newError(CodeLeaseConflict, "worker-token", request.LeaseID, err)
	}
	workerPath := filepath.Join(manager.paths.Workers, request.LeaseID)
	staging := filepath.Join(manager.paths.Workers, "."+request.LeaseID+".staging-"+token[:12])
	stagingCommitted := false
	defer func() {
		// This staging name and token were created by this acquire. If native
		// clone code reports uncertain ownership it must return an owned marker
		// error before this point; otherwise this bounded staging cleanup is safe.
		if !stagingCommitted {
			_ = os.RemoveAll(staging)
		}
	}()
	if _, err := os.Lstat(workerPath); !os.IsNotExist(err) {
		return nil, metrics, newError(CodeLeaseConflict, "worker-path", workerPath, fmt.Errorf("worker path already exists"))
	}
	metadata := WorkerMetadata{
		SchemaVersion:  LeaseSchemaVersion,
		LeaseID:        request.LeaseID,
		KeyDigest:      request.Key.Digest,
		State:          LeaseRequested,
		PID:            manager.pid,
		CreatedAt:      manager.now().UTC(),
		UpdatedAt:      manager.now().UTC(),
		OwnershipToken: token,
		WorkerPath:     workerPath,
		JunctionPath:   filepath.Clean(request.JunctionPath),
		ReservedBytes:  request.ExpectedReserveBytes,
	}
	leaseFile := filepath.Join(manager.paths.Leases, "worker-"+request.LeaseID+".json")
	if err := createJSONExclusive(leaseFile, metadata); err != nil {
		return nil, metrics, newError(CodeLeaseConflict, "create-worker-lease", leaseFile, err)
	}
	cleanupLease := true
	defer func() {
		if cleanupLease {
			_ = os.Remove(leaseFile)
		}
	}()
	metadata.State = LeaseCloning
	if err := manager.updateLease(leaseFile, &metadata); err != nil {
		return nil, metrics, err
	}
	cloneMetrics, err := manager.cloner.CloneTree(ctx, resolution.Baseline.LibraryPath, filepath.Join(staging, "Library"), request.ClusterSize)
	metadata.Clone = cloneMetrics
	metrics = workerMetricsFromClone(cloneMetrics)
	if err != nil {
		if ErrorCode(err) == CodeBlockCloneUnavailable {
			return nil, metrics, err
		}
		return nil, metrics, newError(CodeCloneFailed, "clone-worker-library", staging, err)
	}
	if err := ValidateCloneMetrics(cloneMetrics); err != nil {
		return nil, metrics, err
	}
	if err := os.WriteFile(filepath.Join(staging, workerOwnerFile), mustJSON(metadata), 0600); err != nil {
		return nil, metrics, newError(CodeCloneFailed, "write-worker-owner", staging, err)
	}
	if err := makeWritableTree(staging); err != nil {
		return nil, metrics, newError(CodeCloneFailed, "make-worker-writable", staging, err)
	}
	if err := os.Rename(staging, workerPath); err != nil {
		return nil, metrics, newError(CodeCloneFailed, "commit-worker", workerPath, err)
	}
	stagingCommitted = true
	workerCommitted := true
	defer func() {
		if cleanupLease && workerCommitted {
			_ = os.RemoveAll(workerPath)
		}
	}()

	// Re-hash the canonical payload after cloning. A changed baseline blocks the
	// worker before Unity can mutate it.
	after, _, err := manager.baselines.Resolve(ctx, request.Key)
	if err != nil || after.State != BaselineValid {
		if err == nil {
			err = fmt.Errorf("state=%s reason=%s", after.State, after.Reason)
		}
		return nil, metrics, newError(CodeBaselineCorrupt, "verify-baseline-after-clone", resolution.Baseline.Path, err)
	}
	usage, err := shadow.MeasureDirectoryUsage(filepath.Join(workerPath, "Library"))
	if err != nil {
		return nil, metrics, newError(CodeCloneFailed, "measure-worker", workerPath, err)
	}
	metrics.WorkerReadyLogicalBytes = usage.LogicalBytes
	metrics.WorkerReadyAllocatedBytes = usage.AllocatedBytes
	if err := manager.junctions.Create(filepath.Join(workerPath, "Library"), metadata.JunctionPath); err != nil {
		return nil, metrics, newError(CodeJunctionFailed, "create-library-junction", metadata.JunctionPath, err)
	}
	metadata.State = LeaseReady
	if err := manager.updateLease(leaseFile, &metadata); err != nil {
		_ = manager.junctions.Remove(filepath.Join(workerPath, "Library"), metadata.JunctionPath)
		return nil, metrics, err
	}
	releaseOnFailure = false
	cleanupLease = false
	return &WorkerLease{
		manager:       manager,
		metadata:      metadata,
		leaseFile:     leaseFile,
		releaseActive: releaseActive,
		metrics:       metrics,
	}, metrics, nil
}

func (lease *WorkerLease) MarkRunning() error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released || lease.metadata.State != LeaseReady {
		return newError(CodeLeaseConflict, "mark-worker-running", lease.metadata.LeaseID, fmt.Errorf("state=%s", lease.metadata.State))
	}
	lease.metadata.State = LeaseRunning
	return lease.manager.updateLease(lease.leaseFile, &lease.metadata)
}

// Release requires the caller to have already observed complete Unity process
// termination. Forced-termination recovery is intentionally outside this probe.
func (lease *WorkerLease) Release(ctx context.Context) (WorkerMetrics, error) {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return lease.metrics, nil
	}
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return lease.metrics, cancelled("worker-release", lease.metadata.WorkerPath, err)
	}
	lease.metadata.State = LeaseReleasing
	if err := lease.manager.updateLease(lease.leaseFile, &lease.metadata); err != nil {
		return lease.metrics, err
	}
	libraryPath := filepath.Join(lease.metadata.WorkerPath, "Library")
	if err := lease.manager.junctions.Remove(libraryPath, lease.metadata.JunctionPath); err != nil {
		return lease.metrics, newError(CodeJunctionFailed, "remove-library-junction", lease.metadata.JunctionPath, err)
	}
	usage, err := shadow.MeasureDirectoryUsage(libraryPath)
	if err != nil {
		return lease.metrics, newError(CodeCleanupFailed, "measure-worker-release", libraryPath, err)
	}
	lease.metrics.WorkerReleasedLogicalBytes = usage.LogicalBytes
	lease.metrics.WorkerReleasedAllocatedBytes = usage.AllocatedBytes
	if err := verifyWorkerOwner(lease.metadata.WorkerPath, lease.metadata); err != nil {
		return lease.metrics, err
	}
	quarantine := filepath.Join(lease.manager.paths.Quarantine, "worker-"+lease.metadata.LeaseID+"-"+lease.metadata.OwnershipToken[:12])
	if err := os.MkdirAll(lease.manager.paths.Quarantine, 0700); err != nil {
		return lease.metrics, newError(CodeCleanupFailed, "create-quarantine", lease.manager.paths.Quarantine, err)
	}
	if _, err := os.Lstat(quarantine); !os.IsNotExist(err) {
		return lease.metrics, newError(CodeCleanupFailed, "worker-quarantine-no-replace", quarantine, fmt.Errorf("destination already exists"))
	}
	if err := os.Rename(lease.metadata.WorkerPath, quarantine); err != nil {
		return lease.metrics, newError(CodeCleanupFailed, "quarantine-worker", lease.metadata.WorkerPath, err)
	}
	if err := verifyWorkerOwner(quarantine, lease.metadata); err != nil {
		lease.metadata.State = LeaseQuarantined
		_ = lease.manager.updateLease(lease.leaseFile, &lease.metadata)
		return lease.metrics, err
	}
	if err := os.RemoveAll(quarantine); err != nil {
		lease.metadata.State = LeaseQuarantined
		_ = lease.manager.updateLease(lease.leaseFile, &lease.metadata)
		return lease.metrics, newError(CodeCleanupFailed, "delete-worker", quarantine, err)
	}
	if err := lease.releaseActive(); err != nil {
		return lease.metrics, err
	}
	lease.metadata.State = LeaseReleased
	if err := lease.manager.updateLease(lease.leaseFile, &lease.metadata); err != nil {
		return lease.metrics, err
	}
	if err := os.Remove(lease.leaseFile); err != nil {
		return lease.metrics, newError(CodeCleanupFailed, "remove-worker-lease", lease.leaseFile, err)
	}
	lease.released = true
	lease.metrics.CleanupMs = time.Since(started).Milliseconds()
	return lease.metrics, nil
}

func (manager *WorkerManager) checkLeases(requestedLeaseID string) (int64, error) {
	entries, err := os.ReadDir(manager.paths.Leases)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, newError(CodeLeaseConflict, "list-worker-leases", manager.paths.Leases, err)
	}
	var reservations int64
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "worker-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(manager.paths.Leases, entry.Name()))
		if err != nil {
			return 0, newError(CodeOrphanFound, "read-worker-lease", entry.Name(), err)
		}
		var metadata WorkerMetadata
		if json.Unmarshal(data, &metadata) != nil || metadata.SchemaVersion != LeaseSchemaVersion || metadata.OwnershipToken == "" {
			return 0, newError(CodeOrphanFound, "decode-worker-lease", entry.Name(), fmt.Errorf("invalid lease metadata"))
		}
		if metadata.LeaseID == requestedLeaseID {
			return 0, newError(CodeLeaseConflict, "duplicate-worker-lease", requestedLeaseID, nil)
		}
		if metadata.State != LeaseReleased {
			if !manager.processAlive(metadata.PID) {
				return 0, newError(CodeOrphanFound, "orphan-worker-lease", metadata.LeaseID, fmt.Errorf("pid %d is not active", metadata.PID))
			}
			reservations += metadata.ReservedBytes
		}
	}
	return reservations, nil
}

func (manager *WorkerManager) updateLease(path string, metadata *WorkerMetadata) error {
	metadata.UpdatedAt = manager.now().UTC()
	if err := writeJSONAtomic(path, metadata, 0600); err != nil {
		return newError(CodeLeaseConflict, "update-worker-lease", path, err)
	}
	return nil
}

func verifyWorkerOwner(root string, expected WorkerMetadata) error {
	if !PathWithin(filepath.Dir(root), root) {
		return newError(CodeOwnershipMismatch, "verify-worker-path", root, fmt.Errorf("unsafe worker path"))
	}
	data, err := os.ReadFile(filepath.Join(root, workerOwnerFile))
	if err != nil {
		return newError(CodeOwnershipMismatch, "read-worker-owner", root, err)
	}
	var actual WorkerMetadata
	if json.Unmarshal(data, &actual) != nil || actual.OwnershipToken != expected.OwnershipToken || actual.LeaseID != expected.LeaseID || actual.KeyDigest != expected.KeyDigest {
		return newError(CodeOwnershipMismatch, "verify-worker-owner", root, fmt.Errorf("worker ownership changed"))
	}
	return nil
}

func workerMetricsFromClone(clone CloneMetrics) WorkerMetrics {
	return WorkerMetrics{
		CloneTreeMs:             clone.CloneTreeMs,
		ClonedFileCount:         clone.ClonedFileCount,
		ClonedBytes:             clone.ClonedBytes,
		PhysicalCopiedFileCount: clone.PhysicalCopiedFileCount,
		PhysicalCopiedBytes:     clone.PhysicalCopiedBytes,
		TailCopiedBytes:         clone.TailCopiedBytes,
	}
}

func createJSONExclusive(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	return file.Close()
}

func mustJSON(value any) []byte {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

func randomToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
