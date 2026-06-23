package status

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
)

// Phase represents the current execution phase of a testplay run.
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

// Status is written atomically to testplay-status.json during a run.
type Status struct {
	SchemaVersion string `json:"schema_version"`
	Seq           int    `json:"seq"`
	Phase         Phase  `json:"phase"`
	RunID         string `json:"run_id,omitempty"`
	Total         int    `json:"total,omitempty"`
	Passed        int    `json:"passed,omitempty"`
	Failed        int    `json:"failed,omitempty"`
	CurrentTest   string `json:"current_test,omitempty"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	UpdatedAt     string `json:"updated_at"`

	// Run-scoped metadata — written once at start, preserved across phase updates.
	StartedAt       string `json:"started_at,omitempty"`
	LastHeartbeatAt string `json:"last_heartbeat_at,omitempty"`
	PID             int    `json:"pid,omitempty"`
	ArtifactRoot    string `json:"artifact_root,omitempty"`
}

// WriterInterface is the interface for writing status updates.
type WriterInterface interface {
	Write(s Status) error
}

// Writer writes Status atomically to a file path.
type Writer struct {
	path string
	mu   sync.Mutex
	seq  int // incremented on every Write; injected as Seq into the Status
}

// NewWriter creates a Writer targeting path.
func NewWriter(path string) *Writer {
	return &Writer{path: path}
}

// Write serializes s to JSON and atomically replaces the target file.
// It always sets schema_version, updated_at, and seq before writing.
func (w *Writer) Write(s Status) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.seq++
	s.SchemaVersion = "1"
	s.Seq = w.seq
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	// Atomic + Windows-tolerant: an agent polling the status file can otherwise
	// make the replace-rename fail with a sharing violation on Windows.
	return atomicfile.Write(w.path, data, 0644)
}
