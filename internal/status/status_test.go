package status_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/fastplay-runner/internal/status"
)

func TestWrite_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fastplay-status.json")
	w := status.NewWriter(path)
	if err := w.Write(status.Status{Phase: status.PhaseWaiting}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("status file was not created")
	}
}

func TestWrite_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fastplay-status.json")
	w := status.NewWriter(path)
	_ = w.Write(status.Status{Phase: status.PhaseCompiling, RunID: "20250301-102200"})

	data, _ := os.ReadFile(path)
	var s status.Status
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if s.Phase != status.PhaseCompiling {
		t.Errorf("got phase %q", s.Phase)
	}
}

func TestWrite_IsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fastplay-status.json")
	w := status.NewWriter(path)

	// Write twice; no temp file should linger
	_ = w.Write(status.Status{Phase: status.PhaseWaiting})
	_ = w.Write(status.Status{Phase: status.PhaseRunning})

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected only status file, found %d files: %v", len(entries), names)
	}
}

func TestWrite_ContainsSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fastplay-status.json")
	w := status.NewWriter(path)
	_ = w.Write(status.Status{Phase: status.PhaseRunning})

	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte(`"schema_version"`)) {
		t.Error("status JSON must contain schema_version")
	}
}
