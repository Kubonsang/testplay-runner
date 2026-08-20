package refsworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSetupTransactionCommitsOwnerOnlyAfterDurabilityProof(t *testing.T) {
	native := newFakePoolNative()
	service := NewService(native, copyClaimingCloner{})
	var events []string
	service.recordEvent = func(event string) { events = append(events, event) }
	result, err := service.Setup(context.Background(), Config{Root: filepath.Join(t.TempDir(), "storage")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pending-owner-create", "vhdx-create", "initial-mount",
		"pool-metadata-write", "pool-metadata-flush-read-back", "first-detach",
		"second-attach", "durable-pool-verification", "second-detach",
		"authoritative-owner-commit", "setup-pass",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
	transaction := result.SetupTransaction
	if transaction == nil || !transaction.PendingOwnerCreated || !transaction.VHDXCreated ||
		!transaction.PoolMetadataWritten || !transaction.PoolMetadataFlushed || !transaction.PoolMetadataReadBack ||
		!transaction.VolumeFlushed || !transaction.InitialMount.Detached || !transaction.DurabilityReattach.Mounted ||
		!transaction.DurabilityReattach.MetadataVisible || !transaction.DurabilityReattach.LayoutVerified ||
		!transaction.DurabilityReattach.Detached || !transaction.DurabilityVerified ||
		!transaction.AuthoritativeOwnerCommitted {
		t.Fatalf("transaction=%+v", transaction)
	}
	if _, err := os.Stat(result.Paths.Owner); err != nil {
		t.Fatalf("authoritative owner missing: %v", err)
	}
	if _, err := os.Stat(result.Paths.PendingOwner); !os.IsNotExist(err) {
		t.Fatalf("pending owner remains after commit: %v", err)
	}
}

func TestSetupPersistenceFailuresNeverCommitOwner(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakePoolNative, Paths)
	}{
		{
			name: "pool metadata disappears",
			mutate: func(native *fakePoolNative, paths Paths) {
				native.afterClose = func(count int, _ string) {
					if count == 1 {
						_ = os.Remove(paths.PoolFile)
					}
				}
			},
		},
		{
			name: "required layout disappears",
			mutate: func(native *fakePoolNative, paths Paths) {
				native.afterClose = func(count int, _ string) {
					if count == 1 {
						_ = os.RemoveAll(paths.Workers)
					}
				}
			},
		},
		{
			name: "ownership token changes",
			mutate: func(native *fakePoolNative, paths Paths) {
				native.afterClose = func(count int, _ string) {
					if count != 1 {
						return
					}
					metadata, err := readPoolMetadata(paths.PoolFile)
					if err == nil {
						metadata.OwnershipToken = "changed-after-detach"
						_ = writeJSONAtomic(paths.PoolFile, metadata, 0600)
					}
				}
			},
		},
		{
			name: "volume identity changes",
			mutate: func(native *fakePoolNative, _ Paths) {
				changed := native.volume
				changed.VolumeGUIDPath = `\\?\Volume{changed}\`
				native.mountVolumes = []VolumeInfo{native.volume, changed}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			native := newFakePoolNative()
			config := Config{Root: filepath.Join(t.TempDir(), "storage")}
			_, paths, err := NewPaths(config)
			if err != nil {
				t.Fatal(err)
			}
			native.emptyMountOnCloseAt = map[int]bool{2: true}
			test.mutate(native, paths)
			_, err = NewService(native, copyClaimingCloner{}).Setup(context.Background(), config)
			if ErrorCode(err) != CodePoolPersistenceVerificationFailed {
				t.Fatalf("err=%v", err)
			}
			var setupErr *Error
			if !errors.As(err, &setupErr) || setupErr.OwnerMetadataCommitted || setupErr.CleanupState != "released" || setupErr.ManualRecoveryRequired {
				t.Fatalf("evidence=%+v", setupErr)
			}
			if setupErr.SetupTransaction == nil || setupErr.SetupTransaction.AuthoritativeOwnerCommitted {
				t.Fatalf("transaction=%+v", setupErr.SetupTransaction)
			}
			for _, path := range []string{paths.Owner, paths.PendingOwner, paths.VHDX, paths.Root} {
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Fatalf("pre-commit failure retained %q: %v", path, statErr)
				}
			}
		})
	}
}

func TestPendingOwnerIsNotAcceptedAsNormalPool(t *testing.T) {
	native := newFakePoolNative()
	config := Config{Root: filepath.Join(t.TempDir(), "storage")}
	_, paths, err := NewPaths(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PendingOwner, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"setup", "status", "probe", "remove"} {
		var opErr error
		switch operation {
		case "setup":
			_, opErr = NewService(native, copyClaimingCloner{}).Setup(context.Background(), config)
		case "status":
			_, opErr = NewService(native, copyClaimingCloner{}).Status(context.Background(), config)
		case "probe":
			_, opErr = NewService(native, copyClaimingCloner{}).Probe(context.Background(), config)
		case "remove":
			_, opErr = NewService(native, copyClaimingCloner{}).Remove(context.Background(), config)
		}
		if ErrorCode(opErr) != CodeIncompleteSetup {
			t.Fatalf("%s err=%v", operation, opErr)
		}
	}
}

func TestSetupVHDXIdentityChangePreservesEvidenceAndRequiresRecovery(t *testing.T) {
	native := newFakePoolNative()
	config := Config{Root: filepath.Join(t.TempDir(), "storage")}
	_, paths, err := NewPaths(config)
	if err != nil {
		t.Fatal(err)
	}
	native.afterClose = func(count int, _ string) {
		if count == 1 {
			native.fileIdentity = "changed-volume:changed-file"
		}
	}
	_, err = NewService(native, copyClaimingCloner{}).Setup(context.Background(), config)
	var setupErr *Error
	if ErrorCode(err) != CodePoolPersistenceVerificationFailed || !errors.As(err, &setupErr) ||
		setupErr.CleanupState != "uncertain" || setupErr.OwnerMetadataCommitted || !setupErr.ManualRecoveryRequired {
		t.Fatalf("err=%v evidence=%+v", err, setupErr)
	}
	if _, statErr := os.Stat(paths.VHDX); statErr != nil {
		t.Fatalf("identity uncertainty deleted VHDX: %v", statErr)
	}
	if _, statErr := os.Stat(paths.PendingOwner); statErr != nil {
		t.Fatalf("identity uncertainty deleted pending owner: %v", statErr)
	}
	if _, statErr := os.Stat(paths.Owner); !os.IsNotExist(statErr) {
		t.Fatalf("identity uncertainty committed authoritative owner: %v", statErr)
	}
}
