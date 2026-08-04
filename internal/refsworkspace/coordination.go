package refsworkspace

import (
	"context"
	"errors"
	"os"
	"time"
)

// coordinationLock is a process-safe directory lock. It is intentionally not
// broken or aged out: an abandoned lock is evidence that must be recovered by
// an operator instead of being silently deleted.
type coordinationLock struct {
	path string
}

func acquireCoordinationLock(ctx context.Context, path, operation string) (*coordinationLock, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, cancelled(operation, path, err)
		}
		if err := os.Mkdir(path, 0700); err == nil {
			return &coordinationLock{path: path}, nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, newError(CodeLeaseConflict, operation, path, err)
		}
		select {
		case <-ctx.Done():
			return nil, cancelled(operation, path, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (lock *coordinationLock) release() error {
	if lock == nil {
		return nil
	}
	if err := os.Remove(lock.path); err != nil {
		return newError(CodeCleanupFailed, "release-coordination-lock", lock.path, err)
	}
	return nil
}
