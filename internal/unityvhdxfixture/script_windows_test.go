//go:build windows

package unityvhdxfixture

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func unityVHDXFixtureScriptPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "scripts", "run-unity-vhdx-fixture.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnityVHDXFixtureScriptParses(t *testing.T) {
	path := unityVHDXFixtureScriptPath(t)
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

func TestUnityVHDXFixtureFinalReportHandlesResidualArraysUnderStrictMode(t *testing.T) {
	path := unityVHDXFixtureScriptPath(t)
	script := `
Set-StrictMode -Version Latest
$tokens=$null
$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($env:TESTPLAY_SCRIPT_PATH,[ref]$tokens,[ref]$errors)
if($errors.Count -ne 0){throw $errors[0]}
$functionAst=$ast.Find({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'New-UnityVHDXFinalReport'},$true)
if($null -eq $functionAst){throw 'New-UnityVHDXFinalReport was not found'}
. ([scriptblock]::Create($functionAst.Extent.Text))
$empty=New-UnityVHDXFinalReport -TestExitCode 0 -BeforeVirtualDisks @() -AfterVirtualDisks @() -VirtualDiskDifference @() -ResidualFixtureItems @() -ArtifactRoot 'C:\artifacts'
$singleItem=@([pscustomobject]@{FullName='C:\single'})
$single=New-UnityVHDXFinalReport -TestExitCode 0 -BeforeVirtualDisks @() -AfterVirtualDisks @() -VirtualDiskDifference @() -ResidualFixtureItems $singleItem -ArtifactRoot 'C:\artifacts'
$items=@([pscustomobject]@{FullName='C:\one'},[pscustomobject]@{FullName='C:\two'})
$nonEmpty=New-UnityVHDXFinalReport -TestExitCode 0 -BeforeVirtualDisks @() -AfterVirtualDisks @() -VirtualDiskDifference @() -ResidualFixtureItems $items -ArtifactRoot 'C:\artifacts'
[pscustomobject]@{empty=$empty;single=$single;nonEmpty=$nonEmpty}|ConvertTo-Json -Compress -Depth 6`
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "TESTPLAY_SCRIPT_PATH="+path)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell runtime self-check: %v: %s", err, output)
	}
	var result struct {
		Empty struct {
			Success              bool     `json:"success"`
			ResidualFixtureItems []string `json:"residualFixtureItems"`
		} `json:"empty"`
		Single struct {
			Success              bool     `json:"success"`
			ResidualFixtureItems []string `json:"residualFixtureItems"`
		} `json:"single"`
		NonEmpty struct {
			Success              bool     `json:"success"`
			ResidualFixtureItems []string `json:"residualFixtureItems"`
		} `json:"nonEmpty"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode runtime self-check: %v: %s", err, output)
	}
	if !result.Empty.Success || result.Empty.ResidualFixtureItems == nil || len(result.Empty.ResidualFixtureItems) != 0 {
		t.Fatalf("empty result=%+v", result.Empty)
	}
	if result.Single.Success || !equalStrings(result.Single.ResidualFixtureItems, []string{`C:\single`}) {
		t.Fatalf("single result=%+v", result.Single)
	}
	want := []string{`C:\one`, `C:\two`}
	if result.NonEmpty.Success || !equalStrings(result.NonEmpty.ResidualFixtureItems, want) {
		t.Fatalf("non-empty result=%+v want=%v", result.NonEmpty, want)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
