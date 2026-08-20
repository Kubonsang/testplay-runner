[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
. (Join-Path $PSScriptRoot 'managed-refs-unity-phase2-snapshot.ps1')

function Test-Administrator {
  ([Security.Principal.WindowsPrincipal]::new(
    [Security.Principal.WindowsIdentity]::GetCurrent()
  )).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Resolve-RequiredPath([string]$Name, [string]$Value) {
  if ([string]::IsNullOrWhiteSpace($Value)) { throw "required environment variable is missing: $Name" }
  if (-not [IO.Path]::IsPathRooted($Value)) { throw "$Name must be absolute: $Value" }
  $full = [IO.Path]::GetFullPath($Value).TrimEnd('\')
  if ($full -eq [IO.Path]::GetPathRoot($full).TrimEnd('\')) { throw "$Name must not be a filesystem root" }
  $full
}

function Get-FileBackedDiskSnapshot {
  @(Get-Disk -ErrorAction Stop | Where-Object { $_.BusType.ToString() -eq 'File Backed Virtual' } | Sort-Object Number | Select-Object Number, FriendlyName, SerialNumber, PartitionStyle, IsOffline, IsReadOnly)
}

function Get-DriveLetterSnapshot {
  @(Get-Volume -ErrorAction SilentlyContinue | Where-Object { $null -ne $_.DriveLetter } | ForEach-Object { [string]$_.DriveLetter } | Sort-Object -Unique)
}

function Get-ProcessSnapshot([string]$Name) {
  @(Get-Process -Name $Name -ErrorAction SilentlyContinue | Sort-Object Id | Select-Object Id, ProcessName, StartTime)
}

function Get-PathState([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) { return [ordered]@{ exists = $false; reparsePoint = $false } }
  $item = Get-Item -LiteralPath $Path -Force
  [ordered]@{ exists = $true; directory = [bool]$item.PSIsContainer; reparsePoint = [bool](($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) }
}

if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required; this script does not request or bypass UAC.' }
$unityEditor = Resolve-RequiredPath 'TESTPLAY_REFS_UNITY_EDITOR_PATH' $env:TESTPLAY_REFS_UNITY_EDITOR_PATH
$fixturePath = Resolve-RequiredPath 'TESTPLAY_REFS_UNITY_FIXTURE_PATH' $env:TESTPLAY_REFS_UNITY_FIXTURE_PATH
$workerCount = [int]$env:TESTPLAY_REFS_PARALLEL_WORKERS
if ($workerCount -notin @(2, 4, 8)) { throw 'TESTPLAY_REFS_PARALLEL_WORKERS must be 2, 4, or 8' }
$artifactBase = Resolve-RequiredPath 'TESTPLAY_REFS_ARTIFACT_ROOT' $env:TESTPLAY_REFS_ARTIFACT_ROOT
$stamp = [DateTime]::Now.ToString('yyyyMMdd-HHmmss-fff')
$storageRoot = Join-Path $env:LOCALAPPDATA "TestPlay\UnityParallel-$stamp"
$artifactRoot = "$artifactBase-$stamp"
$poolFile = Join-Path $storageRoot 'managed-library-pool.vhdx'
$mountRoot = Join-Path $storageRoot 'mount'
$maximumBytes = 68719476736L
$softBudgetBytes = if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_SOFT_BUDGET_BYTES)) { 15032385536L } else { [int64]$env:TESTPLAY_REFS_SOFT_BUDGET_BYTES }
$workerReserveBytes = if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_WORKER_RESERVE_BYTES)) { 2147483648L } else { [int64]$env:TESTPLAY_REFS_WORKER_RESERVE_BYTES }
$sizingOnly = $env:TESTPLAY_REFS_BASELINE_SIZING_ONLY -eq '1'
$sizingUsedBytes = if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_BASELINE_SIZING_USED_BYTES)) { 0L } else { [int64]$env:TESTPLAY_REFS_BASELINE_SIZING_USED_BYTES }
$testTimeout = if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_UNITY_TEST_TIMEOUT)) { '20m' } else { $env:TESTPLAY_REFS_UNITY_TEST_TIMEOUT }
$zipPath = "$artifactRoot.zip"

foreach ($freshPath in @($storageRoot, $artifactRoot, $zipPath)) {
  if (Test-Path -LiteralPath $freshPath) { throw "fresh path already exists: $freshPath" }
}
New-Item -ItemType Directory -Path $artifactRoot | Out-Null
$binary = Join-Path $artifactRoot 'testplay-refs-unity-parallel.exe'
$transcript = Join-Path $artifactRoot 'terminal-transcript.txt'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))

$preDisks = @(Get-FileBackedDiskSnapshot)
$preLetters = @(Get-DriveLetterSnapshot)
$preUnity = @(Get-ProcessSnapshot 'Unity')
$preProbe = @(Get-ProcessSnapshot 'testplay-refs-probe')
$preParallel = @(Get-ProcessSnapshot 'testplay-refs-unity-parallel')
$preState = [ordered]@{
  measuredAt = [DateTime]::UtcNow
  elevated = $true
  windows = [ordered]@{ edition = (Get-CimInstance Win32_OperatingSystem).Caption; build = [Environment]::OSVersion.Version.ToString() }
  storageRoot = Get-PathState $storageRoot
  vhdx = Get-PathState $poolFile
  mount = Get-PathState $mountRoot
  fileBackedDisks = @($preDisks)
  driveLetters = @($preLetters)
  unityProcesses = @($preUnity)
  probeProcesses = @($preProbe)
  parallelProcesses = @($preParallel)
}
$preState | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $artifactRoot 'pre-state.json') -Encoding utf8

$runExitCode = 1
$runFailure = $null
$transcriptStarted = $false
try {
  Start-Transcript -LiteralPath $transcript -Force | Out-Null
  $transcriptStarted = $true
  Push-Location $repoRoot
  try {
    & go build -o $binary ./cmd/testplay-refs-unity-parallel
    if ($LASTEXITCODE -ne 0) { throw "parallel harness build failed with exit code $LASTEXITCODE" }
    $arguments = @(
      '--unity-editor', $unityEditor,
      '--fixture', $fixturePath,
      '--artifact-root', $artifactRoot,
      '--storage-root', $storageRoot,
      '--pool-file', $poolFile,
      '--mount-root', $mountRoot,
      '--max-bytes', $maximumBytes,
      '--soft-budget-bytes', $softBudgetBytes,
      '--worker-reserve-bytes', $workerReserveBytes,
      '--worker-count', $workerCount,
      '--baseline-sizing-used-bytes', $sizingUsedBytes,
      '--test-timeout', $testTimeout
    )
    if ($sizingOnly) { $arguments += '--sizing-only' }
    & $binary @arguments
    $runExitCode = $LASTEXITCODE
    if ($runExitCode -ne 0) { throw "parallel harness failed with exit code $runExitCode" }
  }
  finally { Pop-Location }
}
catch { $runFailure = $_.Exception.Message }
finally { if ($transcriptStarted) { Stop-Transcript | Out-Null } }

$postDisks = @(Get-FileBackedDiskSnapshot)
$postLetters = @(Get-DriveLetterSnapshot)
$postUnity = @(Get-ProcessSnapshot 'Unity')
$postProbe = @(Get-ProcessSnapshot 'testplay-refs-probe')
$postParallel = @(Get-ProcessSnapshot 'testplay-refs-unity-parallel')
$newDisks = @(Compare-IDs $preDisks $postDisks 'Number')
$newLetters = @($postLetters | Where-Object { $preLetters -notcontains $_ })
$newUnity = @(Compare-IDs $preUnity $postUnity 'Id')
$newProbe = @(Compare-IDs $preProbe $postProbe 'Id')
$newParallel = @(Compare-IDs $preParallel $postParallel 'Id')
$postState = [ordered]@{
  measuredAt = [DateTime]::UtcNow
  storageRoot = Get-PathState $storageRoot
  vhdx = Get-PathState $poolFile
  owner = Get-PathState (Join-Path $storageRoot 'pool-owner.json')
  pendingOwner = Get-PathState (Join-Path $storageRoot 'pool-owner.pending.json')
  mount = Get-PathState $mountRoot
  fileBackedDisks = @($postDisks)
  newFileBackedDisks = @($newDisks)
  driveLetters = @($postLetters)
  newDriveLetters = @($newLetters)
  unityProcesses = @($postUnity)
  newUnityProcesses = @($newUnity)
  probeProcesses = @($postProbe)
  newProbeProcesses = @($newProbe)
  parallelProcesses = @($postParallel)
  newParallelProcesses = @($newParallel)
}
$postState | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $artifactRoot 'post-state.json') -Encoding utf8

$summaryPath = Join-Path $artifactRoot 'summary.json'
$summary = if (Test-Path -LiteralPath $summaryPath) { Get-Content -Raw -LiteralPath $summaryPath | ConvertFrom-Json } else { [pscustomobject]@{ schemaVersion = 1; status = 'FAILED'; verdict = 'FAILED' } }
$outerZero = ($runExitCode -eq 0 -and $newDisks.Count -eq 0 -and $newLetters.Count -eq 0 -and $newUnity.Count -eq 0 -and $newProbe.Count -eq 0 -and $newParallel.Count -eq 0 -and -not $postState.storageRoot.exists -and -not $postState.vhdx.exists -and -not $postState.mount.exists -and -not $postState.owner.exists -and -not $postState.pendingOwner.exists)
$summary | Add-Member -Force NoteProperty outerResidual ([ordered]@{
  status = if ($outerZero) { 'MEASURED_ZERO' } else { 'MEASURED_NONZERO' }
  attachedDisks = [ordered]@{ measured = $true; count = $newDisks.Count }
  temporaryDriveLetters = [ordered]@{ measured = $true; count = $newLetters.Count }
  unityProcesses = [ordered]@{ measured = $true; count = $newUnity.Count }
  probeProcesses = [ordered]@{ measured = $true; count = $newProbe.Count }
  parallelProcesses = [ordered]@{ measured = $true; count = $newParallel.Count }
  storageRoot = [ordered]@{ measured = $true; count = [int]$postState.storageRoot.exists }
  vhdx = [ordered]@{ measured = $true; count = [int]$postState.vhdx.exists }
  mount = [ordered]@{ measured = $true; count = [int]$postState.mount.exists }
})
if (-not $outerZero -or $runFailure) {
  $summary.status = 'FAILED'
  $summary.verdict = 'FAILED'
  $summary | Add-Member -Force NoteProperty outerError $runFailure
}
$summary | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $summaryPath -Encoding utf8
Compress-Archive -Path (Join-Path $artifactRoot '*') -DestinationPath $zipPath
$hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash
Write-Output "UNITY_PARALLEL_STATUS=$($summary.verdict)"
Write-Output "UNITY_PARALLEL_ARTIFACT_ZIP=$zipPath"
Write-Output "UNITY_PARALLEL_ARTIFACT_SHA256=$hash"
if (-not $outerZero -or $runFailure) { exit 1 }
