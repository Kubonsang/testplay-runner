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
Write-Output 'managed ReFS Unity Phase 2 snapshot tests: PASS'
