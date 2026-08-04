$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'managed-refs-pool-probe-summary.ps1')

$zeroResidual = [pscustomobject]@{
  ownedVhdxFiles = [pscustomobject]@{ measured = $true; count = 1 }
}
$setup = [pscustomobject]@{
  devDrive = [pscustomobject]@{ queryExitCode = 0; queryOutput = '개발자 드라이브'; formatAttempted = $true; formatSucceeded = $true }
  volume = [pscustomobject]@{ filesystem = 'ReFS'; clusterSize = 4096 }
  blockCloneSupported = $true
  metrics = [pscustomobject]@{
    clonedBytes = 69632
    sparseClonedBytes = 61440
    sparseHoleBytes = 4096
    regularBlockCloneIOCTLAttempted = $true
    sparseBlockCloneIOCTLAttempted = $true
  }
  sourceUnchanged = $true
  baselineUnchanged = $true
  physicalImageCreated = $false
  differencingChildCreated = $false
  fallbackUsed = $false
  residual = $zeroResidual
}

$summary = New-ManagedRefsProbeSummary
Update-ManagedRefsSummaryAfterSetup $summary $setup
Set-ManagedRefsSummaryFailure $summary 'probe' 'injected probe failure'
$roundTrip = $summary | ConvertTo-Json -Depth 12 | ConvertFrom-Json

if ($roundTrip.status -ne 'FAILED' -or $roundTrip.setupStatus -ne 'MEASURED_PASS' -or $roundTrip.probeStatus -ne 'MEASURED_FAIL') {
  throw 'stage status was not retained'
}
if ($roundTrip.filesystem -ne 'ReFS' -or [int64]$roundTrip.clusterSize -ne 4096 -or $roundTrip.blockCloneSupported -ne $true) {
  throw 'setup volume evidence was not retained'
}
if ($roundTrip.regularBlockCloneIOCTLAttempted -ne $true -or $roundTrip.sparseBlockCloneIOCTLAttempted -ne $true) {
  throw 'setup IOCTL attempt evidence was not retained'
}
if ([int64]$roundTrip.setupMetrics.clonedBytes -ne 69632 -or [int64]$roundTrip.setupMetrics.sparseHoleBytes -ne 4096) {
  throw 'setup clone metrics were not retained'
}
if ($roundTrip.devDrive.queryOutput -ne '개발자 드라이브' -or $roundTrip.failure -ne 'injected probe failure') {
  throw 'raw evidence or failure was not retained'
}
