package status_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/status"
)

func TestEventLog_Append_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.ndjson")
	log := status.NewEventLog(path)

	ev := status.Event{Event: "run_started", RunID: "20260326-120000", Phase: "compiling"}
	if err := log.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("events.ndjson not created: %v", err)
	}
}

func TestEventLog_Append_MultipleEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.ndjson")
	log := status.NewEventLog(path)

	events := []status.Event{
		{Event: "run_started", RunID: "20260326-120000", Phase: "compiling"},
		{Event: "phase_changed", RunID: "20260326-120000", Phase: "running"},
		{Event: "run_finished", RunID: "20260326-120000", Phase: "done"},
	}
	for _, ev := range events {
		if err := log.Append(ev); err != nil {
			t.Fatalf("Append(%s): %v", ev.Event, err)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open events.ndjson: %v", err)
	}
	defer f.Close()

	var lines []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("invalid JSON line: %v", err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 3 {
		t.Errorf("expected 3 event lines, got %d", len(lines))
	}
	for _, l := range lines {
		if l["timestamp"] == nil || l["timestamp"] == "" {
			t.Error("event must include timestamp")
		}
		if l["event"] == nil {
			t.Error("event must include event field")
		}
	}
}

func TestEventLog_Append_OrderPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.ndjson")
	log := status.NewEventLog(path)

	names := []string{"run_started", "phase_changed", "run_finished"}
	for _, name := range names {
		_ = log.Append(status.Event{Event: name})
	}

	f, _ := os.Open(path)
	defer f.Close()
	sc := bufio.NewScanner(f)
	i := 0
	for sc.Scan() {
		var m map[string]any
		json.Unmarshal(sc.Bytes(), &m)
		if m["event"] != names[i] {
			t.Errorf("line %d: event = %q, want %q", i, m["event"], names[i])
		}
		i++
	}
}

func TestManager_Write_UpdatesSnapshotAndAppendsEvent(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "testplay-status.json")
	eventsPath := filepath.Join(dir, "events.ndjson")

	mgr := status.NewManager(
		status.NewWriter(snapshotPath),
		status.NewEventLog(eventsPath),
	)

	if err := mgr.Write(status.Status{Phase: status.PhaseCompiling, RunID: "20260326-120000"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Snapshot must be updated
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}

	// Event log must have one entry
	f, _ := os.Open(eventsPath)
	defer f.Close()
	sc := bufio.NewScanner(f)
	var count int
	for sc.Scan() {
		count++
		var m map[string]any
		json.Unmarshal(sc.Bytes(), &m)
		if m["event"] != "run_started" {
			t.Errorf("compiling phase → event should be run_started, got %q", m["event"])
		}
		if m["run_id"] != "20260326-120000" {
			t.Errorf("event run_id = %v, want %q", m["run_id"], "20260326-120000")
		}
	}
	if count != 1 {
		t.Errorf("expected 1 event line, got %d", count)
	}
}

func TestManager_Write_RunFinished_ExitCode(t *testing.T) {
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.ndjson")
	mgr := status.NewManager(
		status.NewWriter(filepath.Join(dir, "status.json")),
		status.NewEventLog(eventsPath),
	)

	exitCode := 3
	if err := mgr.Write(status.Status{Phase: status.PhaseDone, ExitCode: &exitCode}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	f, _ := os.Open(eventsPath)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan()
	var m map[string]any
	json.Unmarshal(sc.Bytes(), &m)

	if m["event"] != "run_finished" {
		t.Errorf("event = %q, want run_finished", m["event"])
	}
	gotCode, ok := m["exit_code"].(float64)
	if !ok {
		t.Fatalf("exit_code missing or not a number: %v", m["exit_code"])
	}
	if int(gotCode) != exitCode {
		t.Errorf("exit_code = %v, want %d", gotCode, exitCode)
	}
}

func TestManager_Write_PhaseMapping(t *testing.T) {
	cases := []struct {
		phase     status.Phase
		wantEvent string
	}{
		{status.PhaseCompiling, "run_started"},
		{status.PhaseRunning, "phase_changed"},
		{status.PhaseDone, "run_finished"},
		{status.PhaseTimeoutCompile, "timeout"},
		{status.PhaseTimeoutTest, "timeout"},
		{status.PhaseTimeoutTotal, "timeout"},
		{status.PhaseInterrupted, "interrupted"},
	}

	for _, tc := range cases {
		t.Run(string(tc.phase), func(t *testing.T) {
			dir := t.TempDir()
			mgr := status.NewManager(
				status.NewWriter(filepath.Join(dir, "status.json")),
				status.NewEventLog(filepath.Join(dir, "events.ndjson")),
			)
			_ = mgr.Write(status.Status{Phase: tc.phase})

			f, _ := os.Open(filepath.Join(dir, "events.ndjson"))
			defer f.Close()
			sc := bufio.NewScanner(f)
			sc.Scan()
			var m map[string]any
			json.Unmarshal(sc.Bytes(), &m)
			if m["event"] != tc.wantEvent {
				t.Errorf("phase %s → event %q, want %q", tc.phase, m["event"], tc.wantEvent)
			}
		})
	}
}
