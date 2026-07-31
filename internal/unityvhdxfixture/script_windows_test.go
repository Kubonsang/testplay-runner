//go:build windows

package unityvhdxfixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnityVHDXFixtureScriptParses(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "scripts", "run-unity-vhdx-fixture.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	script := `$tokens=$null; $errors=$null; [Management.Automation.Language.Parser]::ParseFile($env:TESTPLAY_SCRIPT_PATH,[ref]$tokens,[ref]$errors) | Out-Null; if($errors.Count -ne 0){$errors | ForEach-Object {$_.Message}; exit 1}`
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "TESTPLAY_SCRIPT_PATH="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("PowerShell parse: %v: %s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{"Administrator PowerShell is required", "File Backed Virtual", "TESTPLAY_UNITY_EDITOR_PATH", "TESTPLAY_UNITY_VHDX_FIXTURE_ROOT", "TESTPLAY_UNITY_VHDX_ARTIFACT_ROOT"} {
		if !strings.Contains(text, required) {
			t.Fatalf("script is missing %q", required)
		}
	}
}
