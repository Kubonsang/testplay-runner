Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'managed-refs-unity-phase2-snapshot.ps1')

function Assert-ArrayResult($Before, $After, [int]$Expected) {
  $result = @(Compare-IDs $Before $After 'Id')
  if ($result -isnot [array] -or $result.Count -ne $Expected) {
    throw "Compare-IDs result was not an array with Count=$Expected"
  }
  $json = [ordered]@{ newProcesses = @($result) } | ConvertTo-Json -Depth 4
  $roundTrip = $json | ConvertFrom-Json
  if ($roundTrip.newProcesses -isnot [array] -or @($roundTrip.newProcesses).Count -ne $Expected) {
    throw "post-state JSON property was not an array with Count=${Expected}: $json"
  }
}

$before = @([pscustomobject]@{ Id = 1 })
Assert-ArrayResult $before @([pscustomobject]@{ Id = 1 }) 0
Assert-ArrayResult $before @([pscustomobject]@{ Id = 1 }, [pscustomobject]@{ Id = 2 }) 1
Assert-ArrayResult $before @([pscustomobject]@{ Id = 1 }, [pscustomobject]@{ Id = 2 }, [pscustomobject]@{ Id = 3 }) 2

$script = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'run-managed-refs-gnf-single-worker.ps1')
foreach ($required in @(
  '$newDisks = @(',
  '$newLetters = @(',
  '$newUnity = @(',
  '$newProbe = @(',
  '$newGNF = @(',
  'newFileBackedDisks = @($newDisks)',
  'newUnityProcesses = @($newUnity)',
  'newProbeProcesses = @($newProbe)',
  'newGNFProcesses = @($newGNF)'
  'TESTPLAY_REFS_GNF_WORKERS'
  'testplay-refs-gnf-reference-smoke'
  'testplay-refs-gnf-parallel'
  '--baseline-sizing-used-bytes'
)) {
  if (-not $script.Contains($required)) {
    throw "GNF snapshot contract is missing: $required"
  }
}

foreach ($wrapper in @('run-managed-refs-gnf-reference-smoke.ps1', 'run-managed-refs-gnf-parallel.ps1')) {
  if (-not (Test-Path -LiteralPath (Join-Path $PSScriptRoot $wrapper))) { throw "missing GNF wrapper: $wrapper" }
}

Write-Output 'managed ReFS GNF single-worker snapshot tests: PASS'
