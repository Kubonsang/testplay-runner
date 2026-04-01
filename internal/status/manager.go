package status

import (
	"sync"
	"time"
)

// Manager combines a snapshot Writer with an append-only EventLog.
// Each Write call updates the testplay-status.json snapshot and appends
// a corresponding event to events.ndjson.
//
// Manager implements WriterInterface so it can be used anywhere a Writer is
// expected — including as the inner of a runIDWriter.
//
// Manager is safe for concurrent use: Write and Heartbeat may be called
// simultaneously from different goroutines (e.g. the heartbeat ticker and
// the executor phase writer).
type Manager struct {
	snapshot WriterInterface
	events   *EventLog

	mu   sync.Mutex
	last Status // last Status passed to Write; used by Heartbeat
}

// NewManager creates a Manager that delegates snapshot writes to snapshot and
// appends events to events. Both fields are required and must be non-nil.
func NewManager(snapshot WriterInterface, events *EventLog) *Manager {
	return &Manager{snapshot: snapshot, events: events}
}

// Write updates the snapshot and appends an event derived from s.Phase.
// Run-scoped metadata fields (StartedAt, PID, ArtifactRoot, RunID) that are
// zero-valued in s are inherited from the previous Write, so callers only need
// to set them once at run start.
//
// Snapshot write errors are returned immediately. Event append errors are
// returned only if the snapshot write succeeded (so callers can treat
// event failures as secondary issues worth logging but not fatal).
func (m *Manager) Write(s Status) error {
	m.mu.Lock()
	// Inherit run-scoped metadata from the previous write if not set by caller.
	if s.StartedAt == "" {
		s.StartedAt = m.last.StartedAt
	}
	if s.PID == 0 {
		s.PID = m.last.PID
	}
	if s.ArtifactRoot == "" {
		s.ArtifactRoot = m.last.ArtifactRoot
	}
	if s.RunID == "" {
		s.RunID = m.last.RunID
	}
	m.last = s
	m.mu.Unlock()

	if err := m.snapshot.Write(s); err != nil {
		return err
	}

	eventName, reason := phaseToEvent(s.Phase)
	ev := Event{
		Event:    eventName,
		RunID:    s.RunID,
		Phase:    string(s.Phase),
		Reason:   reason,
		ExitCode: s.ExitCode,
	}
	// Event failures are non-fatal — the snapshot is already written.
	// Callers that need to surface these should check the returned error.
	return m.events.Append(ev)
}

// Heartbeat updates last_heartbeat_at in the snapshot without appending an event.
// It is intended to be called periodically from a background goroutine while Unity
// is running, so that external pollers can detect stale or hung runs.
//
// If no Write has been called yet, Heartbeat is a no-op (there is no run context
// to heartbeat into).
func (m *Manager) Heartbeat() error {
	m.mu.Lock()
	if m.last.Phase == "" {
		m.mu.Unlock()
		return nil // no run in progress yet
	}
	s := m.last
	s.LastHeartbeatAt = time.Now().UTC().Format(time.RFC3339)
	m.last = s
	m.mu.Unlock()

	return m.snapshot.Write(s)
}
