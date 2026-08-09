Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-CalculatedBudget([int64]$UsedBytes, [int]$Workers) {
  $required = $UsedBytes + ([int64]$Workers * 2GB) + 4GB
  [int64]([Math]::Ceiling($required / [double](1GB)) * 1GB)
}

if ((Get-CalculatedBudget (5GB) 2) -ne 13GB) { throw '2-worker sizing formula changed' }
if ((Get-CalculatedBudget (5GB) 4) -ne 17GB) { throw '4-worker sizing formula changed' }
if ((Get-CalculatedBudget (5GB) 8) -ne 25GB) { throw '8-worker sizing formula changed' }
if ((Get-CalculatedBudget ((5GB) + 1) 8) -ne 26GB) { throw 'GiB round-up changed' }

$ladder = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'run-managed-refs-worker-ladder.ps1')
foreach ($required in @(
  'nvidia-only-gate',
  'gnf-ntfs-reference-smoke',
  'refs-phase1',
  'fixture-single',
  'fixture-sizing',
  'foreach ($count in @(2, 4, 8))',
  'gnf-sizing',
  'gnf-single',
  'GNF_WORKER_LADDER_2_4_8_COMPATIBLE',
  'amdRestoreRequired = $true'
)) {
  if (-not $ladder.Contains($required)) { throw "ladder fail-closed contract is missing: $required" }
}

$gate = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'test-managed-refs-nvidia-only-gate.ps1')
foreach ($forbidden in @('& Disable-PnpDevice', 'Driver Verifier', 'Set-ItemProperty')) {
  if ($gate.Contains($forbidden)) { throw "hardware gate must remain read-only: $forbidden" }
}
foreach ($required in @('problemCode -ne 22', 'NVIDIA_ONLY_READY', 'Disable-PnpDevice', 'Enable-PnpDevice')) {
  if (-not $gate.Contains($required)) { throw "hardware gate contract is missing: $required" }
}

Write-Output 'managed ReFS worker ladder snapshot tests: PASS'
