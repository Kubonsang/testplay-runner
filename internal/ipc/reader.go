package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// ReadEvent is one line of bus traffic that the reader's owner is allowed
// to observe. Direction is "send" when the message originates from the
// reader's own role, "recv" when it is addressed to the reader's role
// (or broadcast to "*").
type ReadEvent struct {
	Msg       Message
	Direction string // "send" | "recv"
}

// PollingReader tails a bus file and emits ReadEvents matching the role.
// It tolerates a missing file (waits for it to appear) and skips
// malformed lines without aborting.
type PollingReader struct {
	path     string
	role     string
	interval time.Duration
}

// NewPollingReader constructs a reader for the given role. interval
// controls how often the file is polled for new bytes.
func NewPollingReader(path, role string, interval time.Duration) *PollingReader {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	return &PollingReader{path: path, role: role, interval: interval}
}

// Run reads the bus until ctx is canceled. New messages addressed to the
// reader's role (or broadcast) are written to out, along with messages
// originating from the reader's own role (Direction="send"). Each call
// re-opens the file from byte offset 0 of the prior read; we keep a
// running offset so we never re-emit a line.
func (r *PollingReader) Run(ctx context.Context, out chan<- ReadEvent) error {
	var offset int64
	tick := time.NewTicker(r.interval)
	defer tick.Stop()

	for {
		// Drain any new bytes before checking for cancellation, so a
		// canceled context still ships the final batch.
		newOffset, err := r.readSince(offset, out)
		if err != nil {
			// File may not exist yet — that's fine, retry next tick.
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else {
			offset = newOffset
		}

		select {
		case <-ctx.Done():
			// One last drain so messages written between the last tick
			// and the cancel are not lost.
			newOffset, _ = r.readSince(offset, out)
			_ = newOffset
			return nil
		case <-tick.C:
		}
	}
}

// readSince opens the bus file, seeks to offset, and emits filtered
// events for every complete NDJSON line. Returns the new byte offset.
func (r *PollingReader) readSince(offset int64, out chan<- ReadEvent) (int64, error) {
	f, err := os.Open(r.path)
	if err != nil {
		return offset, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, fmt.Errorf("ipc: seek: %w", err)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 8192), 1<<20)
	bytesRead := offset
	for scanner.Scan() {
		line := scanner.Bytes()
		// +1 for the newline that the scanner strips.
		bytesRead += int64(len(line)) + 1
		if len(line) == 0 {
			continue
		}
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			// Skip malformed line. Production callers can surface a
			// warning out-of-band; v0.9 keeps reader silent here.
			continue
		}
		ev, ok := r.classify(m)
		if !ok {
			continue
		}
		out <- ev
	}
	if err := scanner.Err(); err != nil {
		return offset, fmt.Errorf("ipc: scan: %w", err)
	}
	return bytesRead, nil
}

// classify decides whether m is visible to this reader and labels its
// direction. Returns (_, false) when the message is for someone else.
func (r *PollingReader) classify(m Message) (ReadEvent, bool) {
	if m.From == r.role {
		return ReadEvent{Msg: m, Direction: "send"}, true
	}
	if m.To == r.role || m.To == "*" {
		return ReadEvent{Msg: m, Direction: "recv"}, true
	}
	return ReadEvent{}, false
}
