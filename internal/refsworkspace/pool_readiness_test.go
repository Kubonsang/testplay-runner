package refsworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeReadinessInspector struct {
	mount   string
	mu      sync.RWMutex
	reparse map[string]bool
}

func (inspector *fakeReadinessInspector) Lstat(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}
func (inspector *fakeReadinessInspector) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
func (inspector *fakeReadinessInspector) IsReparsePoint(path string) (bool, error) {
	inspector.mu.RLock()
	defer inspector.mu.RUnlock()
	clean := strings.ToLower(filepath.Clean(path))
	return clean == strings.ToLower(filepath.Clean(inspector.mount)) || inspector.reparse[clean], nil
}
func (inspector *fakeReadinessInspector) setReparse(path string) {
	inspector.mu.Lock()
	defer inspector.mu.Unlock()
	inspector.reparse[strings.ToLower(filepath.Clean(path))] = true
}

func readinessFixture(t *testing.T) (Paths, PoolMetadata, VolumeInfo, *fakeReadinessInspector) {
	t.Helper()
	paths, host, _, volume := validPolicyEvidence(t)
	if err := os.MkdirAll(paths.Mount, 0700); err != nil {
		t.Fatal(err)
	}
	return paths, host, volume, &fakeReadinessInspector{mount: paths.Mount, reparse: map[string]bool{}}
}

func writeReadyPool(t *testing.T, paths Paths, metadata PoolMetadata, includeManagedDirectories bool) {
	t.Helper()
	if err := os.MkdirAll(paths.PoolRoot, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PoolFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	if includeManagedDirectories {
		for _, path := range []string{paths.Baselines, paths.Workers, paths.Leases, paths.Quarantine} {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestMountedPoolReadinessWaitsForMetadataAndDirectories(t *testing.T) {
	for _, test := range []struct {
		name          string
		createInitial bool
	}{
		{name: "pool metadata appears after mount"},
		{name: "required directories appear after metadata", createInitial: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths, host, volume, inspector := readinessFixture(t)
			if test.createInitial {
				writeReadyPool(t, paths, host, false)
			}
			created := make(chan struct{})
			go func() {
				time.Sleep(35 * time.Millisecond)
				if !test.createInitial {
					_ = os.MkdirAll(paths.PoolRoot, 0700)
					data, _ := json.Marshal(host)
					_ = os.WriteFile(paths.PoolFile, data, 0600)
				}
				for _, path := range []string{paths.Baselines, paths.Workers, paths.Leases, paths.Quarantine} {
					_ = os.MkdirAll(path, 0700)
				}
				close(created)
			}()
			elapsed, err := waitForMountedPoolReady(context.Background(), paths, host, volume, inspector, mountedPoolReadinessOptions{Timeout: 300 * time.Millisecond, PollInterval: 5 * time.Millisecond})
			<-created
			if err != nil || elapsed < 25*time.Millisecond {
				t.Fatalf("elapsed=%s err=%v", elapsed, err)
			}
		})
	}
}

func TestMountedPoolReadinessTimesOutWithStructuredEvidence(t *testing.T) {
	paths, host, volume, inspector := readinessFixture(t)
	elapsed, err := waitForMountedPoolReady(context.Background(), paths, host, volume, inspector, mountedPoolReadinessOptions{Timeout: 45 * time.Millisecond, PollInterval: 5 * time.Millisecond})
	var readyErr *Error
	if !errors.As(err, &readyErr) || readyErr.Code != CodePoolMountNotReady || readyErr.Operation != "wait-mounted-pool-metadata" {
		t.Fatalf("err=%v", err)
	}
	if elapsed < 35*time.Millisecond || readyErr.MountPath != paths.Mount || readyErr.PoolMetadataPath != paths.PoolFile || readyErr.MountReadyTimeoutMs != 45 || readyErr.LastObservedError == "" {
		t.Fatalf("elapsed=%s evidence=%+v", elapsed, readyErr)
	}
}

func TestMountedPoolReadinessRespectsCancellation(t *testing.T) {
	paths, host, volume, inspector := readinessFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitForMountedPoolReady(ctx, paths, host, volume, inspector, mountedPoolReadinessOptions{Timeout: time.Second, PollInterval: 5 * time.Millisecond})
	if ErrorCode(err) != CodeCancelled {
		t.Fatalf("err=%v", err)
	}
}

func TestMountedPoolReadinessRejectsCorruptionWithoutWaiting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, paths Paths, inspector *fakeReadinessInspector)
	}{
		{name: "invalid pool metadata", mutate: func(t *testing.T, paths Paths, _ *fakeReadinessInspector) {
			if err := os.WriteFile(paths.PoolFile, []byte("not-json"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "pool metadata reparse", mutate: func(_ *testing.T, paths Paths, inspector *fakeReadinessInspector) {
			inspector.setReparse(paths.PoolFile)
		}},
		{name: "pool root reparse", mutate: func(_ *testing.T, paths Paths, inspector *fakeReadinessInspector) {
			inspector.setReparse(paths.PoolRoot)
		}},
		{name: "required directory reparse", mutate: func(_ *testing.T, paths Paths, inspector *fakeReadinessInspector) {
			inspector.setReparse(paths.Workers)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths, host, volume, inspector := readinessFixture(t)
			writeReadyPool(t, paths, host, true)
			test.mutate(t, paths, inspector)
			started := time.Now()
			_, err := waitForMountedPoolReady(context.Background(), paths, host, volume, inspector, mountedPoolReadinessOptions{Timeout: 250 * time.Millisecond, PollInterval: 5 * time.Millisecond})
			if err == nil || ErrorCode(err) == CodePoolMountNotReady || time.Since(started) > 100*time.Millisecond {
				t.Fatalf("err=%v elapsed=%s", err, time.Since(started))
			}
		})
	}
}
