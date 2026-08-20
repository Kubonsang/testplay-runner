package refsworkspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type releasedWorkerRecoveryFixture struct {
	native     *fakePoolNative
	service    *Service
	config     Config
	paths      Paths
	key        CompatibilityKey
	leaseID    string
	markerPath string
}

func newReleasedWorkerRecoveryFixture(t *testing.T) releasedWorkerRecoveryFixture {
	t.Helper()
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	service.runningProcesses = func([]string) ([]string, error) { return nil, nil }
	service.inspectUnmounted = func(string) (int, int, error) { return 0, 0, nil }
	config := Config{Root: filepath.Join(t.TempDir(), "storage")}
	setup, err := service.Setup(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	paths := setup.Paths
	t.Cleanup(func() { _ = makeWritableTree(paths.Baselines) })
	key := testCompatibilityKey("c")
	store := NewLibraryBaselineStore(paths)
	if _, _, _, err := store.Ensure(context.Background(), key, func(_ context.Context, libraryPath string) error {
		return os.WriteFile(filepath.Join(libraryPath, "artifact.bin"), []byte(strings.Repeat("x", 8193)), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	leaseID := "lease-recovery1"
	if _, err := store.AcquireUse(context.Background(), key, leaseID); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(paths.Leases, "active-"+key.Digest+"-"+leaseID+".json")
	// Setup performs two detach cycles. Recovery, Status, and Remove are the
	// next three; only Remove should expose an empty fake mount directory.
	native.emptyMountOnCloseAt = map[int]bool{5: true}
	native.afterClose = func(count int, _ string) {
		if count == 5 {
			_ = makeWritableTree(paths.PoolRoot)
		}
	}
	return releasedWorkerRecoveryFixture{native: native, service: service, config: config, paths: paths, key: key, leaseID: leaseID, markerPath: markerPath}
}

func TestRecoverExactReleasedWorkerResidualSucceeds(t *testing.T) {
	fixture := newReleasedWorkerRecoveryFixture(t)
	result, err := fixture.service.RecoverReleasedWorkerResidual(context.Background(), fixture.config, fixture.key.Digest, fixture.leaseID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "RECOVERED" || result.Operation != "recover-released-worker-residual" || result.Residual.Status != "MEASURED_ZERO" || result.ReleasedWorkerRecovery == nil || !result.ReleasedWorkerRecovery.DurableAbsenceVerified || !result.ReleasedWorkerRecovery.PoolRemoved {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{fixture.paths.VHDX, fixture.paths.Owner, fixture.paths.Mount, fixture.paths.Root} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("recovery left %s: %v", path, statErr)
		}
	}
}

func TestRecoverReleasedWorkerResidualRefusesUnsafeShapes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*releasedWorkerRecoveryFixture)
		key    func(releasedWorkerRecoveryFixture) string
		lease  func(releasedWorkerRecoveryFixture) string
	}{
		{name: "key mismatch", key: func(f releasedWorkerRecoveryFixture) string { return strings.Repeat("f", 64) }},
		{name: "lease mismatch", lease: func(releasedWorkerRecoveryFixture) string { return "lease-mismatch1" }},
		{name: "worker journal", mutate: func(f *releasedWorkerRecoveryFixture) {
			_ = os.WriteFile(filepath.Join(f.paths.Leases, "worker-"+f.leaseID+".json"), []byte("{}"), 0600)
		}},
		{name: "worker directory", mutate: func(f *releasedWorkerRecoveryFixture) { _ = os.Mkdir(filepath.Join(f.paths.Workers, f.leaseID), 0700) }},
		{name: "multiple markers", mutate: func(f *releasedWorkerRecoveryFixture) {
			marker := activeUse{SchemaVersion: LeaseSchemaVersion, KeyDigest: f.key.Digest, LeaseID: "lease-recovery2", OwnershipToken: strings.Repeat("a", 64)}
			data, _ := json.Marshal(marker)
			_ = os.WriteFile(filepath.Join(f.paths.Leases, "active-"+f.key.Digest+"-lease-recovery2.json"), data, 0600)
		}},
		{name: "related process", mutate: func(f *releasedWorkerRecoveryFixture) {
			f.service.runningProcesses = func([]string) ([]string, error) { return []string{"Unity"}, nil }
		}},
		{name: "unknown artifact", mutate: func(f *releasedWorkerRecoveryFixture) {
			_ = os.WriteFile(filepath.Join(f.paths.Leases, "foreign.tmp"), nil, 0600)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReleasedWorkerRecoveryFixture(t)
			if test.mutate != nil {
				test.mutate(&fixture)
			}
			key, lease := fixture.key.Digest, fixture.leaseID
			if test.key != nil {
				key = test.key(fixture)
			}
			if test.lease != nil {
				lease = test.lease(fixture)
			}
			if _, err := fixture.service.RecoverReleasedWorkerResidual(context.Background(), fixture.config, key, lease); err == nil {
				t.Fatal("unsafe recovery unexpectedly succeeded")
			}
			if _, statErr := os.Lstat(fixture.markerPath); statErr != nil {
				t.Fatalf("recovery changed exact marker: %v", statErr)
			}
			if _, statErr := os.Lstat(fixture.paths.VHDX); statErr != nil {
				t.Fatalf("recovery deleted VHDX: %v", statErr)
			}
		})
	}
}

func TestRecoverReleasedWorkerResidualPreservesPoolWhenMarkerReappears(t *testing.T) {
	fixture := newReleasedWorkerRecoveryFixture(t)
	markerData, err := os.ReadFile(fixture.markerPath)
	if err != nil {
		t.Fatal(err)
	}
	originalAfterClose := fixture.native.afterClose
	fixture.native.afterClose = func(count int, mount string) {
		originalAfterClose(count, mount)
		if count == 3 {
			_ = os.WriteFile(fixture.markerPath, markerData, 0600)
		}
	}
	_, err = fixture.service.RecoverReleasedWorkerResidual(context.Background(), fixture.config, fixture.key.Digest, fixture.leaseID)
	if ErrorCode(err) != CodeWorkerReleasePersistenceFailed {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Lstat(fixture.paths.VHDX); statErr != nil {
		t.Fatalf("persistence failure deleted VHDX: %v", statErr)
	}
}
