package status_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/status"
)

// spyWriter records the last Status written to it.
type spyStatusWriter struct {
	last status.Status
}

func (s *spyStatusWriter) Write(st status.Status) error {
	s.last = st
	return nil
}

func TestManager_HeartbeatUpdatesLastHeartbeatAt(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	spy := &spyStatusWriter{}
	mgr := status.NewManager(spy, status.NewEventLog(eventsPath))

	// First write to establish run context.
	if err := mgr.Write(status.Status{
		Phase:     status.PhaseCompiling,
		RunID:     "20260326-120000",
		StartedAt: "2026-03-26T12:00:00Z",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Truncate to second precision because RFC3339 has second granularity.
	before := time.Now().Truncate(time.Second)
	if err := mgr.Heartbeat(); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	if spy.last.LastHeartbeatAt == "" {
		t.Error("last_heartbeat_at must be non-empty after Heartbeat")
	}
	hbTime, parseErr := time.Parse(time.RFC3339, spy.last.LastHeartbeatAt)
	if parseErr != nil {
		t.Fatalf("last_heartbeat_at not valid RFC3339: %v", parseErr)
	}
	// hbTime must be within a 5-second window around before (generous for slow CI).
	if hbTime.Before(before.Add(-5*time.Second)) || hbTime.After(before.Add(5*time.Second)) {
		t.Errorf("last_heartbeat_at %q is not near expected time %v", spy.last.LastHeartbeatAt, before)
	}
}

func TestManager_HeartbeatPreservesPhase(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	spy := &spyStatusWriter{}
	mgr := status.NewManager(spy, status.NewEventLog(eventsPath))

	_ = mgr.Write(status.Status{Phase: status.PhaseRunning, RunID: "run1"})
	_ = mgr.Heartbeat()

	if spy.last.Phase != status.PhaseRunning {
		t.Errorf("Heartbeat must not change phase; got %q", spy.last.Phase)
	}
}

func TestManager_HeartbeatNoOpBeforeFirstWrite(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	spy := &spyStatusWriter{}
	mgr := status.NewManager(spy, status.NewEventLog(eventsPath))

	// Heartbeat before any Write must be a no-op (no panic, no snapshot write).
	if err := mgr.Heartbeat(); err != nil {
		t.Fatalf("expected no error for no-op Heartbeat, got: %v", err)
	}
	if spy.last.Phase != "" {
		t.Error("snapshot must not be written for no-op Heartbeat")
	}
}

func TestManager_WriteInheritsStartedAt(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	spy := &spyStatusWriter{}
	mgr := status.NewManager(spy, status.NewEventLog(eventsPath))

	// First write sets StartedAt.
	_ = mgr.Write(status.Status{
		Phase:     status.PhaseCompiling,
		RunID:     "run1",
		StartedAt: "2026-03-26T12:00:00Z",
	})

	// Second write does not repeat StartedAt — should be inherited.
	_ = mgr.Write(status.Status{
		Phase: status.PhaseRunning,
		RunID: "run1",
	})

	if spy.last.StartedAt != "2026-03-26T12:00:00Z" {
		t.Errorf("StartedAt must be inherited across writes, got %q", spy.last.StartedAt)
	}
}

func TestManager_WritePreservesArtifactRootAndPID(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	spy := &spyStatusWriter{}
	mgr := status.NewManager(spy, status.NewEventLog(eventsPath))

	_ = mgr.Write(status.Status{
		Phase:        status.PhaseCompiling,
		RunID:        "run1",
		PID:          42,
		ArtifactRoot: "/tmp/runs/run1",
	})
	_ = mgr.Write(status.Status{Phase: status.PhaseDone, RunID: "run1"})

	if spy.last.PID != 42 {
		t.Errorf("PID must be inherited; got %d", spy.last.PID)
	}
	if spy.last.ArtifactRoot != "/tmp/runs/run1" {
		t.Errorf("ArtifactRoot must be inherited; got %q", spy.last.ArtifactRoot)
	}
}

func TestManager_HeartbeatDoesNotAppendEvent(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	spy := &spyStatusWriter{}
	mgr := status.NewManager(spy, status.NewEventLog(eventsPath))

	_ = mgr.Write(status.Status{Phase: status.PhaseRunning, RunID: "run1"})
	_ = mgr.Heartbeat()

	// Count lines in events.ndjson — heartbeat must not add one.
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("events.ndjson not written: %v", err)
	}
	var lineCount int
	for _, b := range data {
		if b == '\n' {
			lineCount++
		}
	}
	// Only the one Write event should exist.
	if lineCount != 1 {
		t.Errorf("expected 1 event line (from Write), got %d; heartbeat must not append", lineCount)
	}
}

func TestManager_SnapshotContainsHeartbeatField(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.json")
	eventsPath := filepath.Join(dir, "events.ndjson")
	mgr := status.NewManager(status.NewWriter(statusPath), status.NewEventLog(eventsPath))

	_ = mgr.Write(status.Status{Phase: status.PhaseRunning, RunID: "run1"})
	_ = mgr.Heartbeat()

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("status.json not written: %v", err)
	}
	var snap map[string]any
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if snap["last_heartbeat_at"] == nil {
		t.Error("status.json must contain last_heartbeat_at after Heartbeat")
	}
}
