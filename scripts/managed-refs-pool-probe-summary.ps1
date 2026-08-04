function New-ManagedRefsProbeSummary {
  [ordered]@{
    status = 'FAILED'
    setupStatus = 'NOT EXECUTED'
    probeStatus = 'NOT EXECUTED'
    statusStatus = 'NOT EXECUTED'
    removeStatus = 'NOT EXECUTED'
    nativeWindowsStatus = 'NOT MEASURED'
    windowsProvider = 'dev-drive-vhdx'
    volumeKind = 'Dev Drive'
    devDrive = $null
    filesystem = $null
    clusterSize = $null
    blockCloneSupported = $null
    regularBlockCloneIOCTLAttempted = $null
    sparseBlockCloneIOCTLAttempted = $null
    regularSyntheticClone = 'NOT MEASURED'
    sparseSyntheticClone = 'NOT MEASURED'
    allocateOnWriteIsolation = 'NOT MEASURED'
    physicalImageCreated = $false
    differencingChildCreated = $false
    fallbackUsed = $false
    sourceUnchanged = 'NOT MEASURED'
    baselineUnchanged = 'NOT MEASURED'
    setupMetrics = $null
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
}

function Update-ManagedRefsSummaryAfterSetup($Summary, $Setup) {
  $Summary.setupStatus = 'MEASURED_PASS'
  $Summary.nativeWindowsStatus = 'PARTIALLY_MEASURED'
  $Summary.devDrive = $Setup.devDrive
  $Summary.filesystem = $Setup.volume.filesystem
  $Summary.clusterSize = $Setup.volume.clusterSize
  $Summary.blockCloneSupported = $Setup.blockCloneSupported
  $Summary.regularBlockCloneIOCTLAttempted = $Setup.metrics.regularBlockCloneIOCTLAttempted
  $Summary.sparseBlockCloneIOCTLAttempted = $Setup.metrics.sparseBlockCloneIOCTLAttempted
  $Summary.regularSyntheticClone = if ([int64]$Setup.metrics.clonedBytes -gt 0) { 'PASS' } else { 'MEASURED_FAIL' }
  $Summary.sparseSyntheticClone = if ([int64]$Setup.metrics.sparseClonedBytes -gt 0 -and [int64]$Setup.metrics.sparseHoleBytes -gt 0) { 'PASS' } else { 'MEASURED_FAIL' }
  $Summary.allocateOnWriteIsolation = if ($Setup.sourceUnchanged -eq $true) { 'PASS' } else { 'MEASURED_FAIL' }
  $Summary.sourceUnchanged = $Setup.sourceUnchanged
  $Summary.baselineUnchanged = $Setup.baselineUnchanged
  $Summary.physicalImageCreated = $Setup.physicalImageCreated
  $Summary.differencingChildCreated = $Setup.differencingChildCreated
  $Summary.fallbackUsed = $Setup.fallbackUsed
  $Summary.setupMetrics = $Setup.metrics
  $Summary.setupResidual = $Setup.residual
  $Summary.residualStatus = 'PARTIALLY_MEASURED'
}

function Update-ManagedRefsSummaryAfterProbe($Summary, $Probe) {
  $Summary.probeStatus = 'MEASURED_PASS'
  $Summary.filesystem = $Probe.volume.filesystem
  $Summary.clusterSize = $Probe.volume.clusterSize
  $Summary.blockCloneSupported = $Probe.blockCloneSupported
  $Summary.regularBlockCloneIOCTLAttempted = $Probe.metrics.regularBlockCloneIOCTLAttempted
  $Summary.sparseBlockCloneIOCTLAttempted = $Probe.metrics.sparseBlockCloneIOCTLAttempted
  $Summary.regularSyntheticClone = 'PASS'
  $Summary.sparseSyntheticClone = 'PASS'
  $Summary.allocateOnWriteIsolation = 'PASS'
  $Summary.sourceUnchanged = $Probe.sourceUnchanged
  $Summary.baselineUnchanged = $Probe.baselineUnchanged
  $Summary.probeResidual = $Probe.residual
}

function Update-ManagedRefsSummaryAfterStatus($Summary, $StatusResult) {
  $Summary.statusStatus = 'MEASURED_PASS'
  $Summary.statusResidual = $StatusResult.residual
}

function Update-ManagedRefsSummaryAfterRemove($Summary, $Remove) {
  $Summary.removeStatus = 'MEASURED_PASS'
  $Summary.removeResidual = $Remove.residual
}

function Set-ManagedRefsSummaryFailure($Summary, [string]$Stage, [string]$Message) {
  switch ($Stage) {
    'setup' { $Summary.setupStatus = 'MEASURED_FAIL' }
    'probe' { $Summary.probeStatus = 'MEASURED_FAIL' }
    'status' { $Summary.statusStatus = 'MEASURED_FAIL' }
    'remove' { $Summary.removeStatus = 'MEASURED_FAIL' }
  }
  $Summary.status = 'FAILED'
  $Summary.failure = $Message
}
