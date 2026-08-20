package refsworkspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

var leaseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,63}$`)

const workerOwnerFile = ".testplay-worker-owner.json"

type Junctioner interface {
	Create(target, junction string) error
	Remove(target, junction string) error
}

type WorkerRequest struct {
	Key          CompatibilityKey
	LeaseID      string
	JunctionPath string
}

type WorkerStorageMeter interface {
	VolumeUsedBytes(context.Context) (int64, error)
	HostFreeBytes(context.Context) (int64, error)
}

type WorkerMetadata struct {
	SchemaVersion       int          `json:"schemaVersion"`
	LeaseID             string       `json:"leaseId"`
	KeyDigest           string       `json:"keyDigest"`
	State               LeaseState   `json:"state"`
	PID                 int          `json:"pid"`
	CreatedAt           time.Time    `json:"createdAt"`
	UpdatedAt           time.Time    `json:"updatedAt"`
	OwnershipToken      string       `json:"ownershipToken"`
	WorkerPath          string       `json:"workerPath"`
	JunctionPath        string       `json:"junctionPath"`
	ReservedBytes       int64        `json:"reservedBytes"`
	Clone               CloneMetrics `json:"clone"`
	JunctionRemoved     bool         `json:"junctionRemoved"`
	WorkerOwnerVerified bool         `json:"workerOwnerVerified"`
	WorkerQuarantined   bool         `json:"workerQuarantined"`
	QuarantinePath      string       `json:"quarantinePath,omitempty"`
	WorkerDeleted       bool         `json:"workerDeleted"`
	ActiveUseReleased   bool         `json:"activeUseReleased"`
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
	BaselinePreCloneVerifyMs     int64 `json:"baselinePreCloneVerifyMs"`
	BaselinePostCloneVerifyMs    int64 `json:"baselinePostCloneVerifyMs"`
	BaselineVerifyFileCount      int64 `json:"baselineVerifyFileCount"`
	BaselineVerifyLogicalBytes   int64 `json:"baselineVerifyLogicalBytes"`
	SparseFileCount              int64 `json:"sparseFileCount"`
	SparseLogicalBytes           int64 `json:"sparseLogicalBytes"`
	SparseAllocatedSourceBytes   int64 `json:"sparseAllocatedSourceBytes"`
	SparseClonedBytes            int64 `json:"sparseClonedBytes"`
	SparseHoleBytes              int64 `json:"sparseHoleBytes"`
}

type WorkerManager struct {
	paths           Paths
	baselines       *LibraryBaselineStore
	cloner          TreeCloner
	junctions       Junctioner
	now             func() time.Time
	pid             int
	processAlive    func(int) bool
	storage         WorkerStorageMeter
	policy          PoolPolicy
	releaseHook     func(string) error
	acquireHook     func(string, string)
	removeLease     func(string) error
	updateLeaseHook func(*WorkerMetadata) error
	mkdir           func(string, os.FileMode) error
	writeFile       func(string, []byte, os.FileMode) error
	makeWritable    func(string) error
	rename          func(string, string) error
	resolveBaseline func(context.Context, CompatibilityKey) (BaselineResolution, BaselineMetrics, error)
}

func NewVerifiedWorkerManager(paths Paths, baselines *LibraryBaselineStore, cloner TreeCloner, junctions Junctioner, host, pool PoolMetadata, volume VolumeInfo) (*WorkerManager, error) {
	if err := validateOwnedPaths(paths); err != nil {
		return nil, err
	}
	policy, err := BuildVerifiedPoolPolicy(host, pool, volume)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(pool.VHDXPath) != paths.VHDX {
		return nil, newError(CodePoolCorrupt, "verify-worker-pool-path", paths.VHDX, fmt.Errorf("verified metadata references %q", pool.VHDXPath))
	}
	return newWorkerManager(paths, baselines, cloner, junctions, policy, newNativeWorkerStorageMeter(paths)), nil
}

// newWorkerManager is intentionally unexported. It is the unit-test seam for
// storage meters; production callers must use NewVerifiedWorkerManager.
func newWorkerManager(paths Paths, baselines *LibraryBaselineStore, cloner TreeCloner, junctions Junctioner, policy PoolPolicy, meter WorkerStorageMeter) *WorkerManager {
	return &WorkerManager{
		paths:           paths,
		baselines:       baselines,
		cloner:          cloner,
		junctions:       junctions,
		policy:          policy,
		now:             time.Now,
		pid:             os.Getpid(),
		processAlive:    processIsAlive,
		removeLease:     os.Remove,
		mkdir:           os.Mkdir,
		writeFile:       os.WriteFile,
		makeWritable:    makeWritableTree,
		rename:          os.Rename,
		resolveBaseline: baselines.Resolve,
		storage:         meter,
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

func (manager *WorkerManager) Acquire(ctx context.Context, request WorkerRequest) (resultLease *WorkerLease, metrics WorkerMetrics, returnErr error) {
	manager.observeAcquire(request.LeaseID, "acquire-start")
	defer manager.observeAcquire(request.LeaseID, "acquire-end")
	if manager == nil || manager.baselines == nil || manager.cloner == nil || manager.junctions == nil || manager.storage == nil || manager.mkdir == nil || manager.writeFile == nil || manager.makeWritable == nil || manager.rename == nil || manager.resolveBaseline == nil {
		return nil, metrics, newError(CodeInvalidConfiguration, "worker-acquire", request.LeaseID, fmt.Errorf("worker dependencies are incomplete"))
	}
	if err := ctx.Err(); err != nil {
		return nil, metrics, cancelled("worker-acquire", request.LeaseID, err)
	}
	if !leaseIDPattern.MatchString(request.LeaseID) || request.JunctionPath == "" || !filepath.IsAbs(request.JunctionPath) {
		return nil, metrics, newError(CodeInvalidConfiguration, "worker-request", request.LeaseID, fmt.Errorf("invalid lease or junction"))
	}
	if manager.policy.ClusterSize <= 0 || manager.policy.SoftBudgetBytes <= 0 || manager.policy.WorkerReserveBytes <= 0 || manager.policy.MinimumHostFreeBytes <= 0 {
		return nil, metrics, newError(CodeInvalidConfiguration, "worker-policy", request.LeaseID, fmt.Errorf("verified worker policy is incomplete"))
	}
	for _, path := range []string{manager.paths.PoolRoot, manager.paths.Workers, manager.paths.Leases} {
		if err := requireExistingWorkerLayoutDirectory(path); err != nil {
			return nil, metrics, err
		}
	}
	token, err := randomToken()
	if err != nil {
		return nil, metrics, newError(CodeLeaseConflict, "worker-token", request.LeaseID, err)
	}
	workerPath := filepath.Join(manager.paths.Workers, request.LeaseID)
	metadata := WorkerMetadata{
		SchemaVersion: LeaseSchemaVersion, LeaseID: request.LeaseID, KeyDigest: request.Key.Digest,
		State: LeaseRequested, PID: manager.pid, CreatedAt: manager.now().UTC(), UpdatedAt: manager.now().UTC(),
		OwnershipToken: token, WorkerPath: workerPath, JunctionPath: filepath.Clean(request.JunctionPath),
		ReservedBytes:  manager.policy.WorkerReserveBytes,
		QuarantinePath: filepath.Join(manager.paths.Quarantine, "worker-"+request.LeaseID+"-"+token[:12]),
	}
	leaseFile, err := manager.reserveWorker(ctx, request, metadata)
	if err != nil {
		return nil, metrics, err
	}
	manager.observeAcquire(request.LeaseID, "reservation-committed")
	cleanupLease := true
	defer func() {
		if cleanupLease {
			if cleanupErr := manager.removeLease(leaseFile); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
				returnErr = errors.Join(returnErr, newError(CodeCleanupFailed, "cleanup-failed-worker-lease", leaseFile, cleanupErr))
			}
		}
	}()

	resolution, preVerify, err := manager.resolveBaseline(ctx, request.Key)
	metrics.BaselinePreCloneVerifyMs = preVerify.BaselineVerifyMs
	if err != nil {
		return nil, metrics, err
	}
	if resolution.State == BaselineMissing {
		return nil, metrics, newError(CodeBaselineMissing, "resolve-worker-baseline", request.Key.Digest, nil)
	}
	if resolution.State != BaselineValid || resolution.Baseline == nil {
		return nil, metrics, newError(CodeBaselineCorrupt, "resolve-worker-baseline", request.Key.Digest, fmt.Errorf("%s", resolution.Reason))
	}
	manager.observeAcquire(request.LeaseID, "baseline-verified")
	metrics.BaselineVerifyFileCount = resolution.Baseline.Metadata.Library.FileCount
	metrics.BaselineVerifyLogicalBytes = resolution.Baseline.Metadata.Library.LogicalBytes
	releaseActive, err := manager.baselines.AcquireUse(ctx, request.Key, request.LeaseID)
	if err != nil {
		return nil, metrics, err
	}
	manager.observeAcquire(request.LeaseID, "active-use-acquired")
	releaseOnFailure := true
	defer func() {
		if releaseOnFailure {
			if cleanupErr := releaseActive(); cleanupErr != nil {
				returnErr = errors.Join(returnErr, cleanupErr)
			}
		}
	}()
	staging := filepath.Join(manager.paths.Workers, "."+request.LeaseID+".staging-"+token[:12])
	if err := validateWorkerStagingPaths(manager.paths, workerPath, staging); err != nil {
		return nil, metrics, err
	}
	if _, err := os.Lstat(staging); err == nil {
		return nil, metrics, newError(CodeLeaseConflict, "create-worker-staging", staging, fmt.Errorf("staging path already exists"))
	} else if !os.IsNotExist(err) {
		return nil, metrics, newError(CodeCloneFailed, "inspect-worker-staging", staging, err)
	}
	if _, err := os.Lstat(workerPath); err == nil {
		return nil, metrics, newError(CodeLeaseConflict, "worker-path", workerPath, fmt.Errorf("worker path already exists"))
	} else if !os.IsNotExist(err) {
		return nil, metrics, newError(CodeLeaseConflict, "worker-path", workerPath, err)
	}

	stagingCreated := false
	stagingCommitted := false
	defer func() {
		// A collision is never adopted or removed. Only the exact directory this
		// acquire created is eligible for bounded cleanup.
		if stagingCreated && !stagingCommitted {
			if cleanupErr := os.RemoveAll(staging); cleanupErr != nil {
				returnErr = errors.Join(returnErr, newError(CodeCleanupFailed, "cleanup-worker-staging", staging, cleanupErr))
			}
		}
	}()
	metadata.State = LeaseCloning
	if err := manager.updateLease(leaseFile, &metadata); err != nil {
		return nil, metrics, err
	}
	manager.observeAcquire(request.LeaseID, "cloning-journal-committed")
	if err := manager.mkdir(staging, 0700); err != nil {
		code := CodeCloneFailed
		if os.IsExist(err) {
			code = CodeLeaseConflict
		}
		return nil, metrics, newError(code, "create-worker-staging", staging, err)
	}
	stagingCreated = true
	manager.observeAcquire(request.LeaseID, "staging-created")
	if err := verifyCreatedWorkerStaging(manager.paths, staging); err != nil {
		return nil, metrics, err
	}
	manager.observeAcquire(request.LeaseID, "clone-start")
	cloneMetrics, err := manager.cloner.CloneTree(ctx, CloneRequest{
		TrustedRoot: manager.paths.PoolRoot,
		Source:      resolution.Baseline.LibraryPath, Destination: filepath.Join(staging, "Library"),
		ClusterSize: manager.policy.ClusterSize,
	})
	manager.observeAcquire(request.LeaseID, "clone-end")
	metadata.Clone = cloneMetrics
	cloneWorkerMetrics := workerMetricsFromClone(cloneMetrics)
	cloneWorkerMetrics.BaselinePreCloneVerifyMs = metrics.BaselinePreCloneVerifyMs
	cloneWorkerMetrics.BaselineVerifyFileCount = metrics.BaselineVerifyFileCount
	cloneWorkerMetrics.BaselineVerifyLogicalBytes = metrics.BaselineVerifyLogicalBytes
	metrics = cloneWorkerMetrics
	if err != nil {
		if ErrorCode(err) == CodeBlockCloneUnavailable {
			return nil, metrics, err
		}
		return nil, metrics, newError(CodeCloneFailed, "clone-worker-library", staging, err)
	}
	if err := ValidateCloneMetrics(cloneMetrics); err != nil {
		return nil, metrics, err
	}
	if err := manager.writeFile(filepath.Join(staging, workerOwnerFile), mustJSON(metadata), 0600); err != nil {
		return nil, metrics, newError(CodeCloneFailed, "write-worker-owner", staging, err)
	}
	if err := manager.makeWritable(staging); err != nil {
		return nil, metrics, newError(CodeCloneFailed, "make-worker-writable", staging, err)
	}
	if err := manager.rename(staging, workerPath); err != nil {
		return nil, metrics, newError(CodeCloneFailed, "commit-worker", workerPath, err)
	}
	manager.observeAcquire(request.LeaseID, "worker-committed")
	stagingCommitted = true
	workerCommitted := true
	defer func() {
		if cleanupLease && workerCommitted {
			if cleanupErr := os.RemoveAll(workerPath); cleanupErr != nil {
				returnErr = errors.Join(returnErr, newError(CodeCleanupFailed, "cleanup-worker", workerPath, cleanupErr))
			}
		}
	}()
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		return nil, metrics, newError(CodeCloneFailed, "verify-worker-staging-committed", staging, errors.Join(err, fmt.Errorf("staging path remains after commit")))
	}
	if err := verifyWorkerOwner(workerPath, metadata); err != nil {
		return nil, metrics, err
	}
	if info, err := os.Lstat(filepath.Join(workerPath, "Library")); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, metrics, newError(CodeCloneFailed, "verify-worker-library", filepath.Join(workerPath, "Library"), errors.Join(err, fmt.Errorf("committed Library is not a real directory")))
	}

	// Re-hash the canonical payload after cloning. A changed baseline blocks the
	// worker before Unity can mutate it.
	after, postVerify, err := manager.resolveBaseline(ctx, request.Key)
	metrics.BaselinePostCloneVerifyMs = postVerify.BaselineVerifyMs
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
	manager.observeAcquire(request.LeaseID, "junction-created")
	metadata.State = LeaseReady
	if err := manager.updateLease(leaseFile, &metadata); err != nil {
		if cleanupErr := manager.junctions.Remove(filepath.Join(workerPath, "Library"), metadata.JunctionPath); cleanupErr != nil {
			err = errors.Join(err, newError(CodeCleanupFailed, "cleanup-library-junction", metadata.JunctionPath, cleanupErr))
		}
		return nil, metrics, err
	}
	manager.observeAcquire(request.LeaseID, "ready-journal-committed")
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

func (manager *WorkerManager) observeAcquire(leaseID, stage string) {
	if manager != nil && manager.acquireHook != nil {
		manager.acquireHook(leaseID, stage)
	}
}

func requireExistingWorkerLayoutDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return newError(CodePoolCorrupt, "validate-worker-layout", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return newError(CodeOwnershipMismatch, "validate-worker-layout", path, fmt.Errorf("managed layout entry must be a real directory"))
	}
	reparse, err := inspectPathReparse(path)
	if err != nil || reparse {
		return newError(CodeOwnershipMismatch, "validate-worker-layout", path, errors.Join(err, fmt.Errorf("managed layout entry is a reparse point")))
	}
	return nil
}

func validateWorkerStagingPaths(paths Paths, workerPath, staging string) error {
	if !filepath.IsAbs(staging) || !filepath.IsAbs(workerPath) {
		return newError(CodeInvalidConfiguration, "validate-worker-staging", staging, fmt.Errorf("absolute worker paths required"))
	}
	workers := filepath.Clean(paths.Workers)
	staging = filepath.Clean(staging)
	workerPath = filepath.Clean(workerPath)
	if !strings.EqualFold(filepath.Clean(filepath.Dir(staging)), workers) || !PathWithin(workers, staging) || !PathWithin(paths.PoolRoot, staging) {
		return newError(CodeOwnershipMismatch, "validate-worker-staging", staging, fmt.Errorf("staging must be a direct child of the managed workers root"))
	}
	if strings.EqualFold(staging, workerPath) {
		return newError(CodeOwnershipMismatch, "validate-worker-staging", staging, fmt.Errorf("staging and final worker paths must differ"))
	}
	return nil
}

func verifyCreatedWorkerStaging(paths Paths, staging string) error {
	info, err := os.Lstat(staging)
	if err != nil {
		return newError(CodeCloneFailed, "verify-worker-staging", staging, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return newError(CodeOwnershipMismatch, "verify-worker-staging", staging, fmt.Errorf("created staging is not a real directory"))
	}
	reparse, err := inspectPathReparse(staging)
	if err != nil || reparse {
		return newError(CodeOwnershipMismatch, "verify-worker-staging", staging, errors.Join(err, fmt.Errorf("created staging is a reparse point")))
	}
	if !PathWithin(paths.PoolRoot, staging) || !strings.EqualFold(filepath.Clean(filepath.Dir(staging)), filepath.Clean(paths.Workers)) {
		return newError(CodeOwnershipMismatch, "verify-worker-staging", staging, fmt.Errorf("created staging escaped the verified ReFS pool"))
	}
	library := filepath.Join(staging, "Library")
	if _, err := os.Lstat(library); err == nil {
		return newError(CodeLeaseConflict, "verify-worker-staging", library, fmt.Errorf("clone destination already exists"))
	} else if !os.IsNotExist(err) {
		return newError(CodeCloneFailed, "verify-worker-staging", library, err)
	}
	return nil
}

func (manager *WorkerManager) reserveWorker(ctx context.Context, request WorkerRequest, metadata WorkerMetadata) (leaseFile string, returnErr error) {
	if metadata.ReservedBytes != manager.policy.WorkerReserveBytes {
		return "", newError(CodeInvalidConfiguration, "reserve-worker", request.LeaseID, fmt.Errorf("journal reserve does not match verified policy"))
	}
	lock, err := acquireCoordinationLock(ctx, filepath.Join(manager.paths.Leases, ".reservation.lock"), "reserve-worker")
	if err != nil {
		return "", err
	}
	defer func() { returnErr = errors.Join(returnErr, lock.release()) }()
	used, err := manager.storage.VolumeUsedBytes(ctx)
	if err != nil {
		return "", newError(CodeStorageBudgetExceeded, "measure-refs-used", manager.paths.PoolRoot, err)
	}
	if used < 0 {
		return "", newError(CodeStorageBudgetExceeded, "measure-refs-used", manager.paths.PoolRoot, fmt.Errorf("negative used-byte measurement: %d", used))
	}
	hostFree, err := manager.storage.HostFreeBytes(ctx)
	if err != nil {
		return "", newError(CodeHostFreeSpaceFloor, "measure-host-free", manager.paths.Root, err)
	}
	requiredHostFree, ok := checkedAddInt64(manager.policy.MinimumHostFreeBytes, manager.policy.WorkerReserveBytes)
	if !ok {
		return "", newError(CodeHostFreeSpaceFloor, "reserve-worker", request.LeaseID, fmt.Errorf("host free requirement overflows"))
	}
	if hostFree < requiredHostFree {
		return "", newError(CodeHostFreeSpaceFloor, "reserve-worker", request.LeaseID, fmt.Errorf("hostFree=%d required=%d", hostFree, requiredHostFree))
	}
	activeReservations, err := manager.checkLeases(request.LeaseID)
	if err != nil {
		return "", err
	}
	available := manager.policy.SoftBudgetBytes - used
	if available < 0 || activeReservations > available || manager.policy.WorkerReserveBytes > available-activeReservations {
		return "", newError(CodeStorageBudgetExceeded, "reserve-worker", request.LeaseID, fmt.Errorf("used=%d activeReservations=%d requested=%d budget=%d", used, activeReservations, manager.policy.WorkerReserveBytes, manager.policy.SoftBudgetBytes))
	}
	leaseFile = filepath.Join(manager.paths.Leases, "worker-"+request.LeaseID+".json")
	if err := createJSONExclusive(leaseFile, metadata); err != nil {
		return "", newError(CodeLeaseConflict, "create-worker-lease", leaseFile, err)
	}
	return leaseFile, nil
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
// termination. Retry is idempotent only in the same process through this same
// WorkerLease object. Loading a journal in a new process, forced-termination
// recovery, and reboot recovery are intentionally outside this probe.
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
	if lease.metadata.State != LeaseReleasing && lease.metadata.State != LeaseQuarantined && lease.metadata.State != LeaseReleased {
		lease.metadata.State = LeaseReleasing
		if err := lease.manager.updateLease(lease.leaseFile, &lease.metadata); err != nil {
			return lease.metrics, err
		}
		if err := lease.manager.runReleaseHook("releasing"); err != nil {
			return lease.metrics, err
		}
	}
	libraryPath := filepath.Join(lease.metadata.WorkerPath, "Library")
	if !lease.metadata.JunctionRemoved {
		if err := lease.manager.junctions.Remove(libraryPath, lease.metadata.JunctionPath); err != nil {
			if _, statErr := os.Lstat(lease.metadata.JunctionPath); !os.IsNotExist(statErr) || verifyWorkerOwner(lease.metadata.WorkerPath, lease.metadata) != nil {
				return lease.metrics, newError(CodeJunctionFailed, "remove-library-junction", lease.metadata.JunctionPath, err)
			}
		}
		lease.metadata.JunctionRemoved = true
		if err := lease.manager.updateLease(lease.leaseFile, &lease.metadata); err != nil {
			return lease.metrics, err
		}
		if err := lease.manager.runReleaseHook("junction-removed"); err != nil {
			return lease.metrics, err
		}
	}
	if !lease.metadata.WorkerQuarantined && !lease.metadata.WorkerDeleted {
		usage, err := shadow.MeasureDirectoryUsage(libraryPath)
		if err != nil {
			return lease.metrics, newError(CodeCleanupFailed, "measure-worker-release", libraryPath, err)
		}
		lease.metrics.WorkerReleasedLogicalBytes = usage.LogicalBytes
		lease.metrics.WorkerReleasedAllocatedBytes = usage.AllocatedBytes
	}
	quarantine := lease.metadata.QuarantinePath
	if quarantine == "" {
		quarantine = filepath.Join(lease.manager.paths.Quarantine, "worker-"+lease.metadata.LeaseID+"-"+lease.metadata.OwnershipToken[:12])
		lease.metadata.QuarantinePath = quarantine
	}
	if !lease.metadata.WorkerQuarantined {
		workerExists := pathExists(lease.metadata.WorkerPath)
		quarantineExists := pathExists(quarantine)
		switch {
		case workerExists && quarantineExists:
			return lease.metrics, newError(CodeCleanupFailed, "worker-quarantine-no-replace", quarantine, fmt.Errorf("worker and quarantine both exist"))
		case workerExists:
			if err := verifyWorkerOwner(lease.metadata.WorkerPath, lease.metadata); err != nil {
				return lease.metrics, err
			}
			if err := os.MkdirAll(lease.manager.paths.Quarantine, 0700); err != nil {
				return lease.metrics, newError(CodeCleanupFailed, "create-quarantine", lease.manager.paths.Quarantine, err)
			}
			if err := os.Rename(lease.metadata.WorkerPath, quarantine); err != nil {
				return lease.metrics, newError(CodeCleanupFailed, "quarantine-worker", lease.metadata.WorkerPath, err)
			}
		case !quarantineExists:
			return lease.metrics, newError(CodeOwnershipMismatch, "resume-worker-quarantine", lease.metadata.WorkerPath, fmt.Errorf("worker and quarantine are both absent without deletion milestone"))
		}
		if err := verifyWorkerOwner(quarantine, lease.metadata); err != nil {
			return lease.metrics, err
		}
		lease.metadata.WorkerOwnerVerified = true
		lease.metadata.WorkerQuarantined = true
		lease.metadata.State = LeaseQuarantined
		if err := lease.manager.updateLease(lease.leaseFile, &lease.metadata); err != nil {
			return lease.metrics, err
		}
		if err := lease.manager.runReleaseHook("worker-quarantined"); err != nil {
			return lease.metrics, err
		}
	}
	if !lease.metadata.WorkerDeleted {
		if pathExists(quarantine) {
			if err := verifyWorkerOwner(quarantine, lease.metadata); err != nil {
				return lease.metrics, err
			}
			if err := os.RemoveAll(quarantine); err != nil {
				return lease.metrics, newError(CodeCleanupFailed, "delete-worker", quarantine, err)
			}
		} else if !lease.metadata.WorkerOwnerVerified || !lease.metadata.WorkerQuarantined {
			return lease.metrics, newError(CodeOwnershipMismatch, "resume-worker-delete", quarantine, fmt.Errorf("missing ownership evidence"))
		}
		lease.metadata.WorkerDeleted = true
		if err := lease.manager.updateLease(lease.leaseFile, &lease.metadata); err != nil {
			return lease.metrics, err
		}
		if err := lease.manager.runReleaseHook("worker-deleted"); err != nil {
			return lease.metrics, err
		}
	}
	if !lease.metadata.ActiveUseReleased {
		if err := lease.releaseActive(); err != nil {
			return lease.metrics, err
		}
		lease.metadata.ActiveUseReleased = true
		if err := lease.manager.updateLease(lease.leaseFile, &lease.metadata); err != nil {
			return lease.metrics, err
		}
		if err := lease.manager.runReleaseHook("active-use-released"); err != nil {
			return lease.metrics, err
		}
	}
	lease.metadata.State = LeaseReleased
	if err := lease.manager.updateLease(lease.leaseFile, &lease.metadata); err != nil {
		return lease.metrics, err
	}
	if err := lease.manager.runReleaseHook("released"); err != nil {
		return lease.metrics, err
	}
	if err := lease.manager.runReleaseHook("before-lease-delete"); err != nil {
		return lease.metrics, err
	}
	if err := lease.manager.removeLease(lease.leaseFile); err != nil && !os.IsNotExist(err) {
		return lease.metrics, newError(CodeCleanupFailed, "remove-worker-lease", lease.leaseFile, err)
	}
	lease.released = true
	if err := lease.manager.runReleaseHook("lease-deleted"); err != nil {
		return lease.metrics, err
	}
	lease.metrics.CleanupMs = time.Since(started).Milliseconds()
	return lease.metrics, nil
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func (manager *WorkerManager) runReleaseHook(stage string) error {
	if manager.releaseHook == nil {
		return nil
	}
	if err := manager.releaseHook(stage); err != nil {
		return newError(CodeCleanupFailed, "release-injection-"+stage, "", err)
	}
	return nil
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
		if json.Unmarshal(data, &metadata) != nil || metadata.SchemaVersion != LeaseSchemaVersion || metadata.OwnershipToken == "" || !leaseIDPattern.MatchString(metadata.LeaseID) || !validLeaseState(metadata.State) {
			return 0, newError(CodeOrphanFound, "decode-worker-lease", entry.Name(), fmt.Errorf("invalid lease metadata"))
		}
		if metadata.LeaseID == requestedLeaseID {
			return 0, newError(CodeLeaseConflict, "duplicate-worker-lease", requestedLeaseID, nil)
		}
		if metadata.State != LeaseReleased {
			if metadata.PID <= 0 || metadata.ReservedBytes <= 0 {
				return 0, newError(CodeOrphanFound, "validate-worker-lease", metadata.LeaseID, fmt.Errorf("invalid pid or reservation"))
			}
			if !manager.processAlive(metadata.PID) {
				return 0, newError(CodeOrphanFound, "orphan-worker-lease", metadata.LeaseID, fmt.Errorf("pid %d is not active", metadata.PID))
			}
			if metadata.ReservedBytes > int64(^uint64(0)>>1)-reservations {
				return 0, newError(CodeOrphanFound, "sum-worker-reservations", metadata.LeaseID, fmt.Errorf("reservation overflow"))
			}
			reservations += metadata.ReservedBytes
		}
	}
	return reservations, nil
}

func validLeaseState(state LeaseState) bool {
	switch state {
	case LeaseRequested, LeaseCloning, LeaseReady, LeaseRunning, LeaseReleasing, LeaseReleased, LeaseQuarantined, LeaseUnknown:
		return true
	default:
		return false
	}
}

func (manager *WorkerManager) updateLease(path string, metadata *WorkerMetadata) error {
	metadata.UpdatedAt = manager.now().UTC()
	if manager.updateLeaseHook != nil {
		if err := manager.updateLeaseHook(metadata); err != nil {
			return newError(CodeLeaseConflict, "update-worker-lease", path, err)
		}
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return newError(CodeLeaseConflict, "update-worker-lease", path, err)
	}
	data = append(data, '\n')
	if err := atomicfile.WriteDurable(path, data, 0600); err != nil {
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
		CloneTreeMs:                clone.CloneTreeMs,
		ClonedFileCount:            clone.ClonedFileCount,
		ClonedBytes:                clone.ClonedBytes,
		PhysicalCopiedFileCount:    clone.PhysicalCopiedFileCount,
		PhysicalCopiedBytes:        clone.PhysicalCopiedBytes,
		TailCopiedBytes:            clone.TailCopiedBytes,
		SparseFileCount:            clone.SparseFileCount,
		SparseLogicalBytes:         clone.SparseLogicalBytes,
		SparseAllocatedSourceBytes: clone.SparseAllocatedSourceBytes,
		SparseClonedBytes:          clone.SparseClonedBytes,
		SparseHoleBytes:            clone.SparseHoleBytes,
	}
}

func createJSONExclusive(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.WriteExclusiveDurable(path, data, 0600)
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
