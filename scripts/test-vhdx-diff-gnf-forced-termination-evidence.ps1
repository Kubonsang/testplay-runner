Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'vhdx-diff-gnf-forced-termination-evidence.ps1')

function New-PassSummary {
    return [pscustomobject]@{
        status = 'PASS'
        verdict = 'GNF_VHDX_DIFF_FORCED_TERMINATION_RECOVERY_PASS'
        sourceUnchanged = $true
        localPackageUnchanged = $true
        parentUnchanged = $true
        recoveryVerified = $true
        uninstalled = $true
        residualZero = $true
        cleanupState = 'released'
        termination = [pscustomobject]@{ approved = $true; clientPid = 101; unityPid = 202; clientStopped = $true; unityStopped = $true }
        crashJournal = [pscustomobject]@{ leaseId = 'lease-1'; ownershipToken = 'owner-1' }
        parentBefore = [pscustomobject]@{ key = 'parent-1'; sha256 = 'abc' }
        parentAfter = [pscustomobject]@{ key = 'parent-1'; sha256 = 'abc' }
        recoveredStorageStatus = [pscustomobject]@{ activeChildCount = 0; retainedChildCount = 0; pendingCount = 0; quarantineCount = 0; manualRecoveryRequired = $false }
        newFileBackedDisks = @()
        newDriveLetters = @()
        newProcesses = @()
    }
}

Assert-VHDXDiffGNFForcedTerminationEvidence -Summary (New-PassSummary)
$OrderedSummary = [ordered]@{}
foreach ($Property in (New-PassSummary).PSObject.Properties) { $OrderedSummary[$Property.Name] = $Property.Value }
Assert-VHDXDiffGNFForcedTerminationEvidence -Summary $OrderedSummary

foreach ($Mutation in @(
    { param($Summary) $Summary.sourceUnchanged = $false },
    { param($Summary) $Summary.parentAfter.sha256 = 'changed' },
    { param($Summary) $Summary.recoveredStorageStatus.activeChildCount = 1 },
    { param($Summary) $Summary.termination.unityStopped = $false },
    { param($Summary) $Summary.newFileBackedDisks = @([pscustomobject]@{ Number = 7 }) }
)) {
    $Rejected = $false
    try {
        $Summary = New-PassSummary
        & $Mutation $Summary
        Assert-VHDXDiffGNFForcedTerminationEvidence -Summary $Summary
    }
    catch { $Rejected = $true }
    if (-not $Rejected) { throw 'Invalid GNF forced-termination evidence was accepted.' }
}

Write-Output 'VHDX_DIFF_GNF_FORCED_TERMINATION_EVIDENCE_TEST=PASS'
