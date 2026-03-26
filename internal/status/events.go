package status

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Event is a single entry in the append-only events.ndjson log.
type Event struct {
	Event     string `json:"event"`
	RunID     string `json:"run_id,omitempty"`
	Phase     string `json:"phase,omitempty"`
	Timestamp string `json:"timestamp"`
	Reason    string `json:"reason,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
}

// EventLog writes Event entries as newline-delimited JSON to a file.
// Each Append call atomically appends one line. The file is created on
// first append and shared across the lifetime of a run.
type EventLog struct {
	path string
	mu   sync.Mutex
}

// NewEventLog creates an EventLog that writes to path.
// The file is created on the first Append call.
func NewEventLog(path string) *EventLog {
	return &EventLog{path: path}
}

// Append serializes e as a single JSON line and appends it to the log file.
func (l *EventLog) Append(e Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	e.Timestamp = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("events: marshal: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("events: open %s: %w", l.path, err)
	}
	defer f.Close()

	_, err = f.WriteString(string(data) + "\n")
	return err
}

// phaseToEvent maps a status Phase to an event name and optional reason.
func phaseToEvent(p Phase) (eventName, reason string) {
	switch p {
	case PhaseCompiling:
		return "run_started", ""
	case PhaseRunning:
		return "phase_changed", ""
	case PhaseDone:
		return "run_finished", ""
	case PhaseTimeoutCompile:
		return "timeout", "compile"
	case PhaseTimeoutTest:
		return "timeout", "test"
	case PhaseTimeoutTotal:
		return "timeout", "total"
	case PhaseInterrupted:
		return "interrupted", ""
	default:
		// Unknown phase — emit as phase_changed with the raw value as reason.
		return "phase_changed", strings.ToLower(string(p))
	}
}
