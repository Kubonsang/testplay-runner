Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'managed-refs-unity-phase2-snapshot.ps1')

$empty = @(Compare-IDs @() @() 'Id')
$single = @(Compare-IDs @([pscustomobject]@{ Id = 1 }) @([pscustomobject]@{ Id = 1 }, [pscustomobject]@{ Id = 2 }) 'Id')
$multiple = @(Compare-IDs @([pscustomobject]@{ Id = 1 }) @([pscustomobject]@{ Id = 2 }, [pscustomobject]@{ Id = 3 }) 'Id')
if ($empty.Count -ne 0 -or $single.Count -ne 1 -or $multiple.Count -ne 2) { throw 'Compare-IDs did not preserve array counts' }
$json = [ordered]@{ empty = @($empty); single = @($single); multiple = @($multiple) } | ConvertTo-Json -Depth 4 | ConvertFrom-Json
if ($json.empty.Count -ne 0 -or $json.single.Count -ne 1 -or $json.multiple.Count -ne 2) { throw 'snapshot JSON properties are not stable arrays' }
Write-Output 'managed ReFS Unity parallel snapshot tests: PASS'
