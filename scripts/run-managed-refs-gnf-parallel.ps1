[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$count = [int]$env:TESTPLAY_REFS_GNF_WORKERS
if ($count -notin @(2, 4, 8)) {
  throw 'TESTPLAY_REFS_GNF_WORKERS must be 2, 4, or 8'
}
$env:TESTPLAY_REFS_GNF_REFERENCE_SMOKE = '0'
& (Join-Path $PSScriptRoot 'run-managed-refs-gnf-single-worker.ps1')
exit $LASTEXITCODE
