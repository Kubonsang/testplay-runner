package status

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Phase represents the current execution phase of a fastplay run.
type Phase string

const (
	PhaseWaiting        Phase = "waiting"
	PhaseCompiling      Phase = "compiling"
	PhaseRunning        Phase = "running"
	PhaseDone           Phase = "done"
	PhaseTimeoutCompile Phase = "timeout_compile"
	PhaseTimeoutTest    Phase = "timeout_test"
	PhaseTimeoutTotal   Phase = "timeout_total"
	PhaseInterrupted    Phase = "interrupted"
)

// Status is written atomically to fastplay-status.json during a run.
type Status struct {
	SchemaVersion string `json:"schema_version"`
	Phase         Phase  `json:"phase"`
	RunID         string `json:"run_id,omitempty"`
	Total         int    `json:"total,omitempty"`
	Passed        int    `json:"passed,omitempty"`
	Failed        int    `json:"failed,omitempty"`
	CurrentTest   string `json:"current_test,omitempty"`
	UpdatedAt     string `json:"updated_at"`
}

// WriterInterface is the interface for writing status updates.
type WriterInterface interface {
	Write(s Status) error
}

// Writer writes Status atomically to a file path.
type Writer struct {
	path string
	mu   sync.Mutex
}

// NewWriter creates a Writer targeting path.
func NewWriter(path string) *Writer {
	return &Writer{path: path}
}

// Write serializes s to JSON and atomically replaces the target file.
// It always sets schema_version and updated_at before writing.
func (w *Writer) Write(s Status) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	s.SchemaVersion = "1"
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}

	// On Windows, os.Rename fails if the destination exists.
	// Remove first, then rename for cross-platform atomicity.
	if err := os.Rename(tmp, w.path); err != nil {
		// Fallback: remove destination and retry
		_ = os.Remove(w.path)
		if err2 := os.Rename(tmp, w.path); err2 != nil {
			_ = os.Remove(tmp)
			return err2
		}
	}

	return nil
}
