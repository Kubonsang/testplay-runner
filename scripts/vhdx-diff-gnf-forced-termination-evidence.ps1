Set-StrictMode -Version Latest

function Get-GNFForcedTerminationProperty {
    param([object]$Value, [string]$Name)
    if ($null -eq $Value) { return $null }
    if ($Value -is [Collections.IDictionary] -and $Value.Contains($Name)) { return $Value[$Name] }
    $Property = $Value.PSObject.Properties[$Name]
    if ($null -eq $Property) { return $null }
    return $Property.Value
}

function Assert-VHDXDiffGNFForcedTerminationEvidence {
    param([object]$Summary)

    if ($null -eq $Summary) { throw 'GNF forced-termination summary is missing.' }
    if ($Summary.status -ne 'PASS' -or $Summary.verdict -ne 'GNF_VHDX_DIFF_FORCED_TERMINATION_RECOVERY_PASS') {
        throw "GNF forced-termination verdict is not PASS: status=$($Summary.status) verdict=$($Summary.verdict)"
    }
    foreach ($Name in @('sourceUnchanged', 'localPackageUnchanged', 'parentUnchanged', 'recoveryVerified', 'uninstalled', 'residualZero')) {
        if ((Get-GNFForcedTerminationProperty -Value $Summary -Name $Name) -ne $true) {
            throw "GNF forced-termination evidence is not measured pass: $Name"
        }
    }
    if ($Summary.cleanupState -ne 'released') { throw "GNF forced-termination cleanup is not released: $($Summary.cleanupState)" }
    if ($null -eq $Summary.termination -or -not $Summary.termination.approved -or -not $Summary.termination.clientStopped -or -not $Summary.termination.unityStopped) {
        throw 'Exact client/Unity termination evidence is incomplete.'
    }
    if ([int]$Summary.termination.clientPid -le 0 -or [int]$Summary.termination.unityPid -le 0 -or [int]$Summary.termination.clientPid -eq [int]$Summary.termination.unityPid) {
        throw 'Exact client/Unity PID evidence is invalid.'
    }
    if ($null -eq $Summary.crashJournal -or [string]::IsNullOrWhiteSpace([string]$Summary.crashJournal.leaseId) -or [string]::IsNullOrWhiteSpace([string]$Summary.crashJournal.ownershipToken)) {
        throw 'Crash lease identity evidence is incomplete.'
    }
    if ($null -eq $Summary.parentBefore -or $null -eq $Summary.parentAfter -or $Summary.parentBefore.key -ne $Summary.parentAfter.key -or $Summary.parentBefore.sha256 -ne $Summary.parentAfter.sha256) {
        throw 'Immutable parent identity/hash evidence does not match.'
    }
    foreach ($Name in @('activeChildCount', 'retainedChildCount', 'pendingCount', 'quarantineCount')) {
        $Value = Get-GNFForcedTerminationProperty -Value $Summary.recoveredStorageStatus -Name $Name
        if ($null -eq $Value -or [long]$Value -ne 0) { throw "Recovered storage metric is not zero: $Name=$Value" }
    }
    if ((Get-GNFForcedTerminationProperty -Value $Summary.recoveredStorageStatus -Name 'manualRecoveryRequired') -ne $false) {
        throw 'Recovered storage requires manual recovery.'
    }
    foreach ($Name in @('newFileBackedDisks', 'newDriveLetters', 'newProcesses')) {
        if (@(Get-GNFForcedTerminationProperty -Value $Summary -Name $Name).Count -ne 0) { throw "Outer residual array is nonzero: $Name" }
    }
}
