package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultStaleWindow is the maximum age of a handshake heartbeat for the bridge
// to be considered live. It is 3× the bridge's ~1s heartbeat, so a couple of
// missed beats (GC pause, busy main thread) do not trip a false fall-back.
const DefaultStaleWindow = 3 * time.Second

// BridgeDir returns the bridge runtime directory for a project:
// <projectPath>/.testplay/bridge.
func BridgeDir(projectPath string) string {
	return filepath.Join(projectPath, ".testplay", "bridge")
}

// HandshakePath returns the handshake.json path for a project.
func HandshakePath(projectPath string) string {
	return filepath.Join(BridgeDir(projectPath), "handshake.json")
}

// Probe reads the handshake and reports whether a live, COMPATIBLE bridge is
// present for projectPath. It returns (handshake, true, "") when every gate
// passes, or (handshake-or-nil, false, reason) describing the first failed gate
// (reason is suitable for stderr logging / disclosure). This is the tier-1
// selection gate: any failure must make the caller fall back to the cold path.
//
// expectedUnityVersion is compared (case-insensitively) to the handshake's
// reported Unity version when both are non-empty; an empty expected value skips
// only that single check (the other gates plus the C#-side Pristine Gate still
// protect hermeticity). now is injected for testability.
func Probe(projectPath, expectedUnityVersion string, now time.Time, staleWindow time.Duration) (*Handshake, bool, string) {
	if staleWindow <= 0 {
		staleWindow = DefaultStaleWindow
	}

	data, err := os.ReadFile(HandshakePath(projectPath))
	if err != nil {
		return nil, false, "no bridge handshake present"
	}
	var h Handshake
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, false, "bridge handshake is malformed"
	}

	if h.BridgeProtocolVersion != ProtocolVersion {
		return &h, false, fmt.Sprintf("bridge protocol version mismatch (handshake=%d, want=%d)", h.BridgeProtocolVersion, ProtocolVersion)
	}
	if h.BridgeSessionID == "" {
		return &h, false, "bridge handshake has no session identity"
	}
	if !samePath(projectPath, h.ProjectPathReal) && !samePath(projectPath, h.ProjectPath) {
		return &h, false, "bridge handshake project path does not match"
	}
	if expectedUnityVersion != "" && h.UnityVersion != "" && !strings.EqualFold(expectedUnityVersion, h.UnityVersion) {
		return &h, false, fmt.Sprintf("bridge Unity version mismatch (editor=%s, want=%s)", h.UnityVersion, expectedUnityVersion)
	}

	updatedAt, err := time.Parse(time.RFC3339, h.UpdatedAt)
	if err != nil {
		return &h, false, "bridge handshake timestamp is unparseable"
	}
	// |age| must be within the window. A timestamp far in the future means the
	// editor host clock is skewed ahead and its liveness cannot be trusted, so a
	// crashed-but-skewed editor must not pass the live gate.
	if age := now.Sub(updatedAt); age > staleWindow || age < -staleWindow {
		return &h, false, fmt.Sprintf("bridge handshake is stale or clock-skewed (age %s)", age.Round(time.Millisecond))
	}

	if h.EditorState != EditorStateIdle {
		return &h, false, fmt.Sprintf("editor not idle (state=%q)", h.EditorState)
	}
	// An idle editor must have no run in flight. A non-empty active_run_id with
	// an idle state signals a stale/orphaned run (e.g. a domain reload reset the
	// in-memory running flag while SessionState kept the active id); do not
	// dispatch a new run onto it.
	if h.ActiveRunID != "" {
		return &h, false, "editor reports an active run in flight"
	}

	return &h, true, ""
}

// samePath reports whether a and b resolve to the same filesystem path. It
// canonicalizes symlinks and compares case-insensitively so a probe never
// false-fails on case-insensitive volumes (macOS/Windows). b == "" is never a
// match (an empty handshake field must not satisfy the gate).
func samePath(a, b string) bool {
	if b == "" {
		return false
	}
	return strings.EqualFold(canonPath(a), canonPath(b))
}

func canonPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

// unityVersionRe matches a Unity version token such as "2022.3.10f1" or
// "6000.0.0b3" embedded in a Unity Hub install path.
var unityVersionRe = regexp.MustCompile(`\d{4,}\.\d+\.\d+[abfp]\d+`)

// UnityVersionFromPath extracts a Unity version token from a Unity binary path
// when the Unity Hub layout embeds it (e.g. .../Editor/2022.3.10f1/...).
// Returns "" when no version token is present.
func UnityVersionFromPath(unityPath string) string {
	return unityVersionRe.FindString(unityPath)
}

// ProjectEditorVersion reads m_EditorVersion from
// <projectPath>/ProjectSettings/ProjectVersion.txt. Returns "" when the file is
// missing or the field is absent.
func ProjectEditorVersion(projectPath string) string {
	data, err := os.ReadFile(filepath.Join(projectPath, "ProjectSettings", "ProjectVersion.txt"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "m_EditorVersion:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// ExpectedUnityVersion returns the Unity version a cold run would use, derived
// first from the configured Unity binary path (most authoritative — that is the
// binary a cold run launches) and falling back to the project's
// ProjectVersion.txt. Returns "" when neither is determinable, in which case
// Probe skips the version check.
func ExpectedUnityVersion(unityPath, projectPath string) string {
	if v := UnityVersionFromPath(unityPath); v != "" {
		return v
	}
	return ProjectEditorVersion(projectPath)
}
