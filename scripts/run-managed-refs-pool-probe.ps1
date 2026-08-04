param(
  [switch]$RemoveAfter
)

$ErrorActionPreference = 'Stop'
$required = @(
  'TESTPLAY_REFS_POOL_FILE',
  'TESTPLAY_REFS_MOUNT_ROOT',
  'TESTPLAY_REFS_ARTIFACT_ROOT',
  'TESTPLAY_REFS_MAX_BYTES'
)

foreach ($name in $required) {
  if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) {
    throw "required environment variable is missing: $name"
  }
}

$artifactRoot = [IO.Path]::GetFullPath($env:TESTPLAY_REFS_ARTIFACT_ROOT)
$poolFile = [IO.Path]::GetFullPath($env:TESTPLAY_REFS_POOL_FILE)
$mountRoot = [IO.Path]::GetFullPath($env:TESTPLAY_REFS_MOUNT_ROOT)
$storageRoot = Split-Path -Parent $poolFile
New-Item -ItemType Directory -Path $artifactRoot -Force | Out-Null

$binary = Join-Path $artifactRoot 'testplay-refs-probe.exe'
go build -o $binary ./cmd/testplay-refs-probe
if ($LASTEXITCODE -ne 0) { throw "probe build failed with exit code $LASTEXITCODE" }

function Invoke-ProbeCommand([string]$Operation) {
  $output = & $binary $Operation `
    --root $storageRoot `
    --pool-file $poolFile `
    --mount-root $mountRoot `
    --max-bytes ([int64]$env:TESTPLAY_REFS_MAX_BYTES)
  $exit = $LASTEXITCODE
  $artifact = Join-Path $artifactRoot "$Operation.json"
  $output | Set-Content -LiteralPath $artifact -Encoding utf8
  if ($exit -ne 0) {
    throw "$Operation failed with exit code ${exit}: $output"
  }
  $result = $output | ConvertFrom-Json
  if ($result.fallbackUsed -ne $false) { throw "$Operation used a forbidden fallback" }
  return $result
}

$setup = Invoke-ProbeCommand 'setup'
$probe = Invoke-ProbeCommand 'probe'
$status = Invoke-ProbeCommand 'status'

if ($setup.volume.filesystem -ne 'ReFS') {
  throw "setup filesystem mismatch: $($setup.volume.filesystem)"
}
if ($probe.volume.filesystem -ne 'ReFS' -or $probe.blockCloneSupported -ne $true) {
  throw 'native ReFS Block Clone evidence did not pass'
}

$summary = [ordered]@{
  status = 'PROMISING'
  nativeWindowsStatus = 'MEASURED'
  filesystem = $probe.volume.filesystem
  blockCloneSupported = $probe.blockCloneSupported
  physicalImageCreated = $probe.physicalImageCreated
  differencingChildCreated = $probe.differencingChildCreated
  sourceUnchanged = $probe.sourceUnchanged
  baselineUnchanged = $probe.baselineUnchanged
  unityCorrectness = 'NOT MEASURED'
  residual = $probe.residual
}
$summary | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $artifactRoot 'summary.json') -Encoding utf8

if ($RemoveAfter) {
  Invoke-ProbeCommand 'remove' | Out-Null
}

$summary | ConvertTo-Json -Depth 8
