[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding

function Test-Administrator {
  ([Security.Principal.WindowsPrincipal]::new(
    [Security.Principal.WindowsIdentity]::GetCurrent()
  )).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Resolve-RequiredPath([string]$Name, [string]$Value) {
  if ([string]::IsNullOrWhiteSpace($Value)) { throw "required environment variable is missing: $Name" }
  if (-not [IO.Path]::IsPathRooted($Value)) { throw "$Name must be absolute" }
  [IO.Path]::GetFullPath($Value).TrimEnd('\')
}

function Invoke-LadderScript([string]$Name, [string]$Path) {
  $stdoutPath = Join-Path $artifactRoot "$Name.stdout.txt"
  $stderrPath = Join-Path $artifactRoot "$Name.stderr.txt"
  $started = [DateTime]::UtcNow
  $process = Start-Process -FilePath 'powershell.exe' -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $Path) -Wait -PassThru -NoNewWindow -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath
  $completed = [DateTime]::UtcNow
  $record = [ordered]@{ name = $Name; startedAt = $started; completedAt = $completed; exitCode = $process.ExitCode; stdout = $stdoutPath; stderr = $stderrPath }
  $record | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $artifactRoot "$Name.process.json") -Encoding utf8
  if ($process.ExitCode -ne 0) { throw "$Name failed with exit code $($process.ExitCode)" }
  Get-Content -Raw -LiteralPath $stdoutPath
}

function Require-Output([string]$Output, [string]$Expected, [string]$Stage) {
  if (-not $Output.Contains($Expected)) { throw "$Stage did not report $Expected" }
}

function Get-OutputValue([string]$Output, [string]$Name) {
  $line = @($Output -split "`r?`n" | Where-Object { $_.StartsWith("$Name=") }) | Select-Object -Last 1
  if ([string]::IsNullOrWhiteSpace($line)) { throw "missing output $Name" }
  $line.Substring($Name.Length + 1).Trim()
}

function Get-CalculatedBudget([int64]$UsedBytes, [int]$Workers) {
  $oneGiB = 1GB
  $required = $UsedBytes + ([int64]$Workers * 2GB) + 4GB
  $rounded = [int64]([Math]::Ceiling($required / [double]$oneGiB) * $oneGiB)
  if ($rounded -gt 62GB) { throw "storage-budget-exceeded: workers=$Workers calculated=$rounded maximum=$(62GB)" }
  $rounded
}

if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$unityEditor = Resolve-RequiredPath 'TESTPLAY_REFS_UNITY_EDITOR_PATH' $env:TESTPLAY_REFS_UNITY_EDITOR_PATH
$fixture = Resolve-RequiredPath 'TESTPLAY_REFS_UNITY_FIXTURE_PATH' $env:TESTPLAY_REFS_UNITY_FIXTURE_PATH
$gnfProject = Resolve-RequiredPath 'TESTPLAY_REFS_GNF_PROJECT_PATH' $env:TESTPLAY_REFS_GNF_PROJECT_PATH
$connector = Resolve-RequiredPath 'TESTPLAY_REFS_GNF_UNITY_CLI_CONNECTOR_PATH' $env:TESTPLAY_REFS_GNF_UNITY_CLI_CONNECTOR_PATH
$artifactBase = Resolve-RequiredPath 'TESTPLAY_REFS_LADDER_ARTIFACT_ROOT' $env:TESTPLAY_REFS_LADDER_ARTIFACT_ROOT
$stamp = [DateTime]::Now.ToString('yyyyMMdd-HHmmss-fff')
$artifactRoot = "$artifactBase-$stamp"
$zipPath = "$artifactRoot.zip"
if ((Test-Path -LiteralPath $artifactRoot) -or (Test-Path -LiteralPath $zipPath)) { throw 'fresh ladder artifact path required' }
New-Item -ItemType Directory -Path $artifactRoot | Out-Null

$summary = [ordered]@{ schemaVersion = 1; status = 'FAILED'; verdict = 'FAILED'; startedAt = [DateTime]::UtcNow; completedStages = @(); failedStage = $null; error = $null; amdRestoreRequired = $true; notMeasured = @('forced-termination general recovery', 'performance superiority', 'product CLI integration', 'production readiness', 'release readiness') }
$env:TESTPLAY_REFS_MAX_BYTES = '68719476736'
$env:TESTPLAY_REFS_UNITY_EDITOR_PATH = $unityEditor
$env:TESTPLAY_REFS_UNITY_FIXTURE_PATH = $fixture
$env:TESTPLAY_REFS_GNF_PROJECT_PATH = $gnfProject
$env:TESTPLAY_REFS_GNF_UNITY_CLI_CONNECTOR_PATH = $connector

try {
  $summary.failedStage = 'nvidia-only-gate'
  $env:TESTPLAY_REFS_HARDWARE_GATE_ARTIFACT_ROOT = Join-Path $artifactRoot 'nvidia-only-gate'
  $output = Invoke-LadderScript '01-nvidia-only-gate' (Join-Path $PSScriptRoot 'test-managed-refs-nvidia-only-gate.ps1')
  Require-Output $output 'NVIDIA_ONLY_STATUS=NVIDIA_ONLY_READY' 'nvidia-only-gate'
  $summary.completedStages += 'nvidia-only-gate'

  $summary.failedStage = 'gnf-ntfs-reference-smoke'
  $env:TESTPLAY_REFS_ARTIFACT_ROOT = Join-Path $artifactRoot 'gnf-reference-smoke'
  $output = Invoke-LadderScript '02-gnf-reference-smoke' (Join-Path $PSScriptRoot 'run-managed-refs-gnf-reference-smoke.ps1')
  Require-Output $output 'GNF_STATUS=GNF_NTFS_REFERENCE_STABLE' 'gnf-ntfs-reference-smoke'
  $summary.completedStages += 'gnf-ntfs-reference-smoke'

  $summary.failedStage = 'refs-phase1'
  $phase1Root = Join-Path $env:LOCALAPPDATA "TestPlay\WorkerLadderPhase1-$stamp"
  $env:TESTPLAY_REFS_POOL_FILE = Join-Path $phase1Root 'managed-library-pool.vhdx'
  $env:TESTPLAY_REFS_MOUNT_ROOT = Join-Path $phase1Root 'mount'
  $env:TESTPLAY_REFS_ARTIFACT_ROOT = Join-Path $artifactRoot 'refs-phase1'
  if ((Test-Path -LiteralPath $phase1Root) -or (Test-Path -LiteralPath $env:TESTPLAY_REFS_ARTIFACT_ROOT)) { throw 'fresh Phase 1 paths required' }
  $phase1Script = Join-Path $PSScriptRoot 'run-managed-refs-pool-probe.ps1'
  $phase1Stdout = Join-Path $artifactRoot '03-refs-phase1.stdout.txt'
  & $phase1Script -RemoveAfter *>&1 | Set-Content -LiteralPath $phase1Stdout -Encoding utf8
  $phase1Summary = Get-Content -Raw -LiteralPath (Join-Path $env:TESTPLAY_REFS_ARTIFACT_ROOT 'summary.json') | ConvertFrom-Json
  if ($phase1Summary.status -ne 'PROMISING') { throw "Phase 1 verdict=$($phase1Summary.status)" }
  $summary.completedStages += 'refs-phase1'

  $summary.failedStage = 'fixture-single'
  $fixtureSingleRoot = Join-Path $env:LOCALAPPDATA "TestPlay\WorkerLadderFixtureSingle-$stamp"
  $env:TESTPLAY_REFS_POOL_FILE = Join-Path $fixtureSingleRoot 'managed-library-pool.vhdx'
  $env:TESTPLAY_REFS_MOUNT_ROOT = Join-Path $fixtureSingleRoot 'mount'
  $env:TESTPLAY_REFS_ARTIFACT_ROOT = Join-Path $artifactRoot 'fixture-single'
  $output = Invoke-LadderScript '04-fixture-single' (Join-Path $PSScriptRoot 'run-managed-refs-unity-phase2.ps1')
  Require-Output $output 'UNITY_PHASE2_STATUS=UNITY_PHASE2_SINGLE_WORKER_COMPATIBLE' 'fixture-single'
  $summary.completedStages += 'fixture-single'

  $summary.failedStage = 'fixture-sizing'
  $env:TESTPLAY_REFS_PARALLEL_WORKERS = '8'
  $env:TESTPLAY_REFS_BASELINE_SIZING_ONLY = '1'
  $env:TESTPLAY_REFS_BASELINE_SIZING_USED_BYTES = '0'
  $env:TESTPLAY_REFS_SOFT_BUDGET_BYTES = [string](62GB)
  $env:TESTPLAY_REFS_ARTIFACT_ROOT = Join-Path $artifactRoot 'fixture-sizing'
  $output = Invoke-LadderScript '05-fixture-sizing' (Join-Path $PSScriptRoot 'run-managed-refs-unity-parallel.ps1')
  Require-Output $output 'UNITY_PARALLEL_STATUS=BASELINE_SIZING_COMPLETE' 'fixture-sizing'
  $fixtureSizingRoot = Get-OutputValue $output 'UNITY_PARALLEL_ARTIFACT_ZIP'
  $fixtureSizingArtifact = $fixtureSizingRoot.Substring(0, $fixtureSizingRoot.Length - 4)
  $fixtureUsed = [int64](Get-Content -Raw -LiteralPath (Join-Path $fixtureSizingArtifact 'baseline-sizing.json') | ConvertFrom-Json).usedAfterBaselineBytes
  $summary.completedStages += 'fixture-sizing'

  foreach ($count in @(2, 4, 8)) {
    $summary.failedStage = "fixture-$count"
    $env:TESTPLAY_REFS_PARALLEL_WORKERS = [string]$count
    $env:TESTPLAY_REFS_BASELINE_SIZING_ONLY = '0'
    $env:TESTPLAY_REFS_BASELINE_SIZING_USED_BYTES = [string]$fixtureUsed
    $env:TESTPLAY_REFS_SOFT_BUDGET_BYTES = [string](Get-CalculatedBudget $fixtureUsed $count)
    $env:TESTPLAY_REFS_ARTIFACT_ROOT = Join-Path $artifactRoot "fixture-$count"
    $output = Invoke-LadderScript "fixture-$count" (Join-Path $PSScriptRoot 'run-managed-refs-unity-parallel.ps1')
    $expected = @{ 2 = 'UNITY_PHASE2_TWO_WORKERS_COMPATIBLE'; 4 = 'UNITY_PHASE2_FOUR_WORKERS_COMPATIBLE'; 8 = 'UNITY_PHASE2_EIGHT_WORKERS_COMPATIBLE' }[$count]
    Require-Output $output "UNITY_PARALLEL_STATUS=$expected" "fixture-$count"
    $summary.completedStages += "fixture-$count"
  }

  $summary.failedStage = 'gnf-sizing'
  $env:TESTPLAY_REFS_GNF_WORKERS = '8'
  $env:TESTPLAY_REFS_BASELINE_SIZING_ONLY = '1'
  $env:TESTPLAY_REFS_BASELINE_SIZING_USED_BYTES = '0'
  $env:TESTPLAY_REFS_GNF_SOFT_BUDGET_BYTES = [string](62GB)
  $env:TESTPLAY_REFS_ARTIFACT_ROOT = Join-Path $artifactRoot 'gnf-sizing'
  $output = Invoke-LadderScript 'gnf-sizing' (Join-Path $PSScriptRoot 'run-managed-refs-gnf-parallel.ps1')
  Require-Output $output 'GNF_STATUS=BASELINE_SIZING_COMPLETE' 'gnf-sizing'
  $gnfSizingArtifact = Get-OutputValue $output 'GNF_ARTIFACT_ROOT'
  $gnfUsed = [int64](Get-Content -Raw -LiteralPath (Join-Path $gnfSizingArtifact 'baseline-sizing.json') | ConvertFrom-Json).usedAfterBaselineBytes
  $summary.completedStages += 'gnf-sizing'

  $summary.failedStage = 'gnf-single'
  $env:TESTPLAY_REFS_GNF_WORKERS = '1'
  $env:TESTPLAY_REFS_BASELINE_SIZING_ONLY = '0'
  $env:TESTPLAY_REFS_GNF_SOFT_BUDGET_BYTES = [string](14GB)
  $env:TESTPLAY_REFS_ARTIFACT_ROOT = Join-Path $artifactRoot 'gnf-single'
  $output = Invoke-LadderScript 'gnf-single' (Join-Path $PSScriptRoot 'run-managed-refs-gnf-single-worker.ps1')
  Require-Output $output 'GNF_STATUS=GNF_SINGLE_WORKER_COMPATIBLE' 'gnf-single'
  $summary.completedStages += 'gnf-single'

  foreach ($count in @(2, 4, 8)) {
    $summary.failedStage = "gnf-$count"
    $env:TESTPLAY_REFS_GNF_WORKERS = [string]$count
    $env:TESTPLAY_REFS_BASELINE_SIZING_USED_BYTES = [string]$gnfUsed
    $env:TESTPLAY_REFS_GNF_SOFT_BUDGET_BYTES = [string](Get-CalculatedBudget $gnfUsed $count)
    $env:TESTPLAY_REFS_ARTIFACT_ROOT = Join-Path $artifactRoot "gnf-$count"
    $output = Invoke-LadderScript "gnf-$count" (Join-Path $PSScriptRoot 'run-managed-refs-gnf-parallel.ps1')
    $expected = @{ 2 = 'GNF_TWO_WORKERS_COMPATIBLE'; 4 = 'GNF_FOUR_WORKERS_COMPATIBLE'; 8 = 'GNF_WORKER_LADDER_2_4_8_COMPATIBLE' }[$count]
    Require-Output $output "GNF_STATUS=$expected" "gnf-$count"
    $summary.completedStages += "gnf-$count"
  }
  $summary.status, $summary.verdict, $summary.failedStage = 'PASS', 'GNF_WORKER_LADDER_2_4_8_COMPATIBLE', $null
}
catch {
  $summary.error = $_.Exception.Message
}
finally {
  $summary.completedAt = [DateTime]::UtcNow
  $summary | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $artifactRoot 'ladder-summary.json') -Encoding utf8
  Compress-Archive -Path (Join-Path $artifactRoot '*') -DestinationPath $zipPath
  $hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash
  Write-Output "WORKER_LADDER_STATUS=$($summary.verdict)"
  Write-Output "WORKER_LADDER_ARTIFACT_ZIP=$zipPath"
  Write-Output "WORKER_LADDER_ARTIFACT_SHA256=$hash"
}
if ($summary.status -ne 'PASS') { exit 1 }
