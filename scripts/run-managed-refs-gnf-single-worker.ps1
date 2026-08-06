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
  @(
    Get-Disk -ErrorAction Stop |
      Where-Object { $_.BusType.ToString() -eq 'File Backed Virtual' } |
      Sort-Object Number |
      Select-Object Number, FriendlyName, SerialNumber, PartitionStyle, IsOffline, IsReadOnly
  )
}

function Get-DriveLetterSnapshot {
  @(
    Get-Volume -ErrorAction SilentlyContinue |
      Where-Object { $null -ne $_.DriveLetter } |
      ForEach-Object { [string]$_.DriveLetter } |
      Sort-Object -Unique
  )
}

function Get-ProcessSnapshot([string]$Name) {
  @(
    Get-Process -Name $Name -ErrorAction SilentlyContinue |
      Sort-Object Id |
      Select-Object Id, ProcessName, StartTime
  )
}

function Get-PathState([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) {
    return [ordered]@{ exists = $false; reparsePoint = $false }
  }
  $item = Get-Item -LiteralPath $Path -Force
  [ordered]@{
    exists = $true
    directory = [bool]$item.PSIsContainer
    reparsePoint = [bool](($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)
  }
}

if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required; this script does not request or bypass UAC.' }

$unityEditor = Resolve-RequiredPath 'TESTPLAY_REFS_UNITY_EDITOR_PATH' $env:TESTPLAY_REFS_UNITY_EDITOR_PATH
$projectPath = Resolve-RequiredPath 'TESTPLAY_REFS_GNF_PROJECT_PATH' $env:TESTPLAY_REFS_GNF_PROJECT_PATH
$unityCLIConnector = Resolve-RequiredPath 'TESTPLAY_REFS_GNF_UNITY_CLI_CONNECTOR_PATH' $env:TESTPLAY_REFS_GNF_UNITY_CLI_CONNECTOR_PATH
if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_MAX_BYTES)) { throw 'TESTPLAY_REFS_MAX_BYTES is required' }
$maximumBytes = [int64]$env:TESTPLAY_REFS_MAX_BYTES
$testTimeout = if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_UNITY_TEST_TIMEOUT)) { '30m' } else { $env:TESTPLAY_REFS_UNITY_TEST_TIMEOUT }
$softBudget = if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_GNF_SOFT_BUDGET_BYTES)) { [int64](14GB) } else { [int64]$env:TESTPLAY_REFS_GNF_SOFT_BUDGET_BYTES }
$workerReserve = if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_GNF_WORKER_RESERVE_BYTES)) { [int64](2GB) } else { [int64]$env:TESTPLAY_REFS_GNF_WORKER_RESERVE_BYTES }
$runId = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$storageRoot = Join-Path $env:LOCALAPPDATA "TestPlay\GNFSingleWorker-$runId"
$poolFile = Join-Path $storageRoot 'managed-library-pool.vhdx'
$mountRoot = Join-Path $storageRoot 'mount'
$artifactBase = Resolve-RequiredPath 'TESTPLAY_REFS_ARTIFACT_ROOT' $env:TESTPLAY_REFS_ARTIFACT_ROOT
$artifactRoot = "$artifactBase-$runId"
$zipPath = "$artifactRoot.zip"

foreach ($freshPath in @($storageRoot, $artifactRoot, $zipPath)) {
  if (Test-Path -LiteralPath $freshPath) { throw "fresh path already exists: $freshPath" }
}

New-Item -ItemType Directory -Path $artifactRoot | Out-Null
$binary = Join-Path $artifactRoot 'testplay-refs-gnf-single-worker.exe'
$transcript = Join-Path $artifactRoot 'terminal-transcript.txt'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))

$preDisks = @(Get-FileBackedDiskSnapshot)
$preLetters = @(Get-DriveLetterSnapshot)
$preUnity = @(Get-ProcessSnapshot 'Unity')
$preProbe = @(Get-ProcessSnapshot 'testplay-refs-probe')
$preGNF = @(Get-ProcessSnapshot 'testplay-refs-gnf-single-worker')
$preState = [ordered]@{
  measuredAt = [DateTime]::UtcNow
  elevated = $true
  windows = [ordered]@{
    edition = (Get-CimInstance Win32_OperatingSystem).Caption
    build = [Environment]::OSVersion.Version.ToString()
  }
  projectPath = $projectPath
  unityCLIConnectorPath = $unityCLIConnector
  storageRoot = Get-PathState $storageRoot
  vhdx = Get-PathState $poolFile
  mount = Get-PathState $mountRoot
  fileBackedDisks = @($preDisks)
  driveLetters = @($preLetters)
  unityProcesses = @($preUnity)
  probeProcesses = @($preProbe)
  gnfProcesses = @($preGNF)
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
    & go build -o $binary ./cmd/testplay-refs-gnf-single-worker
    if ($LASTEXITCODE -ne 0) { throw "GNF harness build failed with exit code $LASTEXITCODE" }
  }
  finally {
    Pop-Location
  }
  & $binary `
    --unity-editor $unityEditor `
    --project $projectPath `
    --unity-cli-connector $unityCLIConnector `
    --artifact-root $artifactRoot `
    --storage-root $storageRoot `
    --pool-file $poolFile `
    --mount-root $mountRoot `
    --max-bytes $maximumBytes `
    --soft-budget-bytes $softBudget `
    --worker-reserve-bytes $workerReserve `
    --test-timeout $testTimeout
  $runExitCode = $LASTEXITCODE
  if ($runExitCode -ne 0) { throw "GNF harness failed with exit code $runExitCode" }
}
catch {
  $runFailure = $_.Exception.Message
}
finally {
  if ($transcriptStarted) { Stop-Transcript | Out-Null }
}

$postDisks = @(Get-FileBackedDiskSnapshot)
$postLetters = @(Get-DriveLetterSnapshot)
$postUnity = @(Get-ProcessSnapshot 'Unity')
$postProbe = @(Get-ProcessSnapshot 'testplay-refs-probe')
$postGNF = @(Get-ProcessSnapshot 'testplay-refs-gnf-single-worker')
$newDisks = @(Compare-IDs $preDisks $postDisks 'Number')
$newLetters = @($postLetters | Where-Object { $preLetters -notcontains $_ })
$newUnity = @(Compare-IDs $preUnity $postUnity 'Id')
$newProbe = @(Compare-IDs $preProbe $postProbe 'Id')
$newGNF = @(Compare-IDs $preGNF $postGNF 'Id')
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
  gnfProcesses = @($postGNF)
  newGNFProcesses = @($newGNF)
}
$postState | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $artifactRoot 'post-state.json') -Encoding utf8

$summaryPath = Join-Path $artifactRoot 'summary.json'
if (Test-Path -LiteralPath $summaryPath) {
  $summary = Get-Content -Raw -LiteralPath $summaryPath | ConvertFrom-Json
} else {
  $summary = [pscustomobject]@{ schemaVersion = 1; status = 'FAILED'; verdict = 'FAILED'; cleanupState = 'uncertain' }
}
$outerZero = (
  $newDisks.Count -eq 0 -and
  $newLetters.Count -eq 0 -and
  $newUnity.Count -eq 0 -and
  $newProbe.Count -eq 0 -and
  $newGNF.Count -eq 0 -and
  -not $postState.storageRoot.exists -and
  -not $postState.vhdx.exists -and
  -not $postState.owner.exists -and
  -not $postState.pendingOwner.exists -and
  -not $postState.mount.exists
)
$summary | Add-Member -Force NoteProperty outerResidual ([ordered]@{
  status = if ($outerZero) { 'MEASURED_ZERO' } else { 'MEASURED_NONZERO' }
  attachedDisks = [ordered]@{ measured = $true; count = $newDisks.Count }
  temporaryDriveLetters = [ordered]@{ measured = $true; count = $newLetters.Count }
  unityProcesses = [ordered]@{ measured = $true; count = $newUnity.Count }
  probeProcesses = [ordered]@{ measured = $true; count = $newProbe.Count }
  gnfProcesses = [ordered]@{ measured = $true; count = $newGNF.Count }
  storageRoot = [ordered]@{ measured = $true; count = [int]$postState.storageRoot.exists }
  vhdx = [ordered]@{ measured = $true; count = [int]$postState.vhdx.exists }
  mount = [ordered]@{ measured = $true; count = [int]$postState.mount.exists }
})
if ($runExitCode -ne 0 -or -not $outerZero -or $runFailure) {
  if ($summary.verdict -ne 'BLOCKED') {
    $summary.status = 'FAILED'
    $summary.verdict = 'FAILED'
  }
  $summary | Add-Member -Force NoteProperty outerError $runFailure
}
$summary | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $summaryPath -Encoding utf8

$artifactFiles = @(
  Get-ChildItem -LiteralPath $artifactRoot -File |
    Where-Object { $_.FullName -ne $binary }
)
if ($artifactFiles.Count -eq 0) { throw "no evidence files available for archive: $artifactRoot" }
Compress-Archive -LiteralPath @($artifactFiles.FullName) -DestinationPath $zipPath
$hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash
Write-Output "GNF_STATUS=$($summary.verdict)"
Write-Output "GNF_ARTIFACT_ROOT=$artifactRoot"
Write-Output "GNF_STORAGE_ROOT=$storageRoot"
Write-Output "GNF_ARTIFACT_ZIP=$zipPath"
Write-Output "GNF_ARTIFACT_SHA256=$hash"
if ($runExitCode -ne 0 -or -not $outerZero -or $runFailure) { exit 1 }
