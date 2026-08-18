package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/bridge"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

type fakeCapabilityClient struct {
	outcome bridge.RunOutcome
	err     error
	req     bridge.RunRequest
	xml     []byte
}

func (f *fakeCapabilityClient) Run(_ context.Context, req bridge.RunRequest, _ status.WriterInterface) (bridge.RunOutcome, error) {
	f.req = req
	if len(f.xml) > 0 {
		_ = os.WriteFile(req.ResultsXML, f.xml, 0o644)
	}
	return f.outcome, f.err
}

func capabilityTestConfig(t *testing.T) (string, string) {
	t.Helper()
	project := t.TempDir()
	path := filepath.Join(project, "testplay.json")
	data := []byte(`{"schema_version":"1","unity_path":"C:\\Unity\\Unity.exe","project_path":"` + filepath.ToSlash(project) + `","test_platform":"edit_mode","timeout":{"total_ms":5000}}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, project
}

func capabilityTestDeps(client capabilityClient, probeOK bool) capabilityDeps {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	return capabilityDeps{
		now: func() time.Time { return now },
		probe: func(projectPath, _ string, req bridge.CapabilityRequirements, _ time.Time, _ time.Duration) (*bridge.Handshake, bool, string) {
			if !probeOK {
				return nil, false, "identity mismatch"
			}
			return &bridge.Handshake{
				BridgeProtocolVersion: bridge.ProtocolVersion, ProjectPath: projectPath, ProjectPathReal: projectPath,
				WorkspaceID: req.WorkspaceID, EditorPID: req.EditorPID, BridgeSessionID: req.BridgeSessionID,
			}, true, ""
		},
		client: func(string, *bridge.Handshake) capabilityClient { return client },
	}
}

func decodeCapabilityResult(t *testing.T, buf *bytes.Buffer) capabilityResult {
	t.Helper()
	var got capabilityResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid capability JSON %q: %v", buf.String(), err)
	}
	return got
}

func withCapabilityConfig(t *testing.T, path string) {
	t.Helper()
	previous := configPath
	configPath = path
	t.Cleanup(func() { configPath = previous })
}

func validCapabilityOptions(kind bridge.CapabilityKind) capabilityOptions {
	return capabilityOptions{kind: kind, workspaceID: "workspace-1", requireBridgeSession: "session-1", requireEditorPID: 4242, noFallback: true}
}

func TestCapabilityCompileSuccessDoesNotRunTests(t *testing.T) {
	path, _ := capabilityTestConfig(t)
	withCapabilityConfig(t, path)
	client := &fakeCapabilityClient{outcome: bridge.RunOutcome{Outcome: bridge.OutcomeCompleted}}
	var out bytes.Buffer
	if code := runCapability(&out, validCapabilityOptions(bridge.CapabilityCompile), capabilityTestDeps(client, true)); code != 0 {
		t.Fatalf("exit=%d output=%s", code, out.String())
	}
	got := decodeCapabilityResult(t, &out)
	if got.Capability != bridge.CapabilityCompile || got.FallbackUsed || got.Bridge.WorkspaceID != "workspace-1" {
		t.Fatalf("result=%+v", got)
	}
	if client.req.CapabilityKind != bridge.CapabilityCompile || client.req.Filter != "" || client.req.Category != "" {
		t.Fatalf("compile request=%+v", client.req)
	}
}

func TestCapabilityWarmTestRequiresAtLeastOneResult(t *testing.T) {
	path, _ := capabilityTestConfig(t)
	withCapabilityConfig(t, path)
	client := &fakeCapabilityClient{
		outcome: bridge.RunOutcome{Outcome: bridge.OutcomeCompleted, ResultsXMLWritten: true},
		xml:     []byte(`<test-run total="0" passed="0" failed="0" skipped="0" duration="0"></test-run>`),
	}
	var out bytes.Buffer
	if code := runCapability(&out, validCapabilityOptions(bridge.CapabilityWarmTest), capabilityTestDeps(client, true)); code != 6 {
		t.Fatalf("exit=%d output=%s", code, out.String())
	}
	if got := decodeCapabilityResult(t, &out); got.Total != 0 || got.FallbackUsed {
		t.Fatalf("result=%+v", got)
	}
}

func TestCapabilityWarmTestPassAndFailureExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		xml  string
		code int
	}{
		{"pass", `<test-run total="1" passed="1" failed="0" skipped="0" duration="0.1"><test-suite><test-case fullname="Smoke.Pass" result="Passed" duration="0.1" /></test-suite></test-run>`, 0},
		{"failure", `<test-run total="1" passed="0" failed="1" skipped="0" duration="0.1"><test-suite><test-case fullname="Smoke.Fail" result="Failed" duration="0.1" /></test-suite></test-run>`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := capabilityTestConfig(t)
			withCapabilityConfig(t, path)
			client := &fakeCapabilityClient{outcome: bridge.RunOutcome{Outcome: bridge.OutcomeCompleted, ResultsXMLWritten: true}, xml: []byte(tc.xml)}
			var out bytes.Buffer
			if code := runCapability(&out, validCapabilityOptions(bridge.CapabilityWarmTest), capabilityTestDeps(client, true)); code != tc.code {
				t.Fatalf("exit=%d want=%d output=%s", code, tc.code, out.String())
			}
		})
	}
}

func TestCapabilityNoFallbackAndStrictIdentity(t *testing.T) {
	path, _ := capabilityTestConfig(t)
	withCapabilityConfig(t, path)
	client := &fakeCapabilityClient{}

	for _, tc := range []struct {
		name string
		opts capabilityOptions
		deps capabilityDeps
		code int
	}{
		{"no flag", func() capabilityOptions {
			o := validCapabilityOptions(bridge.CapabilityCompile)
			o.noFallback = false
			return o
		}(), capabilityTestDeps(client, true), 5},
		{"probe mismatch", validCapabilityOptions(bridge.CapabilityCompile), capabilityTestDeps(client, false), 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := runCapability(&out, tc.opts, tc.deps); code != tc.code {
				t.Fatalf("exit=%d want=%d output=%s", code, tc.code, out.String())
			}
			if got := decodeCapabilityResult(t, &out); tc.code == 6 && got.FallbackUsed {
				t.Fatal("strict capability must never fall back")
			}
		})
	}
}
