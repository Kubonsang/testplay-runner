Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'vhdx-diff-gnf-evidence.ps1')

function New-PassingStatus {
    return [pscustomobject]@{
        provider = 'vhdx-differencing'
        parentCount = 1
        activeChildCount = 0
        retainedChildCount = 0
        pendingCount = 0
        quarantineCount = 0
        manualRecoveryRequired = $false
        capacity = [pscustomobject]@{
            allocatedBytes = 4831838208
            quotaBytes = 34359738368
            hostFreeBytes = 90831413248
            hostFloorBytes = 21474836480
        }
    }
}

function Assert-Throws {
    param([scriptblock]$Action, [string]$Name)
    try {
        & $Action
        throw "Expected failure was not observed: $Name"
    }
    catch {
        if ($_.Exception.Message -eq "Expected failure was not observed: $Name") { throw }
    }
}

Assert-VHDXDiffStorageStatus -Status (New-PassingStatus)

$Missing = New-PassingStatus
$Missing.PSObject.Properties.Remove('parentCount')
Assert-Throws -Name 'missing parentCount' -Action { Assert-VHDXDiffStorageStatus -Status $Missing }

$Active = New-PassingStatus
$Active.activeChildCount = 1
Assert-Throws -Name 'active child residual' -Action { Assert-VHDXDiffStorageStatus -Status $Active }

$Recovery = New-PassingStatus
$Recovery.manualRecoveryRequired = $true
Assert-Throws -Name 'manual recovery' -Action { Assert-VHDXDiffStorageStatus -Status $Recovery }

$LowFree = New-PassingStatus
$LowFree.capacity.hostFreeBytes = $LowFree.capacity.hostFloorBytes
Assert-Throws -Name 'host free floor' -Action { Assert-VHDXDiffStorageStatus -Status $LowFree }

Write-Output 'vhdx-diff GNF evidence contract tests: PASS'
