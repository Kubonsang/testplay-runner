// Package bridge implements the Go-side client for the TestPlay warm-editor
// bridge: a persistent listener inside an already-open Unity Editor that runs
// tests in the warm domain and writes the same NUnit results.xml a cold
// batchmode run would produce. Transport is file-based — atomic tmp+rename for
// request/response/handshake and append-only NDJSON for progress — under
// <project>/.testplay/bridge/, mirroring the idioms in internal/ipc and
// internal/status. The bridge never binds a socket (no firewall prompts) and
// never writes testplay-status.json directly (the Go side owns the monotonic
// seq counter); it only streams typed progress that the Go client translates.
package bridge

import (
	"github.com/Kubonsang/testplay-runner/internal/history"
)

// ProtocolVersion is the current wire protocol. LegacyProtocolVersion remains
// readable so an upgraded CLI can continue to use an already-open v2 editor.
const (
	LegacyProtocolVersion = 2
	ProtocolVersion       = 3
)

// CapabilityKind is a protocol-v3 operation. An empty kind is valid only for
// legacy protocol-v2 requests.
type CapabilityKind string

const (
	CapabilityCompile  CapabilityKind = "compile"
	CapabilityWarmTest CapabilityKind = "warm-test"
)

// Editor states reported in the handshake's editor_state field.
const (
	EditorStateIdle         = "idle"
	EditorStateCompiling    = "compiling"
	EditorStateImporting    = "importing"
	EditorStateInPlayMode   = "in_playmode"
	EditorStateRunningTests = "running_tests"
)

// Outcome is the terminal disposition of a bridge run, reported in the response.
type Outcome string

const (
	OutcomeCompleted     Outcome = "completed"      // results.xml written; classified by parseResults (exit 0/3)
	OutcomeCompileFailed Outcome = "compile_failed" // compile-errors.json written; exit 2
	OutcomeBuildFailed   Outcome = "build_failed"   // license/build-target issue; exit 6
	OutcomeBusy          Outcome = "busy"           // another run is in flight; caller falls back to cold
	OutcomeRejected      Outcome = "rejected"       // Pristine Gate refused (e.g. in PlayMode); fall back
	OutcomeIndeterminate Outcome = "indeterminate"  // execution may have started; exit 9, never rerun
)

const (
	ExecutionStateNotStarted      = "not_started"
	ExecutionStatePossiblyStarted = "possibly_started"
)

// Handshake is the liveness + identity document the bridge writes atomically to
// <project>/.testplay/bridge/handshake.json on a ~1s heartbeat.
type Handshake struct {
	SchemaVersion         string `json:"schema_version"`
	BridgeProtocolVersion int    `json:"bridge_protocol_version"`
	ProjectPath           string `json:"project_path"`
	ProjectPathReal       string `json:"project_path_real"` // symlink/case-canonicalized, for matching
	UnityVersion          string `json:"unity_version"`
	EditorPID             int    `json:"editor_pid"`
	WorkspaceID           string `json:"workspace_id,omitempty"`
	BridgeSessionID       string `json:"bridge_session_id"` // runid-format; changes on editor restart
	UpdatedAt             string `json:"updated_at"`        // RFC3339 UTC
	EditorState           string `json:"editor_state"`
	ActiveRunID           string `json:"active_run_id"`
}

// requestFile is the on-disk request DTO written to requests/<runID>.req.json.
type requestFile struct {
	SchemaVersion         string `json:"schema_version"`
	BridgeProtocolVersion int    `json:"bridge_protocol_version"`
	RunID                 string `json:"run_id"`
	BridgeSessionID       string `json:"bridge_session_id"`
	WorkspaceID           string `json:"workspace_id,omitempty"`
	EditorPID             int    `json:"editor_pid,omitempty"`
	CapabilityKind        string `json:"capability_kind,omitempty"`
	TestPlatform          string `json:"test_platform"`
	Filter                string `json:"filter,omitempty"`
	Category              string `json:"category,omitempty"`
	ResultsXML            string `json:"results_xml"`         // Go-assigned -testResults path
	StatusNDJSON          string `json:"status_ndjson"`       // bridge appends progress here
	CompileErrorsJSON     string `json:"compile_errors_json"` // bridge writes the sidecar here on compile failure
	IdleDeadlineMs        int64  `json:"idle_deadline_ms"`    // how long the bridge may wait for the editor to settle
}

// responseFile is the on-disk response DTO written to responses/<runID>.resp.json.
type responseFile struct {
	SchemaVersion         string   `json:"schema_version"`
	BridgeProtocolVersion int      `json:"bridge_protocol_version"`
	RunID                 string   `json:"run_id"`
	BridgeSessionID       string   `json:"bridge_session_id"`
	WorkspaceID           string   `json:"workspace_id,omitempty"`
	EditorPID             int      `json:"editor_pid,omitempty"`
	CapabilityKind        string   `json:"capability_kind,omitempty"`
	Outcome               Outcome  `json:"outcome"`
	ResultsXMLWritten     bool     `json:"results_xml_written"`
	CompileFailed         bool     `json:"compile_failed"`
	CompileErrorCount     int      `json:"compile_error_count"`
	NonPristine           []string `json:"non_pristine"` // disclosure reasons → run warnings
	FinishedAt            string   `json:"finished_at"`
}

// tombstoneFile is a durable transport-failure marker written next to a
// request when the Unity bridge cannot publish its terminal response. It also
// seals requests that were canceled before they could be claimed, preventing a
// later editor restart from replaying them.
type tombstoneFile struct {
	SchemaVersion         string `json:"schema_version"`
	BridgeProtocolVersion int    `json:"bridge_protocol_version"`
	RunID                 string `json:"run_id"`
	BridgeSessionID       string `json:"bridge_session_id,omitempty"`
	WorkspaceID           string `json:"workspace_id,omitempty"`
	EditorPID             int    `json:"editor_pid,omitempty"`
	ExecutionState        string `json:"execution_state"`
	Reason                string `json:"reason"`
	CreatedAt             string `json:"created_at"`
}

// IndeterminateRunError means Unity may have executed some or all selected
// tests but could not publish a trustworthy terminal result. Callers must not
// cold-fallback, because doing so could repeat test side effects.
type IndeterminateRunError struct {
	RunID  string
	Reason string
}

func (e *IndeterminateRunError) Error() string {
	return "bridge: run " + e.RunID + " may have executed but its terminal result is indeterminate: " + e.Reason
}

// progressLine is one NDJSON line the bridge appends to status.ndjson. The
// terminal "done" phase is intentionally NOT streamed — the Go caller owns it
// after parsing results.xml, exactly as the batchmode executor does.
type progressLine struct {
	Phase       string `json:"phase"` // "compiling" | "running"
	Total       int    `json:"total,omitempty"`
	Passed      int    `json:"passed,omitempty"`
	Failed      int    `json:"failed,omitempty"`
	CurrentTest string `json:"current_test,omitempty"`
}

// compileErrorsFile is the sidecar the bridge writes when compilation fails.
// Its Errors map 1:1 onto history.CompileError so the Go side reuses the same
// struct (and downstream MakeRelative/exit-2 handling) the stderr path uses.
type compileErrorsFile struct {
	SchemaVersion string                 `json:"schema_version"`
	Errors        []history.CompileError `json:"errors"`
}

// RunRequest is the Go-side input to Client.Run.
type RunRequest struct {
	RunID          string
	CapabilityKind CapabilityKind
	TestPlatform   string // "edit_mode" | "play_mode"
	Filter         string
	Category       string
	ResultsXML     string // Go-assigned -testResults path; the bridge writes NUnit XML here
	IdleDeadlineMs int64  // how long the bridge may wait for the editor to become idle
}

// RunOutcome is the Go-side result of Client.Run.
type RunOutcome struct {
	Outcome           Outcome
	ResultsXMLWritten bool                   // the bridge claims it wrote results.xml (Outcome==completed)
	NonPristine       []string               // disclosure reasons → run warnings
	CompileErrors     []history.CompileError // populated when Outcome == OutcomeCompileFailed
}
