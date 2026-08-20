Set-StrictMode -Version Latest

function Get-VHDXDiffOptionalMetric {
    param([object]$Metrics, [string]$Name)
    $Property = $Metrics.PSObject.Properties[$Name]
    if ($null -eq $Property) { return $null }
    return $Property.Value
}

function Assert-VHDXDiffStorageStatus {
    param([object]$Status)
    if ($Status.provider -ne 'vhdx-differencing') { throw "Unexpected storage provider: $($Status.provider)" }
    $Expected = [ordered]@{
        parentCount = 1
        activeChildCount = 0
        retainedChildCount = 0
        pendingCount = 0
        quarantineCount = 0
    }
    foreach ($Entry in $Expected.GetEnumerator()) {
        $Actual = Get-VHDXDiffOptionalMetric -Metrics $Status -Name $Entry.Key
        if ($null -eq $Actual) { throw "Storage status metric is not measured: $($Entry.Key)" }
        if ([long]$Actual -ne [long]$Entry.Value) { throw "Unexpected storage status metric: $($Entry.Key)=$Actual expected=$($Entry.Value)" }
    }
    $ManualRecovery = Get-VHDXDiffOptionalMetric -Metrics $Status -Name 'manualRecoveryRequired'
    if ($null -eq $ManualRecovery -or $ManualRecovery -ne $false) { throw "Storage status requires manual recovery: $ManualRecovery" }
    if ($null -eq $Status.capacity -or $Status.capacity.allocatedBytes -le 0 -or $Status.capacity.quotaBytes -le 0 -or $Status.capacity.hostFreeBytes -le $Status.capacity.hostFloorBytes) {
        throw 'Storage capacity evidence is missing or outside the configured safety gate.'
    }
}
