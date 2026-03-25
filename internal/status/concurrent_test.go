package status_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/status"
)

func TestWrite_ConcurrentWrites_NoRaceCondition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fastplay-status.json")
	w := status.NewWriter(path)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Write(status.Status{Phase: status.PhaseRunning})
		}()
	}
	wg.Wait()

	// File must be valid JSON after all writes
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read status file: %v", err)
	}
	var s status.Status
	if err := json.Unmarshal(data, &s); err != nil {
		t.Errorf("corrupted JSON after concurrent writes: %v\ndata: %s", err, data)
	}
}
