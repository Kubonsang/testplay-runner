//go:build windows

package vhdxstorage

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDevDrivePowerShellParses(t *testing.T) {
	parser := `
$tokens = $null
$parseErrors = $null
[System.Management.Automation.Language.Parser]::ParseInput(
  $env:TESTPLAY_DEV_DRIVE_SCRIPT,
  [ref]$tokens,
  [ref]$parseErrors
) | Out-Null
if ($parseErrors.Count -ne 0) {
  $parseErrors | ForEach-Object { Write-Error $_.Message }
  exit 1
}
`
	for name, script := range map[string]string{
		"capability": devDriveCapabilityScript,
		"initialize": initializeDevDriveDiskScript,
		"inspect":    inspectDevDriveVolumeScript,
	} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", parser)
			command.Env = append(os.Environ(), "TESTPLAY_DEV_DRIVE_SCRIPT="+script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("parse failed: %v\n%s", err, output)
			}
		})
	}
}

func TestPowerShellUTF8PreludePreservesNativeEvidenceText(t *testing.T) {
	want := "개발자 드라이브"
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", powershellUTF8Prelude+"[Console]::Out.Write('"+want+"')")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("UTF-8 probe failed: %v\n%s", err, output)
	}
	if string(output) != want {
		t.Fatalf("decoded output=%q want=%q bytes=%x", output, want, output)
	}
}

func TestDevDriveInitializerHasNoGenericReFSFallback(t *testing.T) {
	if strings.Contains(initializeDevDriveDiskScript, "-FileSystem ReFS") {
		t.Fatal("generic ReFS formatting remains in the Dev Drive initializer")
	}
	for _, required := range []string{
		"Get-AvailableTemporaryDriveLetter",
		"Format-Volume -DriveLetter ([char]$temporaryLetter) -DevDrive",
		"Add-PartitionAccessPath -InputObject $partition -AccessPath $mountPath",
		"Remove-PartitionAccessPath -InputObject $partition -AccessPath $temporaryAccessPath",
		"fsutil.exe devdrv query",
		"temporary-drive-letter-unavailable",
		"temporary-drive-letter-cleanup-failed",
	} {
		if !strings.Contains(initializeDevDriveDiskScript, required) {
			t.Fatalf("initializer is missing %q", required)
		}
	}
	for _, forbidden := range []string{"devdrv enable", "devdrv disable", "devdrv trust", "Set-MpPreference", "reg.exe"} {
		if strings.Contains(strings.ToLower(devDriveCapabilityScript+initializeDevDriveDiskScript), strings.ToLower(forbidden)) {
			t.Fatalf("forbidden system mutation %q is present", forbidden)
		}
	}
}

func TestDevDriveInitializerSelectsOnlyUnusedDriveLetters(t *testing.T) {
	for _, required := range []string{"Get-Volume", "Get-PSDrive", "Get-Partition", "$used.Contains($candidate)", "[char]'Z'", "[char]'D'"} {
		if !strings.Contains(initializeDevDriveDiskScript, required) {
			t.Fatalf("temporary drive-letter selection is missing %q", required)
		}
	}
	formatIndex := strings.Index(initializeDevDriveDiskScript, "Format-Volume")
	privateMountIndex := strings.Index(initializeDevDriveDiskScript, "Add-PartitionAccessPath -InputObject $partition -AccessPath $mountPath")
	removeLetterIndex := strings.Index(initializeDevDriveDiskScript, "Remove-PartitionAccessPath -InputObject $partition -AccessPath $temporaryAccessPath")
	if formatIndex < 0 || privateMountIndex <= formatIndex || removeLetterIndex <= privateMountIndex {
		t.Fatalf("unsafe Dev Drive access-path order: format=%d mount=%d remove=%d", formatIndex, privateMountIndex, removeLetterIndex)
	}
}

func TestExistingDevDriveInspectionNeverFormats(t *testing.T) {
	if strings.Contains(inspectDevDriveVolumeScript, "Format-Volume") || strings.Contains(inspectDevDriveVolumeScript, "Add-PartitionAccessPath") {
		t.Fatal("existing Dev Drive inspection must not format or add a second access path")
	}
	for _, required := range []string{"FileSystemType.ToString() -ne 'ReFS'", "fsutil.exe devdrv query", "privateMountVerified = $true"} {
		if !strings.Contains(inspectDevDriveVolumeScript, required) {
			t.Fatalf("inspection is missing %q", required)
		}
	}
}

func TestDevDriveFailuresHaveStableCodes(t *testing.T) {
	tests := map[string]string{
		"Format-Volume DevDrive parameter missing": CodeDevDriveUnavailable,
		"Dev Drive globally disabled":              CodeDevDriveDisabled,
		"Dev Drive format failure":                 CodeDevDriveFormatFailed,
		"filesystem is not ReFS":                   CodeDevDriveVerificationFailed,
		"fsutil query failure":                     CodeDevDriveVerificationFailed,
		"private mount failure":                    CodeDevDriveVerificationFailed,
		"no temporary drive letter":                CodeTemporaryDriveLetterUnavailable,
		"temporary letter already used":            CodeTemporaryDriveLetterUnavailable,
		"temporary letter removal failure":         CodeTemporaryDriveLetterCleanupFailed,
	}
	for name, code := range tests {
		t.Run(name, func(t *testing.T) {
			err := classifyDevDriveError("test", "path", errors.New("wrapper: "+code+": injected"), CodeMountFailed)
			var storageErr *Error
			if !errors.As(err, &storageErr) || storageErr.Code != code {
				t.Fatalf("code=%q err=%v", code, err)
			}
		})
	}
}

func TestDevDriveInitializerTagsEveryProvisioningFailureBoundary(t *testing.T) {
	for _, required := range []string{
		"Format-Volume has no DevDrive parameter",
		"temporary-drive-letter-unavailable: no unused drive letter",
		"temporary-drive-letter-unavailable: failed to assign",
		"dev-drive-format-failed:",
		"dev-drive-verification-failed: filesystem=",
		"dev-drive-verification-failed: fsutil devdrv query",
		"dev-drive-verification-failed: private directory mount failed",
		"temporary-drive-letter-cleanup-failed: failed to remove",
	} {
		if !strings.Contains(devDriveCapabilityScript+initializeDevDriveDiskScript, required) {
			t.Fatalf("provisioning failure boundary is missing %q", required)
		}
	}
}

func TestDevDriveCapabilityCheckIsReadOnly(t *testing.T) {
	for _, required := range []string{"Format-Volume", "Parameters.ContainsKey('DevDrive')", "fsutil devdrv query", "dev-drive-disabled"} {
		if !strings.Contains(devDriveCapabilityScript, required) {
			t.Fatalf("capability check is missing %q", required)
		}
	}
	for _, forbidden := range []string{"devdrv enable", "devdrv disable", "devdrv trust"} {
		if strings.Contains(strings.ToLower(devDriveCapabilityScript), forbidden) {
			t.Fatalf("capability check mutates global state with %q", forbidden)
		}
	}
}
