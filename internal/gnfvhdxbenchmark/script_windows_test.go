//go:build windows

package gnfvhdxbenchmark

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func benchmarkScriptPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "scripts", "run-gnf-vhdx-benchmark.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGNFBenchmarkScriptParses(t *testing.T) {
	path := benchmarkScriptPath(t)
	script := `$tokens=$null;$errors=$null;[Management.Automation.Language.Parser]::ParseFile($env:TESTPLAY_SCRIPT_PATH,[ref]$tokens,[ref]$errors)|Out-Null;if($errors.Count-ne 0){$errors|ForEach-Object{$_.Message};exit 1}`
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "TESTPLAY_SCRIPT_PATH="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("parse: %v: %s", err, output)
	}
	data, _ := os.ReadFile(path)
	for _, required := range []string{"Specify exactly one of -Smoke or -Full", "Administrator PowerShell is required", "CodexMovementSmokeTest.TestPlayer_MovesRight_InPlayMode", "File Backed Virtual", "Set-StrictMode -Version Latest"} {
		if !strings.Contains(string(data), required) {
			t.Fatalf("missing %q", required)
		}
	}
}

func TestGNFBenchmarkFinalReportHandlesEmptyArraysUnderStrictMode(t *testing.T) {
	path := benchmarkScriptPath(t)
	script := `
Set-StrictMode -Version Latest
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($env:TESTPLAY_SCRIPT_PATH,[ref]$tokens,[ref]$errors)
if($errors.Count-ne 0){throw $errors[0]}
$functionAst=$ast.Find({param($node)$node-is[Management.Automation.Language.FunctionDefinitionAst]-and$node.Name-eq'New-GNFBenchmarkFinalReport'},$true)
if($null-eq$functionAst){throw 'function missing'}
. ([scriptblock]::Create($functionAst.Extent.Text))
$empty=New-GNFBenchmarkFinalReport -TestExitCode 0 -BeforeVirtualDisks @() -AfterVirtualDisks @() -VirtualDiskDifference @() -ProcessDifference @() -ResidualWorkItems @() -ArtifactRoot 'C:\artifacts'
$items=@([pscustomobject]@{FullName='C:\one'},[pscustomobject]@{FullName='C:\two'})
$nonEmpty=New-GNFBenchmarkFinalReport -TestExitCode 0 -BeforeVirtualDisks @() -AfterVirtualDisks @() -VirtualDiskDifference @() -ProcessDifference @() -ResidualWorkItems $items -ArtifactRoot 'C:\artifacts'
[pscustomobject]@{empty=$empty;nonEmpty=$nonEmpty}|ConvertTo-Json -Compress -Depth 6`
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "TESTPLAY_SCRIPT_PATH="+path)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime: %v: %s", err, output)
	}
	var result struct {
		Empty struct {
			Success  bool     `json:"success"`
			Residual []string `json:"residualWorkItems"`
		} `json:"empty"`
		NonEmpty struct {
			Success  bool     `json:"success"`
			Residual []string `json:"residualWorkItems"`
		} `json:"nonEmpty"`
	}
	if err = json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode: %v: %s", err, output)
	}
	if !result.Empty.Success || result.Empty.Residual == nil || len(result.Empty.Residual) != 0 {
		t.Fatalf("empty=%+v", result.Empty)
	}
	if result.NonEmpty.Success || len(result.NonEmpty.Residual) != 2 {
		t.Fatalf("nonEmpty=%+v", result.NonEmpty)
	}
}
