package refsworkspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func incompleteRecoveryFixture(t *testing.T) (*fakePoolNative, *Service, Config, Paths, PoolMetadata) {
	t.Helper()
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	config := Config{Root: filepath.Join(t.TempDir(), "storage")}
	config, paths, err := NewPaths(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Root, paths.Mount, paths.PoolRoot, paths.Baselines, paths.Workers, paths.Leases, paths.Quarantine} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(paths.VHDX, []byte("dynamic-vhdx"), 0600); err != nil {
		t.Fatal(err)
	}
	metadata := PoolMetadata{
		SchemaVersion: PoolSchemaVersion, Architecture: managedReFSArchitecture,
		WindowsProvider: WindowsProviderDevDriveVHDX, VolumeKind: VolumeKindDevDrive,
		OwnershipToken: "incomplete-owner", VHDXPath: paths.VHDX, VHDXIdentity: "fake-volume:fake-file",
		VolumeGUIDPath: native.volume.VolumeGUIDPath, Filesystem: "ReFS", ClusterSize: 4096,
		MaximumBytes: DefaultMaximumBytes, SoftBudgetBytes: DefaultSoftBudget, WorkerReserveBytes: DefaultReserveBytes,
		MinimumHostFreeBytes: DefaultMinimumHostFreeBytes, VHDXOverheadReserveBytes: DefaultVHDXOverheadReserveBytes,
	}
	if err := writeJSONAtomic(paths.Owner, metadata, 0600); err != nil {
		t.Fatal(err)
	}
	return native, service, config, paths, metadata
}

func TestRecoverExactIncompleteEmptyPoolSucceeds(t *testing.T) {
	native, service, config, paths, _ := incompleteRecoveryFixture(t)
	native.emptyMountOnClose = true
	result, err := service.RecoverIncompleteSetup(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "RECOVERED" || result.Operation != "recover-incomplete-setup" || result.Residual.Status != "MEASURED_ZERO" {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{paths.VHDX, paths.Owner, paths.PendingOwner, paths.Mount, paths.Root} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("recovery retained %q: %v", path, statErr)
		}
	}
}

func TestRecoverIncompletePoolRefusesUnsafeEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakePoolNative, Paths, *PoolMetadata) error
		code   string
	}{
		{name: "vhdx identity mismatch", code: CodeOwnershipMismatch, mutate: func(_ *fakePoolNative, _ Paths, metadata *PoolMetadata) error {
			metadata.VHDXIdentity = "other-volume:other-file"
			return nil
		}},
		{name: "volume guid mismatch", code: CodeOwnershipMismatch, mutate: func(native *fakePoolNative, _ Paths, _ *PoolMetadata) error {
			native.volume.VolumeGUIDPath = `\\?\Volume{other}\`
			return nil
		}},
		{name: "unknown root entry", code: CodeIncompleteSetup, mutate: func(_ *fakePoolNative, paths Paths, _ *PoolMetadata) error {
			return os.WriteFile(filepath.Join(paths.Mount, "unknown.bin"), []byte("unknown"), 0600)
		}},
		{name: "unknown testplay entry", code: CodeIncompleteSetup, mutate: func(_ *fakePoolNative, paths Paths, _ *PoolMetadata) error {
			return os.WriteFile(filepath.Join(paths.PoolRoot, "unknown.bin"), []byte("unknown"), 0600)
		}},
		{name: "non-empty worker", code: CodeIncompleteSetup, mutate: func(_ *fakePoolNative, paths Paths, _ *PoolMetadata) error {
			return os.WriteFile(filepath.Join(paths.Workers, "worker.bin"), []byte("worker"), 0600)
		}},
		{name: "active lease", code: CodeIncompleteSetup, mutate: func(_ *fakePoolNative, paths Paths, _ *PoolMetadata) error {
			return os.WriteFile(filepath.Join(paths.Leases, "active-test.json"), []byte("{}"), 0600)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			native, service, config, paths, metadata := incompleteRecoveryFixture(t)
			if err := test.mutate(native, paths, &metadata); err != nil {
				t.Fatal(err)
			}
			if err := writeJSONAtomic(paths.Owner, metadata, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := service.RecoverIncompleteSetup(context.Background(), config)
			if ErrorCode(err) != test.code {
				t.Fatalf("err=%v", err)
			}
			for _, path := range []string{paths.VHDX, paths.Owner} {
				if _, statErr := os.Stat(path); statErr != nil {
					t.Fatalf("refused recovery changed %q: %v", path, statErr)
				}
			}
		})
	}
}

func TestRecoverAcceptsOnlyStrictKnownSyntheticProbe(t *testing.T) {
	native, service, config, paths, _ := incompleteRecoveryFixture(t)
	probe := filepath.Join(paths.PoolRoot, ".block-clone-probe-012345abcdef")
	for _, directory := range []string{filepath.Join(probe, "source"), filepath.Join(probe, "destination")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"payload.bin", "sparse.bin"} {
			if err := os.WriteFile(filepath.Join(directory, file), []byte("probe"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	native.emptyMountOnClose = true
	if _, err := service.RecoverIncompleteSetup(context.Background(), config); err != nil {
		t.Fatal(err)
	}
}

func TestNormalRemoveStillRefusesMissingPoolMetadata(t *testing.T) {
	_, service, config, paths, _ := incompleteRecoveryFixture(t)
	_, err := service.Remove(context.Background(), config)
	if err == nil {
		t.Fatal("normal remove accepted incomplete pool")
	}
	for _, path := range []string{paths.VHDX, paths.Owner} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("normal remove changed %q: %v", path, statErr)
		}
	}
}
