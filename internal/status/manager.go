package status

// Manager combines a snapshot Writer with an append-only EventLog.
// Each Write call updates the fastplay-status.json snapshot and appends
// a corresponding event to events.ndjson.
//
// Manager implements WriterInterface so it can be used anywhere a Writer is
// expected — including as the inner of a runIDWriter.
type Manager struct {
	snapshot WriterInterface
	events   *EventLog
}

// NewManager creates a Manager that delegates snapshot writes to snapshot and
// appends events to events. Both fields are required and must be non-nil.
func NewManager(snapshot WriterInterface, events *EventLog) *Manager {
	return &Manager{snapshot: snapshot, events: events}
}

// Write updates the snapshot and appends an event derived from s.Phase.
// Snapshot write errors are returned immediately. Event append errors are
// returned only if the snapshot write succeeded (so callers can treat
// event failures as secondary issues worth logging but not fatal).
func (m *Manager) Write(s Status) error {
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
