package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

const defaultPollInterval = 100 * time.Millisecond

// Client submits run requests to a warm-editor bridge over the file-based
// protocol under <project>/.testplay/bridge/ and translates the bridge's
// streamed progress into status writes. A Client is single-use per run but
// cheap to construct; it holds no open handles between calls.
type Client struct {
	dir             string // <project>/.testplay/bridge
	bridgeSessionID string // handshake identity this request is bound to
	pollInterval    time.Duration
}

// NewClient returns a Client targeting the bridge runtime dir of projectPath.
// bridgeSessionID must come from the Probe handshake immediately preceding the
// run. Binding every request and response to that identity prevents an editor
// restart from replaying a request that the previous process may have started.
func NewClient(projectPath, bridgeSessionID string) *Client {
	return &Client{
		dir:             BridgeDir(projectPath),
		bridgeSessionID: bridgeSessionID,
		pollInterval:    defaultPollInterval,
	}
}

// Run submits req to the bridge and blocks until the bridge writes a response,
// ctx is canceled, or an unrecoverable IO error occurs. Progress streamed by
// the bridge to status.ndjson is translated into sw.Write calls (compiling /
// running phases + current_test); the terminal "done" phase is owned by the
// caller (unity.ExecuteBridge) after it parses results.xml, matching the
// batchmode executor. On ctx cancellation Run writes a best-effort .cancel
// marker and returns ctx.Err() so the caller maps it to the same exit 4/8 a
// cold run would.
func (c *Client) Run(ctx context.Context, req RunRequest, sw status.WriterInterface) (RunOutcome, error) {
	if err := ctx.Err(); err != nil {
		return RunOutcome{}, err
	}
	if c.bridgeSessionID == "" {
		return RunOutcome{}, fmt.Errorf("bridge: expected bridge session id is empty")
	}
	if sw == nil {
		sw = noopWriter{}
	}

	runDir := filepath.Join(c.dir, "runs", req.RunID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return RunOutcome{}, fmt.Errorf("bridge: create run dir: %w", err)
	}
	statusPath := filepath.Join(runDir, "status.ndjson")
	compileErrPath := filepath.Join(runDir, "compile-errors.json")

	rf := requestFile{
		SchemaVersion:         "1",
		BridgeProtocolVersion: ProtocolVersion,
		RunID:                 req.RunID,
		BridgeSessionID:       c.bridgeSessionID,
		TestPlatform:          req.TestPlatform,
		Filter:                req.Filter,
		Category:              req.Category,
		ResultsXML:            req.ResultsXML,
		StatusNDJSON:          statusPath,
		CompileErrorsJSON:     compileErrPath,
		IdleDeadlineMs:        req.IdleDeadlineMs,
	}
	reqPath := filepath.Join(c.dir, "requests", req.RunID+".req.json")
	if err := writeAtomicJSON(reqPath, rf); err != nil {
		return RunOutcome{}, fmt.Errorf("bridge: write request: %w", err)
	}

	respPath := filepath.Join(c.dir, "responses", req.RunID+".resp.json")
	tombstonePath := filepath.Join(c.dir, "requests", req.RunID+".tombstone.json")
	var statusOffset int64

	interval := c.pollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		// Drain any new progress before checking cancellation, so a canceled
		// context still ships the final batch (mirrors ipc.PollingReader).
		statusOffset = c.drainProgress(statusPath, statusOffset, sw)

		if resp, ok := readResponse(respPath, req.RunID, c.bridgeSessionID); ok {
			return finishOutcome(resp, compileErrPath), nil
		}
		if tombstone, ok := readTombstone(tombstonePath, req.RunID); ok {
			if tombstone.ExecutionState != ExecutionStateNotStarted {
				return RunOutcome{}, &IndeterminateRunError{RunID: req.RunID, Reason: tombstone.Reason}
			}
			return RunOutcome{}, fmt.Errorf("bridge: transport failure for run %s: %s", req.RunID, tombstone.Reason)
		}

		select {
		case <-ctx.Done():
			c.writeCancel(req.RunID)
			c.drainProgress(statusPath, statusOffset, sw)
			return RunOutcome{}, ctx.Err()
		case <-tick.C:
		}
	}
}

// readTombstone validates the durable transport marker for runID. As with
// responses, partial, malformed, or foreign files are ignored and retried on a
// later poll.
func readTombstone(path, runID string) (tombstoneFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tombstoneFile{}, false
	}
	var tombstone tombstoneFile
	if err := json.Unmarshal(data, &tombstone); err != nil {
		return tombstoneFile{}, false
	}
	if tombstone.RunID != runID {
		return tombstoneFile{}, false
	}
	return tombstone, true
}

// finishOutcome builds the Go-side outcome from the bridge response, reading the
// compile-errors sidecar when the bridge reported a compile failure. The bridge
// writes the sidecar before the response (atomic ordering), so by the time the
// response is visible the sidecar is complete.
func finishOutcome(resp responseFile, compileErrPath string) RunOutcome {
	out := RunOutcome{Outcome: resp.Outcome, ResultsXMLWritten: resp.ResultsXMLWritten, NonPristine: resp.NonPristine}
	if resp.Outcome == OutcomeCompileFailed {
		if errs, err := readCompileErrors(compileErrPath); err == nil {
			out.CompileErrors = errs
		}
	}
	return out
}

// drainProgress reads new NDJSON progress lines from path since offset,
// translates each into a status write, and returns the new byte offset. A
// missing/partial file is tolerated (returns the unchanged offset).
func (c *Client) drainProgress(path string, offset int64, sw status.WriterInterface) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 8192), 1<<20)
	read := offset
	for scanner.Scan() {
		line := scanner.Bytes()
		read += int64(len(line)) + 1 // +1 for the stripped newline
		if len(line) == 0 {
			continue
		}
		var p progressLine
		if err := json.Unmarshal(line, &p); err != nil {
			continue // skip malformed/partial line; re-read next tick
		}
		if s, ok := progressToStatus(p); ok {
			_ = sw.Write(s)
		}
	}
	return read
}

// progressToStatus maps a streamed progress line to a status snapshot. It
// deliberately ignores any terminal "done" phase — that write belongs to the
// caller after parsing results.xml, keeping a single source of the done phase.
func progressToStatus(p progressLine) (status.Status, bool) {
	var phase status.Phase
	switch p.Phase {
	case string(status.PhaseCompiling):
		phase = status.PhaseCompiling
	case string(status.PhaseRunning):
		phase = status.PhaseRunning
	default:
		return status.Status{}, false
	}
	return status.Status{
		Phase:       phase,
		Total:       p.Total,
		Passed:      p.Passed,
		Failed:      p.Failed,
		CurrentTest: p.CurrentTest,
	}, true
}

// readResponse reads and validates the response file. It returns ok=false (so
// the caller keeps polling) when the file is absent, partially written, or
// carries a different run_id (a stale/foreign response can never be misread).
func readResponse(path, runID, bridgeSessionID string) (responseFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return responseFile{}, false
	}
	var r responseFile
	if err := json.Unmarshal(data, &r); err != nil {
		return responseFile{}, false
	}
	if r.RunID != runID || r.BridgeProtocolVersion != ProtocolVersion || r.BridgeSessionID != bridgeSessionID {
		return responseFile{}, false
	}
	return r, true
}

func readCompileErrors(path string) ([]history.CompileError, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f compileErrorsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return f.Errors, nil
}

// writeCancel drops a best-effort .cancel marker the bridge may observe to stop
// scheduling further test cases. Errors are ignored: cancellation correctness
// is owned by the Go-side context, not by the bridge cooperating.
func (c *Client) writeCancel(runID string) {
	path := filepath.Join(c.dir, "requests", runID+".cancel")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte("cancel\n"), 0o644)
}

// writeAtomicJSON marshals v and writes it to path via the shared atomic +
// Windows-tolerant tmp+rename writer (internal/atomicfile).
func writeAtomicJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0o644)
}

type noopWriter struct{}

func (noopWriter) Write(status.Status) error { return nil }
