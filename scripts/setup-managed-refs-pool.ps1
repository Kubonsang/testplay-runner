param(
  [string]$StorageRoot = (Join-Path $env:LOCALAPPDATA 'TestPlay\Storage'),
  [string]$ArtifactRoot = (Join-Path $env:TEMP 'testplay-refs-setup-artifacts')
)

$ErrorActionPreference = 'Stop'
$poolFile = Join-Path $StorageRoot 'managed-library-pool.vhdx'
$mountRoot = Join-Path $StorageRoot 'mount'
$ownerFile = Join-Path $StorageRoot 'pool-owner.json'

foreach ($path in @($poolFile, $ownerFile)) {
  if (Test-Path -LiteralPath $path) {
    throw "existing Managed Dev Drive pool detected; refusing to overwrite or reformat: $path"
  }
}
if (Test-Path -LiteralPath $StorageRoot) {
  $entries = @(Get-ChildItem -LiteralPath $StorageRoot -Force -ErrorAction Stop)
  if ($entries.Count -ne 0) {
    throw "storage root is not empty; refusing setup: $StorageRoot"
  }
}

$env:TESTPLAY_REFS_POOL_FILE = $poolFile
$env:TESTPLAY_REFS_MOUNT_ROOT = $mountRoot
$env:TESTPLAY_REFS_ARTIFACT_ROOT = $ArtifactRoot
$env:TESTPLAY_REFS_MAX_BYTES = '68719476736'

Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
New-Item -ItemType Directory -Path $ArtifactRoot -Force | Out-Null
$binary = Join-Path $ArtifactRoot 'testplay-refs-probe.exe'

go build -o $binary ./cmd/testplay-refs-probe
if ($LASTEXITCODE -ne 0) { throw "probe build failed with exit code $LASTEXITCODE" }

function Invoke-SetupOperation([string]$Operation) {
  $output = & $binary $Operation `
    --root $StorageRoot `
    --pool-file $poolFile `
    --mount-root $mountRoot `
    --max-bytes 68719476736
  $exitCode = $LASTEXITCODE
  $output | Set-Content -LiteralPath (Join-Path $ArtifactRoot "$Operation.json") -Encoding utf8
  if ($exitCode -ne 0) { throw "$Operation failed with exit code ${exitCode}: $output" }
  $result = $output | ConvertFrom-Json
  if ($null -ne $result.devDrive.queryOutput) {
    $result.devDrive.queryOutput | Set-Content -LiteralPath (Join-Path $ArtifactRoot "$Operation-dev-drive-query.txt") -Encoding utf8 -NoNewline
  }
  return $result
}

$setup = Invoke-SetupOperation 'setup'
if ($setup.status -ne 'PASS' -or $setup.windowsProvider -ne 'dev-drive-vhdx' -or $setup.volumeKind -ne 'Dev Drive' -or $setup.devDrive.formatSucceeded -ne $true -or $setup.devDrive.temporaryDriveLetterRemoved -ne $true) {
  throw 'setup evidence did not prove a detached persistent Managed Dev Drive VHDX'
}
$status = Invoke-SetupOperation 'status'
if ($status.status -ne 'READY' -or $status.devDrive.formatAttempted -ne $false -or [int]$status.devDrive.queryExitCode -ne 0) {
  throw 'status did not reattach and verify the persistent Managed Dev Drive VHDX'
}
if (-not (Test-Path -LiteralPath $poolFile)) { throw 'persistent VHDX is missing after setup' }
if (-not (Test-Path -LiteralPath $mountRoot)) { throw 'private mount directory is missing after detach' }
$mountItem = Get-Item -LiteralPath $mountRoot -Force
if (($mountItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'private mount remained attached after setup' }

[pscustomobject]@{
  status = 'READY'
  windowsProvider = 'dev-drive-vhdx'
  volumeKind = 'Dev Drive'
  poolFile = $poolFile
  mountRoot = $mountRoot
  artifactRoot = $ArtifactRoot
  vhdxPreserved = $true
  detached = $true
  maximumBytes = 68719476736
  softBudgetBytes = 15032385536
} | ConvertTo-Json -Depth 4
