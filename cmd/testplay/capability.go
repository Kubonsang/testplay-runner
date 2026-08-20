package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
	"github.com/Kubonsang/testplay-runner/internal/bridge"
	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/runid"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

type capabilityOptions struct {
	kind                 bridge.CapabilityKind
	requireBridgeSession string
	requireEditorPID     int
	workspaceID          string
	noFallback           bool
	filter               string
	category             string
}

type capabilityClient interface {
	Run(context.Context, bridge.RunRequest, status.WriterInterface) (bridge.RunOutcome, error)
}

type capabilityDeps struct {
	now    func() time.Time
	probe  func(string, string, bridge.CapabilityRequirements, time.Time, time.Duration) (*bridge.Handshake, bool, string)
	client func(string, *bridge.Handshake) capabilityClient
}

type capabilityResult struct {
	SchemaVersion       string                 `json:"schema_version"`
	Capability          bridge.CapabilityKind  `json:"capability"`
	RunID               string                 `json:"run_id"`
	ArtifactRoot        string                 `json:"artifact_root"`
	ExitCode            int                    `json:"exit_code"`
	Backend             string                 `json:"backend"`
	Bridge              capabilityBridge       `json:"bridge"`
	CompileErrors       int                    `json:"compile_errors"`
	CompileErrorDetails []history.CompileError `json:"compile_error_details,omitempty"`
	Total               int                    `json:"total"`
	Passed              int                    `json:"passed"`
	Failed              int                    `json:"failed"`
	Skipped             int                    `json:"skipped"`
	FallbackUsed        bool                   `json:"fallback_used"`
	CleanupState        string                 `json:"cleanup_state"`
	Error               string                 `json:"error,omitempty"`
}

type capabilityBridge struct {
	ProtocolVersion int    `json:"protocol_version"`
	WorkspaceID     string `json:"workspace_id"`
	EditorPID       int    `json:"editor_pid"`
	BridgeSessionID string `json:"bridge_session_id"`
}

func runCapability(w io.Writer, opts capabilityOptions, deps capabilityDeps) int {
	if opts.kind != bridge.CapabilityCompile && opts.kind != bridge.CapabilityWarmTest {
		writeJSON(w, map[string]any{"schema_version": "1", "error": "capability must be compile or warm-test"})
		return 5
	}
	if !filepath.IsAbs(configPath) {
		writeJSON(w, map[string]any{"schema_version": "1", "error": "--config must be an absolute path for capability commands"})
		return 5
	}
	if !opts.noFallback {
		writeJSON(w, map[string]any{"schema_version": "1", "error": "capability commands require --no-fallback"})
		return 5
	}
	if opts.workspaceID == "" || opts.requireBridgeSession == "" || opts.requireEditorPID <= 0 {
		writeJSON(w, map[string]any{"schema_version": "1", "error": "--workspace-id, --require-bridge-session, and a positive --require-editor-pid are required"})
		return 5
	}
	if opts.kind == bridge.CapabilityCompile && (opts.filter != "" || opts.category != "") {
		writeJSON(w, map[string]any{"schema_version": "1", "error": "--filter and --category are valid only for warm-test"})
		return 5
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		writeJSON(w, map[string]any{"schema_version": "1", "error": err.Error()})
		return 5
	}
	if err := cfg.Validate(true); err != nil {
		writeJSON(w, map[string]any{"schema_version": "1", "error": err.Error()})
		return 5
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.probe == nil {
		deps.probe = bridge.ProbeCapability
	}
	if deps.client == nil {
		deps.client = func(projectPath string, h *bridge.Handshake) capabilityClient {
			return bridge.NewClientForHandshake(projectPath, h)
		}
	}

	now := deps.now()
	runID := runid.Generate(now)
	store := artifacts.NewStore(filepath.Join(cfg.ProjectPath, ".testplay", "runs"))
	artifactRoot, err := store.PrepareRunDir(runID)
	if err != nil {
		writeJSON(w, map[string]any{"schema_version": "1", "error": err.Error()})
		return 6
	}
	_ = store.SaveRawLogs(runID, nil, nil)
	resultsPath := store.ResultsFilePath(runID)

	result := capabilityResult{
		SchemaVersion: "1", Capability: opts.kind, RunID: runID, ArtifactRoot: artifactRoot,
		ExitCode: 6, Backend: "bridge", FallbackUsed: false, CleanupState: "released",
		Bridge: capabilityBridge{ProtocolVersion: bridge.ProtocolVersion, WorkspaceID: opts.workspaceID,
			EditorPID: opts.requireEditorPID, BridgeSessionID: opts.requireBridgeSession},
	}
	finish := func(code int, message string) int {
		result.ExitCode = code
		result.Error = message
		_ = store.SaveSummary(runID, result)
		_ = store.SaveManifest(runID, artifacts.Manifest{
			SchemaVersion: "1", RunID: runID, ArtifactRoot: artifactRoot, ResultsXML: resultsPath,
			StdoutLog: store.StdoutFilePath(runID), StderrLog: store.StderrFilePath(runID),
			StartedAt: now.UTC().Format(time.RFC3339), FinishedAt: deps.now().UTC().Format(time.RFC3339), ExitCode: code,
		})
		writeJSON(w, result)
		return code
	}

	requirements := bridge.CapabilityRequirements{
		WorkspaceID: opts.workspaceID, BridgeSessionID: opts.requireBridgeSession, EditorPID: opts.requireEditorPID,
	}
	h, ok, reason := deps.probe(cfg.ProjectPath, bridge.ExpectedUnityVersion(cfg.UnityPath, cfg.ProjectPath), requirements, now, 0)
	if !ok {
		return finish(6, "strict bridge capability probe failed: "+reason)
	}
	result.Bridge = capabilityBridge{ProtocolVersion: h.BridgeProtocolVersion, WorkspaceID: h.WorkspaceID, EditorPID: h.EditorPID, BridgeSessionID: h.BridgeSessionID}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout.TotalMs)*time.Millisecond)
	defer cancel()
	outcome, runErr := deps.client(cfg.ProjectPath, h).Run(ctx, bridge.RunRequest{
		RunID: runID, CapabilityKind: opts.kind, TestPlatform: cfg.TestPlatform,
		Filter: opts.filter, Category: opts.category, ResultsXML: resultsPath,
		IdleDeadlineMs: cfg.Timeout.TotalMs,
	}, nil)
	if runErr != nil {
		if _, indeterminate := runErr.(*bridge.IndeterminateRunError); indeterminate {
			return finish(9, runErr.Error())
		}
		if ctx.Err() != nil {
			return finish(9, "capability execution became indeterminate: "+ctx.Err().Error())
		}
		return finish(6, runErr.Error())
	}

	switch outcome.Outcome {
	case bridge.OutcomeCompileFailed:
		result.CompileErrors = len(outcome.CompileErrors)
		result.CompileErrorDetails = outcome.CompileErrors
		return finish(2, "Unity compilation failed")
	case bridge.OutcomeBuildFailed, bridge.OutcomeBusy, bridge.OutcomeRejected:
		return finish(6, fmt.Sprintf("bridge capability was not executed: outcome=%s", outcome.Outcome))
	case bridge.OutcomeIndeterminate:
		return finish(9, "bridge capability execution is indeterminate")
	case bridge.OutcomeCompleted:
	default:
		return finish(6, fmt.Sprintf("unknown bridge outcome %q", outcome.Outcome))
	}

	if opts.kind == bridge.CapabilityCompile {
		if outcome.ResultsXMLWritten {
			return finish(6, "compile capability unexpectedly executed tests")
		}
		return finish(0, "")
	}
	if !outcome.ResultsXMLWritten {
		return finish(9, "warm-test completed without a durable results.xml")
	}
	data, err := os.ReadFile(resultsPath)
	if err != nil {
		return finish(9, "warm-test results could not be read: "+err.Error())
	}
	parsed, err := parser.Parse(data)
	if err != nil {
		return finish(9, err.Error())
	}
	result.Total, result.Passed, result.Failed, result.Skipped = parsed.Total, parsed.Passed, parsed.Failed, parsed.Skipped
	if parsed.Total < 1 {
		return finish(6, "warm-test selection executed zero tests")
	}
	if parsed.Failed > 0 {
		return finish(3, "one or more warm tests failed")
	}
	return finish(0, "")
}

var capabilityCmd = &cobra.Command{
	Use:   "capability",
	Short: "Execute an exact protocol-3 warm Unity capability",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return fmt.Errorf("capability kind required: compile or warm-test")
	},
}

func newCapabilityLeaf(kind bridge.CapabilityKind) *cobra.Command {
	opts := capabilityOptions{kind: kind}
	cmd := &cobra.Command{
		Use:   string(kind),
		Short: "Execute the " + string(kind) + " capability in an owned warm editor",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			os.Exit(runCapability(cmd.OutOrStdout(), opts, capabilityDeps{}))
		},
	}
	cmd.Flags().StringVar(&opts.requireBridgeSession, "require-bridge-session", "", "Required exact bridge session ID")
	cmd.Flags().IntVar(&opts.requireEditorPID, "require-editor-pid", 0, "Required exact Unity Editor PID")
	cmd.Flags().StringVar(&opts.workspaceID, "workspace-id", "", "Required exact HoneyBee workspace ID")
	cmd.Flags().BoolVar(&opts.noFallback, "no-fallback", false, "Refuse cold Unity fallback")
	if kind == bridge.CapabilityWarmTest {
		cmd.Flags().StringVar(&opts.filter, "filter", "", "Full-name test filter")
		cmd.Flags().StringVar(&opts.category, "category", "", "Test category filter")
	}
	return cmd
}

func init() {
	capabilityCmd.AddCommand(newCapabilityLeaf(bridge.CapabilityCompile), newCapabilityLeaf(bridge.CapabilityWarmTest))
	rootCmd.AddCommand(capabilityCmd)
}
