// Package ipc implements the v0.9 scenario-scoped IPC bus that lets
// instances of a scenario-mode run exchange newline-delimited JSON messages
// through a shared file. The BusWriter here is mainly a Go-side test
// utility — production users append to the bus file directly from their
// Unity test code in any language.
//
// Atomic-append guarantees follow the same pattern as
// internal/status/events.go: O_APPEND + per-writer mutex. OS-level atomic
// concatenation holds for writes smaller than PIPE_BUF (typically 4096B).
package ipc

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Message is one entry on the IPC bus. Schema is documented in the v0.9
// release notes and RELEASE-PLAN.md; agents read this from
// instances[].ipc_messages in scenario output.
type Message struct {
	Seq     int    `json:"seq"`
	Ts      string `json:"ts"`
	From    string `json:"from"`
	To      string `json:"to"`
	Kind    string `json:"kind"`
	Payload any    `json:"payload,omitempty"`
}

// BusWriter appends Messages to a shared bus file. Each writer is bound
// to a single role; the From field is set automatically and overrides any
// caller-supplied value.
type BusWriter struct {
	path string
	role string
	mu   sync.Mutex
	seq  int
}

// NewBusWriter returns a writer that appends to path under the given role.
// The bus file is created lazily on the first Append.
func NewBusWriter(path, role string) (*BusWriter, error) {
	return &BusWriter{path: path, role: role}, nil
}

// Append serializes m as one NDJSON line and appends it to the bus file.
// Seq, Ts, and From are filled in by the writer; any caller-supplied
// values for those fields are overwritten.
func (w *BusWriter) Append(m Message) (Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.seq++
	m.Seq = w.seq
	m.Ts = time.Now().UTC().Format(time.RFC3339)
	m.From = w.role

	data, err := json.Marshal(m)
	if err != nil {
		return Message{}, fmt.Errorf("ipc: marshal: %w", err)
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return Message{}, fmt.Errorf("ipc: open %s: %w", w.path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(string(data) + "\n"); err != nil {
		return Message{}, fmt.Errorf("ipc: write: %w", err)
	}
	return m, nil
}
