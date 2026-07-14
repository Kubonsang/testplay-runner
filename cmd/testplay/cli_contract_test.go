package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// The stdout=JSON contract can only be violated by cobra's own output paths
// (help, usage, completion), which bypass the commands' RunE functions.
// These tests therefore exercise the real built binary.
var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

func testBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		// Not t.TempDir(): the binary must outlive the first test that builds it.
		dir, err := os.MkdirTemp("", "testplay-cli-test")
		if err != nil {
			buildErr = err
			return
		}
		name := "testplay-cli-test"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		builtBin = filepath.Join(dir, name)
		out, err := exec.Command("go", "build", "-o", builtBin, ".").CombinedOutput()
		if err != nil {
			buildErr = err
			t.Logf("go build output: %s", out)
		}
	})
	if buildErr != nil {
		t.Fatalf("failed to build testplay binary: %v", buildErr)
	}
	return builtBin
}

func runBinary(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(testBinary(t), args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("failed to run binary: %v", err)
	}
	return outBuf.String(), errBuf.String(), code
}

func assertJSONError(t *testing.T, stdout string) {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout must be a single JSON object, got: %q", stdout)
	}
	if out["schema_version"] != "1" {
		t.Errorf("expected schema_version \"1\", got %v", out["schema_version"])
	}
	if out["error"] == nil || out["error"] == "" {
		t.Errorf("expected non-empty error field, got %v", out["error"])
	}
}

func TestCLI_BareInvocation_JSONErrorExit5(t *testing.T) {
	stdout, _, code := runBinary(t)
	if code != 5 {
		t.Errorf("bare invocation must not exit 0 (exit 0 = all tests passed); got %d", code)
	}
	assertJSONError(t, stdout)
}

func TestCLI_Help_StdoutStaysPureJSON(t *testing.T) {
	stdout, stderr, code := runBinary(t, "--help")
	if code != 0 {
		t.Errorf("--help should exit 0, got %d", code)
	}
	if stdout != "" {
		t.Errorf("--help must not write to stdout (stdout = JSON only), got: %q", stdout)
	}
	if stderr == "" {
		t.Error("--help should print human help on stderr")
	}
}

func TestCLI_UnknownCommand_JSONErrorExit5(t *testing.T) {
	stdout, _, code := runBinary(t, "frobnicate")
	if code != 5 {
		t.Errorf("unknown command should exit 5 (usage/config error), got %d", code)
	}
	assertJSONError(t, stdout)
}

func TestCLI_UnknownFlag_JSONErrorExit5(t *testing.T) {
	stdout, _, code := runBinary(t, "run", "--no-such-flag")
	if code != 5 {
		t.Errorf("unknown flag should exit 5 (usage/config error), got %d", code)
	}
	assertJSONError(t, stdout)
}

func TestCLI_CompletionCommandDisabled(t *testing.T) {
	stdout, _, code := runBinary(t, "completion", "bash")
	if code == 0 {
		t.Fatalf("completion command must be disabled (it dumps a script to stdout); stdout: %q", stdout)
	}
	assertJSONError(t, stdout)
}

func TestCLI_InternalCompletionCommandsReturnSingleJSON(t *testing.T) {
	for _, hidden := range []string{"__complete", "__completeNoDesc"} {
		t.Run(hidden, func(t *testing.T) {
			stdout, _, code := runBinary(t, hidden, "run")
			if code != 5 {
				t.Fatalf("%s must be rejected with exit 5, got %d; stdout: %q", hidden, code, stdout)
			}
			assertJSONError(t, stdout)
		})
	}
}

func TestCLI_InternalCompletionAfterPersistentConfigFlagReturnsSingleJSON(t *testing.T) {
	stdout, _, code := runBinary(t, "--config", "elsewhere.json", "__complete", "run")
	if code != 5 {
		t.Fatalf("hidden completion after persistent flags must exit 5, got %d; stdout: %q", code, stdout)
	}
	assertJSONError(t, stdout)
}

func TestCLI_PositionalArgsRejected(t *testing.T) {
	// `testplay run MyTest` silently ran the FULL suite — the positional arg
	// was accepted and ignored. Commands take no positional arguments.
	stdout, _, code := runBinary(t, "check", "stray-arg")
	if code != 5 {
		t.Errorf("positional args should be rejected with exit 5, got %d", code)
	}
	assertJSONError(t, stdout)
}

func TestCLI_ListRejectsInvalidConfigWithJSONExit5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "testplay.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"1","unknown_key":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runBinary(t, "--config", path, "list")
	if code != 5 {
		t.Fatalf("invalid list config must exit 5, got %d; stdout: %q", code, stdout)
	}
	assertJSONError(t, stdout)
}

func TestCLI_ListMissingConfigUsesIncompleteStaticFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	stdout, _, code := runBinary(t, "--config", path, "list")
	if code != 0 {
		t.Fatalf("missing config list fallback must exit 0, got %d; stdout: %q", code, stdout)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout must be a single JSON object: %v; output: %q", err, stdout)
	}
	if out["schema_version"] != "1" || out["source"] != "static_scan" || out["complete"] != false {
		t.Fatalf("unexpected missing-config fallback: %v", out)
	}
}
