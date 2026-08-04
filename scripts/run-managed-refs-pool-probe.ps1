param(
  [switch]$RemoveAfter
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($env:TESTPLAY_REFS_MAX_BYTES)) {
  $env:TESTPLAY_REFS_MAX_BYTES = '68719476736'
}
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

$summary = [ordered]@{
  status = 'FAILED'
  nativeWindowsStatus = 'NOT MEASURED'
  windowsProvider = 'dev-drive-vhdx'
  volumeKind = 'Dev Drive'
  devDrive = $null
  filesystem = $null
  blockCloneSupported = $false
  regularSyntheticClone = 'NOT MEASURED'
  sparseSyntheticClone = 'NOT MEASURED'
  allocateOnWriteIsolation = 'NOT MEASURED'
  physicalImageCreated = $false
  differencingChildCreated = $false
  fallbackUsed = $false
  sourceUnchanged = 'NOT MEASURED'
  baselineUnchanged = 'NOT MEASURED'
  residualStatus = 'NOT_MEASURED'
  probeProcesses = [ordered]@{ measured = $false; count = $null }
  setupResidual = $null
  probeResidual = $null
  statusResidual = $null
  removeResidual = $null
  unityCorrectness = 'NOT MEASURED'
  parallelIsolation = [ordered]@{
    workers1 = 'NOT MEASURED'
    workers2 = 'NOT MEASURED'
    workers4 = 'NOT MEASURED'
    workers8 = 'NOT MEASURED'
  }
  failure = $null
}

$binary = Join-Path $artifactRoot 'testplay-refs-probe.exe'

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
  if ($null -ne $result.devDrive -and $null -ne $result.devDrive.queryOutput) {
    $result.devDrive.queryOutput | Set-Content -LiteralPath (Join-Path $artifactRoot "$Operation-dev-drive-query.txt") -Encoding utf8 -NoNewline
  }
  return $result
}

function Assert-NoForbiddenPath($Result, [string]$Operation) {
  if ($Result.fallbackUsed -ne $false) { throw "$Operation used a forbidden fallback" }
  if ($Result.physicalImageCreated -ne $false) { throw "$Operation created a forbidden physical Image" }
  if ($Result.differencingChildCreated -ne $false) { throw "$Operation created a forbidden differencing child" }
}

function Assert-BinaryZeroResidual($Residual, [int]$ExpectedVhdxFiles, [string]$Operation) {
  $names = @(
    'activeBaselineUses',
    'workerLeaseJournals',
    'workerDirectories',
    'baselineCreationLocks',
    'baselineStagingDirs',
    'workerStagingDirs',
    'unknownLeaseArtifacts',
    'unknownBaselineEntries',
    'unknownWorkerArtifacts',
    'quarantineEntries',
    'reservationLocks',
    'baselineCoordinationLocks',
    'baselineMutationMarkers',
    'coordinationArtifacts',
    'syntheticProbeDirectories',
    'mountReparsePoints',
    'mountDirectoryEntries',
    'junctions',
    'attachedDisks'
  )
  foreach ($name in $names) {
    $metric = $Residual.$name
    if ($null -eq $metric -or $metric.measured -ne $true) { throw "$Operation residual $name was not measured" }
    if ([int]$metric.count -ne 0) { throw "$Operation residual $name is $($metric.count), expected zero" }
  }
  if ($null -eq $Residual.ownedVhdxFiles -or $Residual.ownedVhdxFiles.measured -ne $true) {
    throw "$Operation owned VHDX residual was not measured"
  }
  if ([int]$Residual.ownedVhdxFiles.count -ne $ExpectedVhdxFiles) {
    throw "$Operation owned VHDX count is $($Residual.ownedVhdxFiles.count), expected $ExpectedVhdxFiles"
  }
  if ($null -eq $Residual.probeProcesses -or $Residual.probeProcesses.measured -ne $false) {
    throw "$Operation binary must leave probeProcesses unmeasured for the outer harness"
  }
}

try {
  go build -o $binary ./cmd/testplay-refs-probe
  if ($LASTEXITCODE -ne 0) { throw "probe build failed with exit code $LASTEXITCODE" }

  $setup = Invoke-ProbeCommand 'setup'
  Assert-NoForbiddenPath $setup 'setup'
  if ($setup.windowsProvider -ne 'dev-drive-vhdx' -or $setup.volumeKind -ne 'Dev Drive') { throw 'setup did not use the Dev Drive VHDX provider' }
  if ($setup.devDrive.formatAttempted -ne $true -or $setup.devDrive.formatSucceeded -ne $true -or [int]$setup.devDrive.queryExitCode -ne 0 -or $setup.devDrive.temporaryDriveLetterAssigned -ne $true -or $setup.devDrive.temporaryDriveLetterRemoved -ne $true -or $setup.devDrive.privateMountVerified -ne $true) {
    throw 'setup Dev Drive evidence is incomplete'
  }
  if ($setup.status -ne 'PASS' -or $setup.volume.filesystem -ne 'ReFS') { throw 'setup did not produce a ready ReFS pool' }
  if ($setup.blockCloneSupported -ne $true) { throw 'setup did not prove Block Clone capability' }
  Assert-BinaryZeroResidual $setup.residual 1 'setup'
  $summary.setupResidual = $setup.residual

  $probe = Invoke-ProbeCommand 'probe'
  Assert-NoForbiddenPath $probe 'probe'
  if ($probe.windowsProvider -ne 'dev-drive-vhdx' -or $probe.volumeKind -ne 'Dev Drive' -or [int]$probe.devDrive.queryExitCode -ne 0 -or $probe.devDrive.privateMountVerified -ne $true -or $probe.devDrive.formatAttempted -ne $false) {
    throw 'probe did not verify the persistent Dev Drive without reformatting'
  }
  if ($probe.status -ne 'PASS' -or $probe.volume.filesystem -ne 'ReFS' -or $probe.blockCloneSupported -ne $true) {
    throw 'native ReFS Block Clone probe did not pass'
  }
  if ([int64]$probe.metrics.clonedBytes -le 0) { throw 'regular synthetic Block Clone bytes were not measured' }
  if ([int64]$probe.metrics.sparseFileCount -lt 1 -or [int64]$probe.metrics.sparseClonedBytes -le 0 -or [int64]$probe.metrics.sparseHoleBytes -le 0) {
    throw 'sparse synthetic Block Clone evidence did not pass'
  }
  if ($probe.sourceUnchanged -ne $true) { throw 'allocate-on-write source isolation failed' }
  Assert-BinaryZeroResidual $probe.residual 1 'probe'
  $summary.probeResidual = $probe.residual

  $status = Invoke-ProbeCommand 'status'
  Assert-NoForbiddenPath $status 'status'
  if ($status.windowsProvider -ne 'dev-drive-vhdx' -or $status.volumeKind -ne 'Dev Drive' -or [int]$status.devDrive.queryExitCode -ne 0 -or $status.devDrive.privateMountVerified -ne $true -or $status.devDrive.formatAttempted -ne $false) {
    throw 'status did not verify the persistent Dev Drive without reformatting'
  }
  if ($status.status -ne 'READY' -or $status.volume.filesystem -ne 'ReFS') { throw 'post-probe status is not ready' }
  Assert-BinaryZeroResidual $status.residual 1 'status'
  $summary.statusResidual = $status.residual

  $probeProcesses = @(Get-Process -Name 'testplay-refs-probe' -ErrorAction SilentlyContinue)
  if ($probeProcesses.Count -ne 0) { throw "$($probeProcesses.Count) probe helper processes remain" }
  $summary.probeProcesses = [ordered]@{ measured = $true; count = 0 }

  if ($RemoveAfter) {
    $removed = Invoke-ProbeCommand 'remove'
    Assert-NoForbiddenPath $removed 'remove'
    if ($removed.windowsProvider -ne 'dev-drive-vhdx' -or $removed.volumeKind -ne 'Dev Drive' -or [int]$removed.devDrive.queryExitCode -ne 0 -or $removed.devDrive.formatAttempted -ne $false) {
      throw 'remove did not verify the persistent Dev Drive before deletion'
    }
    Assert-BinaryZeroResidual $removed.residual 0 'remove'
    if (Test-Path -LiteralPath $poolFile) { throw 'VHDX remains after remove' }
    if (Test-Path -LiteralPath $mountRoot) { throw 'mount directory remains after remove' }
    $summary.removeResidual = $removed.residual
    $probeProcesses = @(Get-Process -Name 'testplay-refs-probe' -ErrorAction SilentlyContinue)
    if ($probeProcesses.Count -ne 0) { throw "$($probeProcesses.Count) probe helper processes remain after remove" }
  }

  $summary.status = 'PROMISING'
  $summary.nativeWindowsStatus = 'MEASURED'
  $summary.devDrive = $setup.devDrive
  $summary.filesystem = $probe.volume.filesystem
  $summary.blockCloneSupported = $probe.blockCloneSupported
  $summary.regularSyntheticClone = 'PASS'
  $summary.sparseSyntheticClone = 'PASS'
  $summary.allocateOnWriteIsolation = 'PASS'
  $summary.physicalImageCreated = $probe.physicalImageCreated
  $summary.differencingChildCreated = $probe.differencingChildCreated
  $summary.fallbackUsed = $probe.fallbackUsed
  $summary.sourceUnchanged = $probe.sourceUnchanged
  $summary.baselineUnchanged = $probe.baselineUnchanged
  $summary.residualStatus = 'MEASURED_ZERO'
}
catch {
  $summary.residualStatus = 'FAILED'
  $summary.failure = $_.Exception.Message
  throw
}
finally {
  $summary | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath (Join-Path $artifactRoot 'summary.json') -Encoding utf8
}

$summary | ConvertTo-Json -Depth 12
