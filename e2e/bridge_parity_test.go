//go:build e2e

package e2e_test

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
	"github.com/Kubonsang/testplay-runner/internal/bridge"
	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

// TestE2E_BridgeColdParity is the SHIP GATE for the warm-editor bridge: for the
// same project, the cold (shadow/process) backend and the warm bridge backend
// must produce identical tests[], identical exit codes, and identical errors[]
// (modulo absolute paths).
//
// Prerequisites (the test self-skips otherwise):
//   - UNITY_PATH set to a real Editor.
//   - A live TestPlay bridge: the Editor open on testdata/unity-project with the
//     com.testplay.bridge package installed and TESTPLAY_BRIDGE_ENABLE=1.
//
// Run: UNITY_PATH=/path/to/Unity go test -tags e2e ./e2e/... -run BridgeColdParity
func TestE2E_BridgeColdParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E parity test in short mode")
	}
	cfg := buildConfig(t, t.TempDir()) // skips if UNITY_PATH unset

	expectedVer := bridge.ExpectedUnityVersion(cfg.UnityPath, cfg.ProjectPath)
	if _, ok, reason := bridge.Probe(cfg.ProjectPath, expectedVer, time.Now(), 0); !ok {
		t.Skipf("no live TestPlay bridge for parity (%s); open the Editor on the test project with TESTPLAY_BRIDGE_ENABLE=1 and com.testplay.bridge installed", reason)
	}

	cold := runService(t, cfg, runsvc.Request{Config: cfg, DisableBridge: true})
	warm := runService(t, cfg, runsvc.Request{Config: cfg, ForceBridge: true})

	if warm.Result.Backend != "bridge" {
		t.Fatalf("warm run did not use the bridge backend (got %q); cannot assert parity", warm.Result.Backend)
	}
	t.Logf("cold backend=%q exit=%d, bridge backend=%q exit=%d",
		cold.Result.Backend, cold.ExitCode, warm.Result.Backend, warm.ExitCode)

	if cold.ExitCode != warm.ExitCode {
		t.Errorf("exit code mismatch: cold=%d bridge=%d", cold.ExitCode, warm.ExitCode)
	}
	assertSameTests(t, cold.Result.Tests, warm.Result.Tests)
	assertSameErrors(t, cold.Result.Errors, warm.Result.Errors)
}

// runService executes one run with its own result/artifact/status dirs.
func runService(t *testing.T, cfg *config.Config, req runsvc.Request) runsvc.Response {
	t.Helper()
	resultDir := t.TempDir()
	svc := &runsvc.Service{
		Runner:       &unity.ProcessRunner{UnityPath: cfg.UnityPath},
		Store:        history.NewStore(resultDir),
		Artifacts:    artifacts.NewStore(filepath.Join(t.TempDir(), ".testplay", "runs")),
		StatusWriter: status.NewWriter(filepath.Join(t.TempDir(), "status.json")),
	}
	resp, err := svc.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Service.Run infrastructure error (backend req force=%v disable=%v): %v", req.ForceBridge, req.DisableBridge, err)
	}
	return resp
}

func assertSameTests(t *testing.T, cold, warm []parser.TestCase) {
	t.Helper()
	key := func(tcs []parser.TestCase) map[string]string {
		m := make(map[string]string, len(tcs))
		for _, tc := range tcs {
			m[tc.Name] = tc.Result
		}
		return m
	}
	c, w := key(cold), key(warm)
	if len(c) != len(w) {
		t.Errorf("test count mismatch: cold=%d bridge=%d", len(c), len(w))
	}
	for name, res := range c {
		if wr, ok := w[name]; !ok {
			t.Errorf("test %q present cold, missing in bridge", name)
		} else if wr != res {
			t.Errorf("test %q result mismatch: cold=%q bridge=%q", name, res, wr)
		}
	}
	for name := range w {
		if _, ok := c[name]; !ok {
			t.Errorf("test %q present in bridge, missing cold", name)
		}
	}
}

func assertSameErrors(t *testing.T, cold, warm []history.CompileError) {
	t.Helper()
	if len(cold) != len(warm) {
		t.Errorf("compile-error count mismatch: cold=%d bridge=%d", len(cold), len(warm))
		return
	}
	// Compare by (basename:line, message) ignoring absolute path differences.
	norm := func(errs []history.CompileError) []string {
		out := make([]string, 0, len(errs))
		for _, e := range errs {
			out = append(out, filepath.Base(e.File)+":"+itoa(e.Line)+" "+e.Message)
		}
		sort.Strings(out)
		return out
	}
	c, w := norm(cold), norm(warm)
	for i := range c {
		if c[i] != w[i] {
			t.Errorf("compile-error mismatch at %d:\n cold=%q\n  bri=%q", i, c[i], w[i])
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
