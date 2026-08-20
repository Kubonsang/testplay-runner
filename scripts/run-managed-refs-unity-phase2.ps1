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
$fixturePath = Resolve-RequiredPath 'TESTPLAY_REFS_UNITY_FIXTURE_PATH' $env:TESTPLAY_REFS_UNITY_FIXTURE_PATH
$poolFile = Resolve-RequiredPath 'TESTPLAY_REFS_POOL_FILE' $env:TESTPLAY_REFS_POOL_FILE
$mountRoot = Resolve-RequiredPath 'TESTPLAY_REFS_MOUNT_ROOT' $env:TESTPLAY_REFS_MOUNT_ROOT
$artifactRoot = Resolve-RequiredPath 'TESTPLAY_REFS_ARTIFACT_ROOT' $env:TESTPLAY_REFS_ARTIFACT_ROOT
if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_MAX_BYTES)) { throw 'TESTPLAY_REFS_MAX_BYTES is required' }
$maximumBytes = [int64]$env:TESTPLAY_REFS_MAX_BYTES
$testTimeout = if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_UNITY_TEST_TIMEOUT)) { '20m' } else { $env:TESTPLAY_REFS_UNITY_TEST_TIMEOUT }
$storageRoot = Split-Path -Parent $poolFile

if ((Split-Path -Parent $mountRoot) -ne $storageRoot) { throw 'mount root and VHDX must be direct children of the same storage root' }
if (Test-Path -LiteralPath $storageRoot) { throw "fresh storage root already exists: $storageRoot" }
if (Test-Path -LiteralPath $artifactRoot) { throw "fresh artifact root already exists: $artifactRoot" }
$zipPath = "$artifactRoot.zip"
if (Test-Path -LiteralPath $zipPath) { throw "artifact ZIP already exists: $zipPath" }

New-Item -ItemType Directory -Path $artifactRoot | Out-Null
$binary = Join-Path $artifactRoot 'testplay-refs-unity-phase2.exe'
$transcript = Join-Path $artifactRoot 'terminal-transcript.txt'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))

$preDisks = @(Get-FileBackedDiskSnapshot)
$preLetters = @(Get-DriveLetterSnapshot)
$preUnity = @(Get-ProcessSnapshot 'Unity')
$preProbe = @(Get-ProcessSnapshot 'testplay-refs-probe')
$prePhase2 = @(Get-ProcessSnapshot 'testplay-refs-unity-phase2')
$preState = [ordered]@{
  measuredAt = [DateTime]::UtcNow
  elevated = $true
  windows = [ordered]@{
    edition = (Get-CimInstance Win32_OperatingSystem).Caption
    build = [Environment]::OSVersion.Version.ToString()
  }
  storageRoot = Get-PathState $storageRoot
  vhdx = Get-PathState $poolFile
  mount = Get-PathState $mountRoot
  fileBackedDisks = $preDisks
  driveLetters = $preLetters
  unityProcesses = $preUnity
  probeProcesses = $preProbe
  phase2Processes = $prePhase2
}
$preState | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $artifactRoot 'pre-state.json') -Encoding utf8

$runExitCode = 1
$runFailure = $null
$transcriptStarted = $false
try {
  Start-Transcript -LiteralPath $transcript -Force | Out-Null
  $transcriptStarted = $true
  & go build -o $binary ./cmd/testplay-refs-unity-phase2
  if ($LASTEXITCODE -ne 0) { throw "Phase 2 harness build failed with exit code $LASTEXITCODE" }
  & $binary `
    --unity-editor $unityEditor `
    --fixture $fixturePath `
    --artifact-root $artifactRoot `
    --storage-root $storageRoot `
    --pool-file $poolFile `
    --mount-root $mountRoot `
    --max-bytes $maximumBytes `
    --test-timeout $testTimeout
  $runExitCode = $LASTEXITCODE
  if ($runExitCode -ne 0) { throw "Phase 2 harness failed with exit code $runExitCode" }
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
$postPhase2 = @(Get-ProcessSnapshot 'testplay-refs-unity-phase2')
$newDisks = @(Compare-IDs $preDisks $postDisks 'Number')
$newLetters = @($postLetters | Where-Object { $preLetters -notcontains $_ })
$newUnity = @(Compare-IDs $preUnity $postUnity 'Id')
$newProbe = @(Compare-IDs $preProbe $postProbe 'Id')
$newPhase2 = @(Compare-IDs $prePhase2 $postPhase2 'Id')
$postState = [ordered]@{
  measuredAt = [DateTime]::UtcNow
  storageRoot = Get-PathState $storageRoot
  vhdx = Get-PathState $poolFile
  owner = Get-PathState (Join-Path $storageRoot 'pool-owner.json')
  pendingOwner = Get-PathState (Join-Path $storageRoot 'pool-owner.pending.json')
  mount = Get-PathState $mountRoot
  fileBackedDisks = $postDisks
  newFileBackedDisks = @($newDisks)
  driveLetters = $postLetters
  newDriveLetters = $newLetters
  unityProcesses = $postUnity
  newUnityProcesses = @($newUnity)
  probeProcesses = $postProbe
  newProbeProcesses = @($newProbe)
  phase2Processes = $postPhase2
  newPhase2Processes = @($newPhase2)
}
$postState | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $artifactRoot 'post-state.json') -Encoding utf8

$summaryPath = Join-Path $artifactRoot 'summary.json'
if (Test-Path -LiteralPath $summaryPath) {
  $summary = Get-Content -Raw -LiteralPath $summaryPath | ConvertFrom-Json
} else {
  $summary = [pscustomobject]@{ schemaVersion = 1; status = 'FAILED'; verdict = 'FAILED' }
}
$outerZero = (
  $runExitCode -eq 0 -and
  $newDisks.Count -eq 0 -and
  $newLetters.Count -eq 0 -and
  $newUnity.Count -eq 0 -and
  $newProbe.Count -eq 0 -and
  $newPhase2.Count -eq 0 -and
  -not $postState.storageRoot.exists -and
  -not $postState.vhdx.exists -and
  -not $postState.mount.exists
)
$summary | Add-Member -Force NoteProperty outerResidual ([ordered]@{
  status = if ($outerZero) { 'MEASURED_ZERO' } else { 'MEASURED_NONZERO' }
  attachedDisks = [ordered]@{ measured = $true; count = $newDisks.Count }
  temporaryDriveLetters = [ordered]@{ measured = $true; count = $newLetters.Count }
  unityProcesses = [ordered]@{ measured = $true; count = $newUnity.Count }
  probeProcesses = [ordered]@{ measured = $true; count = $newProbe.Count }
  phase2Processes = [ordered]@{ measured = $true; count = $newPhase2.Count }
  storageRoot = [ordered]@{ measured = $true; count = [int]$postState.storageRoot.exists }
  vhdx = [ordered]@{ measured = $true; count = [int]$postState.vhdx.exists }
  mount = [ordered]@{ measured = $true; count = [int]$postState.mount.exists }
})
if (-not $outerZero -or $runFailure) {
  $summary.status = 'FAILED'
  $summary.verdict = 'FAILED'
  $summary | Add-Member -Force NoteProperty outerError $runFailure
}
$summary | ConvertTo-Json -Depth 16 | Set-Content -LiteralPath $summaryPath -Encoding utf8

Compress-Archive -Path (Join-Path $artifactRoot '*') -DestinationPath $zipPath
$hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash
Write-Output "UNITY_PHASE2_STATUS=$($summary.verdict)"
Write-Output "UNITY_PHASE2_ARTIFACT_ZIP=$zipPath"
Write-Output "UNITY_PHASE2_ARTIFACT_SHA256=$hash"
if (-not $outerZero -or $runFailure) { exit 1 }
