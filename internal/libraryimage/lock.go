package libraryimage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const staleLockAge = 30 * time.Minute

type lockRecord struct {
	PID       int       `json:"pid"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"createdAt"`
}

type imageLock struct {
	path  string
	token string
}

func (s *Store) acquireLock(key Key) (*imageLock, error) {
	lockDir := filepath.Join(s.root, "locks")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return nil, fmt.Errorf("library image lock: create directory: %w", err)
	}
	path := filepath.Join(lockDir, key.Digest+".lock")

	for attempts := 0; attempts < 2; attempts++ {
		token, err := randomToken()
		if err != nil {
			return nil, fmt.Errorf("library image lock: token: %w", err)
		}
		record := lockRecord{PID: s.pid, Token: token, CreatedAt: s.now().UTC()}
		data, err := json.Marshal(record)
		if err != nil {
			return nil, fmt.Errorf("library image lock: encode: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			if _, writeErr := file.Write(data); writeErr != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("library image lock: write: %w", writeErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("library image lock: close: %w", closeErr)
			}
			return &imageLock{path: path, token: token}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("library image lock: create: %w", err)
		}

		existingData, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("%w: unreadable lock %s", errLockConflict, path)
		}
		var existing lockRecord
		if jsonErr := json.Unmarshal(existingData, &existing); jsonErr != nil {
			return nil, fmt.Errorf("%w: invalid lock %s", errLockConflict, path)
		}
		age := s.now().Sub(existing.CreatedAt)
		if existing.PID == s.pid || age < staleLockAge || s.processAlive(existing.PID) {
			return nil, fmt.Errorf("%w: pid=%d age=%s", errLockConflict, existing.PID, age.Round(time.Second))
		}

		// Re-read before removal so a replaced/live lock is never deleted based
		// on stale contents observed earlier.
		latest, latestErr := os.ReadFile(path)
		if latestErr != nil || string(latest) != string(existingData) {
			return nil, fmt.Errorf("%w: lock changed during stale check", errLockConflict)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("%w: remove stale lock: %v", errLockConflict, err)
		}
	}
	return nil, errLockConflict
}

func (l *imageLock) release() {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	var record lockRecord
	if json.Unmarshal(data, &record) != nil || record.Token != l.token {
		return
	}
	_ = os.Remove(l.path)
}

func randomToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
