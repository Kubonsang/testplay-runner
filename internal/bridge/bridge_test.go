package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeHandshake(t *testing.T, projectPath string, h Handshake) {
	t.Helper()
	if err := os.MkdirAll(BridgeDir(projectPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(HandshakePath(projectPath), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// validHandshake returns a handshake that passes every Probe gate for the given
// project at the given reference time.
func validHandshake(projectPath string, now time.Time) Handshake {
	return Handshake{
		SchemaVersion:         "1",
		BridgeProtocolVersion: ProtocolVersion,
		ProjectPath:           projectPath,
		ProjectPathReal:       canonPath(projectPath),
		UnityVersion:          "2022.3.10f1",
		EditorPID:             4242,
		BridgeSessionID:       "20260101-120000-aaaaaaaa",
		UpdatedAt:             now.Add(-1 * time.Second).UTC().Format(time.RFC3339),
		EditorState:           EditorStateIdle,
		ActiveRunID:           "",
	}
}

func TestProbe_AllGatesPass(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeHandshake(t, dir, validHandshake(dir, now))

	h, ok, reason := Probe(dir, "2022.3.10f1", now, 0)
	if !ok {
		t.Fatalf("expected probe to pass, got reason %q", reason)
	}
	if h == nil || h.EditorState != EditorStateIdle {
		t.Fatalf("unexpected handshake: %+v", h)
	}
}

func TestProbe_VersionCheckSkippedWhenExpectedEmpty(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	hs := validHandshake(dir, now)
	hs.UnityVersion = "9999.9.9f9"
	writeHandshake(t, dir, hs)

	if _, ok, reason := Probe(dir, "", now, 0); !ok {
		t.Fatalf("empty expected version should skip the check, got %q", reason)
	}
}

func TestProbe_Failures(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		expectVer string
		mutate    func(projectPath string, h *Handshake)
		write     bool // when false, no handshake is written at all
		wantFrag  string
	}{
		{name: "missing", write: false, wantFrag: "no bridge handshake"},
		{name: "protocol mismatch", write: true, mutate: func(_ string, h *Handshake) { h.BridgeProtocolVersion = 99 }, wantFrag: "protocol version mismatch"},
		{name: "missing session identity", write: true, mutate: func(_ string, h *Handshake) { h.BridgeSessionID = "" }, wantFrag: "no session identity"},
		{name: "project mismatch", write: true, mutate: func(_ string, h *Handshake) { h.ProjectPathReal = "/somewhere/else"; h.ProjectPath = "/somewhere/else" }, wantFrag: "project path does not match"},
		{name: "version mismatch", write: true, expectVer: "2022.3.10f1", mutate: func(_ string, h *Handshake) { h.UnityVersion = "2021.3.0f1" }, wantFrag: "Unity version mismatch"},
		{name: "stale", write: true, mutate: func(_ string, h *Handshake) {
			h.UpdatedAt = time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339)
		}, wantFrag: "stale"},
		{name: "bad timestamp", write: true, mutate: func(_ string, h *Handshake) { h.UpdatedAt = "not-a-time" }, wantFrag: "unparseable"},
		{name: "not idle", write: true, mutate: func(_ string, h *Handshake) { h.EditorState = EditorStateInPlayMode }, wantFrag: "not idle"},
		{name: "active run in flight", write: true, mutate: func(_ string, h *Handshake) { h.ActiveRunID = "20260101-120000-deadbeef" }, wantFrag: "active run"},
		{name: "future clock skew", write: true, mutate: func(_ string, h *Handshake) {
			h.UpdatedAt = time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
		}, wantFrag: "skew"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.write {
				hs := validHandshake(dir, now)
				if tc.mutate != nil {
					tc.mutate(dir, &hs)
				}
				writeHandshake(t, dir, hs)
			}
			expect := tc.expectVer
			if expect == "" {
				expect = "2022.3.10f1"
			}
			_, ok, reason := Probe(dir, expect, now, 3*time.Second)
			if ok {
				t.Fatalf("expected probe to fail for %s", tc.name)
			}
			if !strings.Contains(reason, tc.wantFrag) {
				t.Fatalf("reason %q does not contain %q", reason, tc.wantFrag)
			}
		})
	}
}

func TestProbe_MalformedHandshake(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(BridgeDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(HandshakePath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, reason := Probe(dir, "", time.Now(), 0); ok || !strings.Contains(reason, "malformed") {
		t.Fatalf("expected malformed failure, got ok=%v reason=%q", ok, reason)
	}
}

func TestUnityVersionFromPath(t *testing.T) {
	cases := map[string]string{
		"/Applications/Unity/Hub/Editor/2022.3.10f1/Unity.app/Contents/MacOS/Unity": "2022.3.10f1",
		`C:\Program Files\Unity\Hub\Editor\6000.0.0b3\Editor\Unity.exe`:             "6000.0.0b3",
		"/usr/local/bin/unity": "",
	}
	for path, want := range cases {
		if got := UnityVersionFromPath(path); got != want {
			t.Errorf("UnityVersionFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestProjectEditorVersionAndExpected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ProjectSettings"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "m_EditorVersion: 2022.3.10f1\nm_EditorVersionWithRevision: 2022.3.10f1 (abc123)\n"
	if err := os.WriteFile(filepath.Join(dir, "ProjectSettings", "ProjectVersion.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ProjectEditorVersion(dir); got != "2022.3.10f1" {
		t.Fatalf("ProjectEditorVersion = %q", got)
	}
	// Path token wins when present.
	if got := ExpectedUnityVersion("/Editor/2021.3.5f1/Unity", dir); got != "2021.3.5f1" {
		t.Fatalf("ExpectedUnityVersion path-derived = %q", got)
	}
	// Falls back to ProjectVersion.txt when the path has no token.
	if got := ExpectedUnityVersion("/usr/bin/unity", dir); got != "2022.3.10f1" {
		t.Fatalf("ExpectedUnityVersion project-derived = %q", got)
	}
}
