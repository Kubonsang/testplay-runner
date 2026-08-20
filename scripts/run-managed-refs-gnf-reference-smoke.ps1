[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$env:TESTPLAY_REFS_GNF_REFERENCE_SMOKE = '1'
$env:TESTPLAY_REFS_GNF_WORKERS = '1'
& (Join-Path $PSScriptRoot 'run-managed-refs-gnf-single-worker.ps1')
exit $LASTEXITCODE
