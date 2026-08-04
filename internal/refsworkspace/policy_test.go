package refsworkspace

import (
	"context"
	"path/filepath"
	"testing"
)

func validPolicyEvidence(t *testing.T) (Paths, PoolMetadata, PoolMetadata, VolumeInfo) {
	t.Helper()
	_, paths, err := NewPaths(Config{Root: filepath.Join(t.TempDir(), "storage")})
	if err != nil {
		t.Fatal(err)
	}
	host := PoolMetadata{
		SchemaVersion: PoolSchemaVersion, Architecture: managedReFSArchitecture,
		OwnershipToken: "owner", VHDXPath: paths.VHDX, VHDXIdentity: "disk:file",
		VolumeGUIDPath: `\\?\Volume{verified}\`, Filesystem: "ReFS", ClusterSize: 4096,
		MaximumBytes: 16 << 30, SoftBudgetBytes: 14 << 30, WorkerReserveBytes: 2 << 30,
		MinimumHostFreeBytes: 30 << 30, VHDXOverheadReserveBytes: 2 << 30,
	}
	volume := VolumeInfo{VolumeGUIDPath: host.VolumeGUIDPath, Filesystem: "ReFS", ClusterSize: 4096, SupportsBlockCloning: true}
	return paths, host, host, volume
}

func TestBuildVerifiedPoolPolicyRejectsEveryMetadataMismatch(t *testing.T) {
	mutations := map[string]func(*PoolMetadata){
		"schema":       func(value *PoolMetadata) { value.SchemaVersion++ },
		"architecture": func(value *PoolMetadata) { value.Architecture = "other" },
		"owner":        func(value *PoolMetadata) { value.OwnershipToken = "other" },
		"vhdx path":    func(value *PoolMetadata) { value.VHDXPath += ".other" },
		"vhdx identity": func(value *PoolMetadata) {
			value.VHDXIdentity = "other"
		},
		"volume guid": func(value *PoolMetadata) { value.VolumeGUIDPath = `\\?\Volume{other}\` },
		"filesystem":  func(value *PoolMetadata) { value.Filesystem = "NTFS" },
		"cluster":     func(value *PoolMetadata) { value.ClusterSize *= 2 },
		"maximum":     func(value *PoolMetadata) { value.MaximumBytes++ },
		"soft budget": func(value *PoolMetadata) { value.SoftBudgetBytes-- },
		"worker reserve": func(value *PoolMetadata) {
			value.WorkerReserveBytes--
		},
		"host floor": func(value *PoolMetadata) { value.MinimumHostFreeBytes-- },
		"overhead":   func(value *PoolMetadata) { value.VHDXOverheadReserveBytes++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			paths, host, pool, volume := validPolicyEvidence(t)
			mutate(&pool)
			if _, err := BuildVerifiedPoolPolicy(host, pool, volume); ErrorCode(err) != CodePoolCorrupt {
				t.Fatalf("build err=%v", err)
			}
			if err := comparePoolIdentity(paths, host, pool, volume); ErrorCode(err) != CodePoolCorrupt {
				t.Fatalf("compare err=%v", err)
			}
		})
	}
}

func TestNewVerifiedWorkerManagerOwnsPolicyForAcquire(t *testing.T) {
	paths, host, pool, volume := validPolicyEvidence(t)
	for _, path := range []string{paths.Baselines, paths.Workers, paths.Leases, paths.Quarantine} {
		if _, err := PrepareOwnedRoot(path); err != nil {
			t.Fatal(err)
		}
	}
	store := NewLibraryBaselineStore(paths)
	manager, err := NewVerifiedWorkerManager(paths, store, copyClaimingCloner{}, symlinkJunctioner{}, host, pool, volume)
	if err != nil {
		t.Fatal(err)
	}
	manager.storage = &fakeWorkerStorageMeter{hostFree: 100 << 30}
	if manager.policy.WorkerReserveBytes != pool.WorkerReserveBytes || manager.policy.ClusterSize != volume.ClusterSize {
		t.Fatalf("policy=%+v", manager.policy)
	}
	_, _, err = manager.Acquire(context.Background(), WorkerRequest{
		Key: testCompatibilityKey("a"), LeaseID: "lease-verified", JunctionPath: filepath.Join(t.TempDir(), "Library"),
	})
	if ErrorCode(err) != CodeBaselineMissing {
		t.Fatalf("verified-policy acquire err=%v", err)
	}
}

func TestWorkerHostFloorPolicyOverflowFailsClosed(t *testing.T) {
	paths := testPoolPaths(t)
	policy := testWorkerPolicy()
	policy.MinimumHostFreeBytes = int64(^uint64(0) >> 1)
	manager := newWorkerManager(paths, NewLibraryBaselineStore(paths), copyClaimingCloner{}, symlinkJunctioner{}, policy, &fakeWorkerStorageMeter{hostFree: policy.MinimumHostFreeBytes})
	_, _, err := manager.Acquire(context.Background(), WorkerRequest{Key: testCompatibilityKey("b"), LeaseID: "lease-overflow", JunctionPath: filepath.Join(t.TempDir(), "Library")})
	if ErrorCode(err) != CodeHostFreeSpaceFloor {
		t.Fatalf("err=%v", err)
	}
}
