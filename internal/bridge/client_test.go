package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

type recorder struct {
	mu     sync.Mutex
	writes []status.Status
}

const testBridgeSessionID = "20260101-110000-cafebabe"

func (r *recorder) Write(s status.Status) error {
	r.mu.Lock()
	r.writes = append(r.writes, s)
	r.mu.Unlock()
	return nil
}

func (r *recorder) snapshot() []status.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]status.Status, len(r.writes))
	copy(out, r.writes)
	return out
}

// waitForRequest polls until the bridge request file appears, then returns it.
func waitForRequest(t *testing.T, path string) requestFile {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			var rf requestFile
			if json.Unmarshal(data, &rf) == nil && rf.RunID != "" {
				return rf
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("bridge request file not written in time")
	return requestFile{}
}

func appendNDJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

func newFastClient(projectPath string) *Client {
	c := NewClient(projectPath, testBridgeSessionID)
	c.pollInterval = 5 * time.Millisecond
	return c
}

func TestClientRun_Completed(t *testing.T) {
	dir := t.TempDir()
	c := newFastClient(dir)
	runID := "20260101-120000-deadbeef"
	reqPath := filepath.Join(c.dir, "requests", runID+".req.json")

	go func() {
		rf := waitForRequest(t, reqPath)
		// Stream progress, then complete.
		appendNDJSON(t, rf.StatusNDJSON, progressLine{Phase: "compiling"})
		appendNDJSON(t, rf.StatusNDJSON, progressLine{Phase: "running", Total: 3, CurrentTest: "Ns.MyTest"})
		_ = writeAtomicJSON(filepath.Join(c.dir, "responses", runID+".resp.json"), responseFile{
			SchemaVersion:         "1",
			BridgeProtocolVersion: ProtocolVersion,
			RunID:                 runID,
			BridgeSessionID:       testBridgeSessionID,
			Outcome:               OutcomeCompleted,
			ResultsXMLWritten:     true,
		})
	}()

	rec := &recorder{}
	out, err := c.Run(context.Background(), RunRequest{RunID: runID, TestPlatform: "edit_mode", ResultsXML: filepath.Join(dir, "results.xml")}, rec)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out.Outcome != OutcomeCompleted {
		t.Fatalf("outcome = %q, want completed", out.Outcome)
	}

	writes := rec.snapshot()
	var sawCompiling, sawRunning bool
	for _, w := range writes {
		if w.Phase == status.PhaseCompiling {
			sawCompiling = true
		}
		if w.Phase == status.PhaseRunning && w.CurrentTest == "Ns.MyTest" {
			sawRunning = true
		}
	}
	if !sawCompiling || !sawRunning {
		t.Fatalf("expected compiling+running progress writes, got %+v", writes)
	}
}

func TestClientRun_CompileFailedReadsSidecar(t *testing.T) {
	dir := t.TempDir()
	c := newFastClient(dir)
	runID := "20260101-120001-deadbee0"
	reqPath := filepath.Join(c.dir, "requests", runID+".req.json")

	want := history.CompileError{
		File:         "Assets/Foo.cs",
		AbsolutePath: filepath.Join(dir, "Assets", "Foo.cs"),
		Line:         42,
		Column:       10,
		Message:      "CS0246: The type or namespace name 'Bar' could not be found",
	}

	go func() {
		rf := waitForRequest(t, reqPath)
		// Sidecar MUST be written before the response (ordering discipline).
		_ = writeAtomicJSON(rf.CompileErrorsJSON, compileErrorsFile{SchemaVersion: "1", Errors: []history.CompileError{want}})
		_ = writeAtomicJSON(filepath.Join(c.dir, "responses", runID+".resp.json"), responseFile{
			SchemaVersion:         "1",
			BridgeProtocolVersion: ProtocolVersion,
			RunID:                 runID,
			BridgeSessionID:       testBridgeSessionID,
			Outcome:               OutcomeCompileFailed,
			CompileFailed:         true,
			CompileErrorCount:     1,
		})
	}()

	out, err := c.Run(context.Background(), RunRequest{RunID: runID, TestPlatform: "edit_mode"}, nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out.Outcome != OutcomeCompileFailed {
		t.Fatalf("outcome = %q, want compile_failed", out.Outcome)
	}
	if len(out.CompileErrors) != 1 || out.CompileErrors[0] != want {
		t.Fatalf("compile errors = %+v, want [%+v]", out.CompileErrors, want)
	}
}

func TestClientRun_NonPristineWarnings(t *testing.T) {
	dir := t.TempDir()
	c := newFastClient(dir)
	runID := "20260101-120002-deadbee1"
	reqPath := filepath.Join(c.dir, "requests", runID+".req.json")

	go func() {
		waitForRequest(t, reqPath)
		_ = writeAtomicJSON(filepath.Join(c.dir, "responses", runID+".resp.json"), responseFile{
			SchemaVersion:         "1",
			BridgeProtocolVersion: ProtocolVersion,
			RunID:                 runID,
			BridgeSessionID:       testBridgeSessionID,
			Outcome:               OutcomeCompleted,
			ResultsXMLWritten:     true,
			NonPristine:           []string{"editor had unsaved changes in 1 scene"},
		})
	}()

	out, err := c.Run(context.Background(), RunRequest{RunID: runID, TestPlatform: "edit_mode"}, nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(out.NonPristine) != 1 {
		t.Fatalf("expected 1 non_pristine disclosure, got %+v", out.NonPristine)
	}
}

func TestClientRun_ContextCancelWritesCancelMarker(t *testing.T) {
	dir := t.TempDir()
	c := newFastClient(dir)
	runID := "20260101-120003-deadbee2"

	ctx, cancel := context.WithCancel(context.Background())
	// No bridge responds; cancel shortly after the request is submitted.
	go func() {
		reqPath := filepath.Join(c.dir, "requests", runID+".req.json")
		waitForRequest(t, reqPath)
		cancel()
	}()

	_, err := c.Run(ctx, RunRequest{RunID: runID, TestPlatform: "edit_mode"}, nil)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	cancelPath := filepath.Join(c.dir, "requests", runID+".cancel")
	if _, statErr := os.Stat(cancelPath); statErr != nil {
		t.Fatalf("expected cancel marker at %s: %v", cancelPath, statErr)
	}
}

func TestClientRun_PreCanceledContextDoesNotPublishRequest(t *testing.T) {
	dir := t.TempDir()
	c := newFastClient(dir)
	runID := "20260101-120003-deadbee9"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Run(ctx, RunRequest{RunID: runID, TestPlatform: "edit_mode"}, nil)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	requestPath := filepath.Join(c.dir, "requests", runID+".req.json")
	if _, statErr := os.Stat(requestPath); !os.IsNotExist(statErr) {
		t.Fatalf("pre-canceled run must not publish request, stat err=%v", statErr)
	}
}

func TestClientRun_IgnoresResponseFromDifferentBridgeSession(t *testing.T) {
	dir := t.TempDir()
	c := newFastClient(dir)
	runID := "20260101-120003-deadbeea"
	reqPath := filepath.Join(c.dir, "requests", runID+".req.json")

	go func() {
		waitForRequest(t, reqPath)
		responsePath := filepath.Join(c.dir, "responses", runID+".resp.json")
		_ = writeAtomicJSON(responsePath, responseFile{
			SchemaVersion:         "1",
			BridgeProtocolVersion: ProtocolVersion,
			RunID:                 runID,
			BridgeSessionID:       "different-editor-session",
			Outcome:               OutcomeCompleted,
			ResultsXMLWritten:     true,
		})
		time.Sleep(30 * time.Millisecond)
		_ = writeAtomicJSON(responsePath, responseFile{
			SchemaVersion:         "1",
			BridgeProtocolVersion: ProtocolVersion,
			RunID:                 runID,
			BridgeSessionID:       testBridgeSessionID,
			Outcome:               OutcomeCompleted,
			ResultsXMLWritten:     true,
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	out, err := c.Run(ctx, RunRequest{RunID: runID, TestPlatform: "edit_mode"}, nil)
	if err != nil || out.Outcome != OutcomeCompleted {
		t.Fatalf("expected current-session completion, outcome=%q err=%v", out.Outcome, err)
	}
	if elapsed := time.Since(started); elapsed < 25*time.Millisecond {
		t.Fatalf("foreign-session response was accepted too early; elapsed=%s", elapsed)
	}
}

func TestClientRun_TombstoneReturnsTransportFailureImmediately(t *testing.T) {
	dir := t.TempDir()
	c := newFastClient(dir)
	runID := "20260101-120004-deadbee3"
	reqPath := filepath.Join(c.dir, "requests", runID+".req.json")

	go func() {
		waitForRequest(t, reqPath)
		_ = writeAtomicJSON(filepath.Join(c.dir, "requests", runID+".tombstone.json"), tombstoneFile{
			SchemaVersion:         "1",
			BridgeProtocolVersion: ProtocolVersion,
			RunID:                 runID,
			Reason:                "terminal response could not be persisted",
			CreatedAt:             time.Now().UTC().Format(time.RFC3339),
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	_, err := c.Run(ctx, RunRequest{RunID: runID, TestPlatform: "edit_mode"}, nil)
	if err == nil || !strings.Contains(err.Error(), "transport failure") || !strings.Contains(err.Error(), "could not be persisted") {
		t.Fatalf("expected bridge transport failure with tombstone reason, got %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("tombstone should fail promptly instead of waiting for timeout; elapsed=%s", elapsed)
	}
}
