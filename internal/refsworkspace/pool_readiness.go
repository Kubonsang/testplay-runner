package refsworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

const (
	mountedPoolReadyTimeout = 20 * time.Second
	mountedPoolReadyPoll    = 150 * time.Millisecond
)

type mountedPoolReadinessInspector interface {
	Lstat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
	IsReparsePoint(string) (bool, error)
}

type osMountedPoolReadinessInspector struct{}

func (osMountedPoolReadinessInspector) Lstat(path string) (fs.FileInfo, error) {
	return os.Lstat(path)
}

func (osMountedPoolReadinessInspector) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (osMountedPoolReadinessInspector) IsReparsePoint(path string) (bool, error) {
	return inspectPathReparse(path)
}

type mountedPoolReadinessOptions struct {
	Timeout      time.Duration
	PollInterval time.Duration
}

func waitForMountedPoolReady(ctx context.Context, paths Paths, expected PoolMetadata, volume VolumeInfo, inspector mountedPoolReadinessInspector, options mountedPoolReadinessOptions) (time.Duration, error) {
	started := time.Now()
	if inspector == nil {
		inspector = osMountedPoolReadinessInspector{}
	}
	if options.Timeout <= 0 {
		options.Timeout = mountedPoolReadyTimeout
	}
	if options.PollInterval <= 0 {
		options.PollInterval = mountedPoolReadyPoll
	}
	if err := validateVolume(volume); err != nil {
		return time.Since(started), err
	}

	deadline := time.NewTimer(options.Timeout)
	defer deadline.Stop()
	var lastObserved error
	contentVisible := false
	for {
		ready, progressed, err := inspectMountedPoolReadiness(paths, expected, volume, inspector)
		contentVisible = contentVisible || progressed
		if ready {
			return time.Since(started), nil
		}
		if err != nil && !isRetryableMountedPoolReadinessError(err, contentVisible) {
			return time.Since(started), err
		}
		if err != nil {
			lastObserved = err
		}

		timer := time.NewTimer(options.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return time.Since(started), cancelled("wait-mounted-pool-metadata", paths.PoolFile, ctx.Err())
		case <-deadline.C:
			if !timer.Stop() {
				<-timer.C
			}
			return time.Since(started), newMountedPoolNotReadyError(paths, options.Timeout, lastObserved)
		case <-timer.C:
		}
	}
}

func inspectMountedPoolReadiness(paths Paths, expected PoolMetadata, volume VolumeInfo, inspector mountedPoolReadinessInspector) (bool, bool, error) {
	mountReparse, err := inspector.IsReparsePoint(paths.Mount)
	if err != nil {
		return false, false, err
	}
	if !mountReparse {
		return false, false, fs.ErrNotExist
	}

	progressed, err := validateReadyDirectory(paths.PoolRoot, inspector)
	if err != nil {
		return false, false, err
	}
	if !progressed {
		return false, false, fs.ErrNotExist
	}

	info, err := inspector.Lstat(paths.PoolFile)
	if err != nil {
		return false, true, err
	}
	reparse, reparseErr := inspector.IsReparsePoint(paths.PoolFile)
	if reparseErr != nil {
		return false, true, newError(CodePoolCorrupt, "inspect-mounted-pool-metadata", paths.PoolFile, reparseErr)
	}
	if reparse || !info.Mode().IsRegular() {
		return false, true, newError(CodeOwnershipMismatch, "wait-mounted-pool-metadata", paths.PoolFile, fmt.Errorf("pool metadata must be a regular non-reparse file"))
	}
	data, err := inspector.ReadFile(paths.PoolFile)
	if err != nil {
		return false, true, err
	}
	var metadata PoolMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return false, true, newError(CodePoolCorrupt, "decode-mounted-pool-metadata", paths.PoolFile, err)
	}
	if err := comparePoolIdentity(paths, expected, metadata, volume); err != nil {
		return false, true, err
	}
	for _, directory := range []string{paths.Baselines, paths.Workers, paths.Leases, paths.Quarantine} {
		visible, err := validateReadyDirectory(directory, inspector)
		if err != nil {
			return false, true, err
		}
		if !visible {
			return false, true, fs.ErrNotExist
		}
	}
	return true, true, nil
}

func validateReadyDirectory(path string, inspector mountedPoolReadinessInspector) (bool, error) {
	info, err := inspector.Lstat(path)
	if err != nil {
		return false, err
	}
	reparse, err := inspector.IsReparsePoint(path)
	if err != nil {
		return false, newError(CodePoolCorrupt, "inspect-mounted-pool-directory", path, err)
	}
	if reparse || !info.IsDir() {
		return false, newError(CodeOwnershipMismatch, "wait-mounted-pool-metadata", path, fmt.Errorf("managed directory must be a real non-reparse directory"))
	}
	return true, nil
}

func isRetryableMountedPoolReadinessError(err error, contentVisible bool) bool {
	if err == nil || ErrorCode(err) != "unknown" {
		return false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if !contentVisible && errors.Is(err, fs.ErrPermission) {
		return true
	}
	return platformTemporaryMountedPoolReadinessError(err, contentVisible)
}

func newMountedPoolNotReadyError(paths Paths, timeout time.Duration, lastObserved error) error {
	last := ""
	if lastObserved != nil {
		last = lastObserved.Error()
	}
	return &Error{
		Code: CodePoolMountNotReady, Operation: "wait-mounted-pool-metadata", Path: paths.PoolFile,
		Cause:     fmt.Errorf("mounted pool content was not ready within %s; last observed error: %v", timeout, lastObserved),
		MountPath: paths.Mount, PoolMetadataPath: paths.PoolFile, MountReadyTimeoutMs: timeout.Milliseconds(),
		LastObservedError: last,
	}
}
