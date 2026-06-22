package runsvc_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
	"github.com/Kubonsang/testplay-runner/internal/bridge"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

// trackingRunner records whether the cold batchmode path was invoked.
type trackingRunner struct{ called atomic.Bool }

func (r *trackingRunner) Run(_ context.Context, _ []string, _, _ io.Writer) (int, error) {
	r.called.Store(true)
	return 2, nil // a cold run here would produce exit 2 (no results.xml)
}

func writeBridgeHandshake(t *testing.T, projectPath string, now time.Time) {
	t.Helper()
	dir := bridge.BridgeDir(projectPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := bridge.Handshake{
		SchemaVersion:         "1",
		BridgeProtocolVersion: bridge.ProtocolVersion,
		ProjectPath:           projectPath,
		ProjectPathReal:       projectPath,
		UnityVersion:          "2022.3.10f1",
		EditorPID:             1234,
		BridgeSessionID:       "20260326-120000-feedface",
		UpdatedAt:             now.UTC().Format(time.RFC3339),
		EditorState:           bridge.EditorStateIdle,
	}
	data, _ := json.MarshalIndent(h, "", "  ")
	if err := os.WriteFile(bridge.HandshakePath(projectPath), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// simulateBridge watches the bridge requests dir and, for the first request,
// writes the passing results.xml the Go-assigned path and a completed response.
func simulateBridge(t *testing.T, projectPath string, resultsXML []byte) {
	t.Helper()
	reqDir := filepath.Join(bridge.BridgeDir(projectPath), "requests")
	respDir := filepath.Join(bridge.BridgeDir(projectPath), "responses")
	go func() {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			entries, _ := os.ReadDir(reqDir)
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".req.json") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(reqDir, e.Name()))
				if err != nil {
					continue
				}
				var req struct {
					RunID      string `json:"run_id"`
					ResultsXML string `json:"results_xml"`
				}
				if json.Unmarshal(data, &req) != nil || req.RunID == "" {
					continue
				}
				_ = os.WriteFile(req.ResultsXML, resultsXML, 0o644)
				resp := map[string]any{
					"schema_version":          "1",
					"bridge_protocol_version": bridge.ProtocolVersion,
					"run_id":                  req.RunID,
					"outcome":                 "completed",
					"results_xml_written":     true,
				}
				rb, _ := json.MarshalIndent(resp, "", "  ")
				_ = os.MkdirAll(respDir, 0o755)
				tmp := filepath.Join(respDir, req.RunID+".resp.json.tmp")
				_ = os.WriteFile(tmp, rb, 0o644)
				_ = os.Rename(tmp, filepath.Join(respDir, req.RunID+".resp.json"))
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Error("bridge: no request observed before deadline")
	}()
}

func TestService_BridgeSelectedWhenHandshakeLive(t *testing.T) {
	cfg, dir := baseConfig(t)
	now := time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC)
	writeBridgeHandshake(t, dir, now)

	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	simulateBridge(t, dir, xmlData)

	cold := &trackingRunner{}
	svc := &runsvc.Service{
		Runner:       cold,
		Store:        history.NewStore(cfg.ResultDir),
		Artifacts:    artifacts.NewStore(filepath.Join(dir, ".testplay", "runs")),
		StatusWriter: status.NewWriter(filepath.Join(dir, "status.json")),
		Clock:        func() time.Time { return now },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := svc.Run(ctx, runsvc.Request{Config: cfg})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Fatalf("expected exit 0 via bridge, got %d", resp.ExitCode)
	}
	if resp.Result.Backend != "bridge" {
		t.Fatalf("backend = %q, want bridge", resp.Result.Backend)
	}
	if cold.called.Load() {
		t.Fatal("cold batchmode runner must not be invoked when the bridge handles the run")
	}
}

func TestService_DisableBridgeForcesCold(t *testing.T) {
	cfg, dir := baseConfig(t)
	now := time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC)
	writeBridgeHandshake(t, dir, now) // live bridge present, but disabled by request

	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	cold := &fakeRunner{resultsXML: xmlData}
	svc := &runsvc.Service{
		Runner:       cold,
		Store:        history.NewStore(cfg.ResultDir),
		Artifacts:    artifacts.NewStore(filepath.Join(dir, ".testplay", "runs")),
		StatusWriter: status.NewWriter(filepath.Join(dir, "status.json")),
		Clock:        func() time.Time { return now },
	}

	resp, err := svc.Run(context.Background(), runsvc.Request{Config: cfg, DisableBridge: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", resp.ExitCode)
	}
	if resp.Result.Backend != "process" {
		t.Fatalf("backend = %q, want process (bridge disabled)", resp.Result.Backend)
	}
}

func TestService_NoHandshakeFallsBackToProcess(t *testing.T) {
	cfg, dir := baseConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	cold := &fakeRunner{resultsXML: xmlData}
	svc := &runsvc.Service{
		Runner:       cold,
		Store:        history.NewStore(cfg.ResultDir),
		Artifacts:    artifacts.NewStore(filepath.Join(dir, ".testplay", "runs")),
		StatusWriter: status.NewWriter(filepath.Join(dir, "status.json")),
		Clock:        func() time.Time { return time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC) },
	}

	resp, err := svc.Run(context.Background(), runsvc.Request{Config: cfg})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Result.Backend != "process" {
		t.Fatalf("backend = %q, want process (no bridge handshake)", resp.Result.Backend)
	}
}
