Set-StrictMode -Version Latest

function Assert-VHDXDiffGNFParallelScenario {
    param(
        [object]$Result,
        [int]$WorkerCount,
        [string]$ExpectedTest,
        [int]$ExpectedParentCreatedCount
    )

    if ($Result.exit_code -ne 0) { throw "Scenario failed: exit=$($Result.exit_code)" }
    $Instances = @($Result.instances)
    if ($Instances.Count -ne $WorkerCount) { throw "Unexpected worker count: $($Instances.Count)" }
    $ParentKeys = @()
    $ParentPaths = @()
    $MountPaths = @()
    $PhysicalDisks = @()
    $VolumeGUIDs = @()
    $LocalPackageDigests = @()
    $Starts = @()
    $Finishes = @()
    [int]$Created = 0
    [int]$Reused = 0

    foreach ($Instance in $Instances) {
        if ($Instance.exit_code -ne 0 -or $Instance.total -ne 1 -or $Instance.passed -ne 1 -or $Instance.failed -ne 0 -or $Instance.skipped -ne 0) {
            throw "GNF worker test result is not PASS: role=$($Instance.role)"
        }
        $Tests = @($Instance.tests)
        if ($Tests.Count -ne 1 -or $Tests[0].name -ne $ExpectedTest -or $Tests[0].result -ne 'Passed') {
            throw "GNF worker test selection differs: role=$($Instance.role)"
        }
        if (@($Instance.errors).Count -ne 0) { throw "GNF worker compile errors were reported: role=$($Instance.role)" }

        $Metrics = $Instance.workspace_metrics
        if ($null -eq $Metrics -or $Metrics.workspaceBackend -ne 'vhdx-diff' -or $Metrics.provider -ne 'vhdx-differencing') {
            throw "GNF worker vhdx-diff metrics are missing: role=$($Instance.role)"
        }
        if ($Metrics.fallbackUsed -or $Metrics.cleanupState -ne 'released') { throw "GNF worker used fallback or was not released: role=$($Instance.role)" }
        if ($Metrics.localPackageOverrideCount -ne 1 -or [string]::IsNullOrWhiteSpace($Metrics.localPackagesDigest)) { throw "GNF local package override evidence is missing: role=$($Instance.role)" }
        foreach ($MeasuredName in @('childReadyAllocatedMeasured', 'childPeakAllocatedMeasured', 'childReleasedAllocatedMeasured')) {
            if ((Get-VHDXDiffOptionalMetric -Metrics $Metrics -Name $MeasuredName) -ne $true) { throw "GNF child allocation metric is not measured: role=$($Instance.role) metric=$MeasuredName" }
        }
        if ([long]$Metrics.childPeakAllocatedBytes -lt [long]$Metrics.childReadyAllocatedBytes) { throw "GNF child peak is smaller than ready allocation: role=$($Instance.role)" }
        $ReleasedBytes = Get-VHDXDiffOptionalMetric -Metrics $Metrics -Name 'childReleasedAllocatedBytes'
        if ($null -eq $ReleasedBytes) { $ReleasedBytes = 0 }
        if ([long]$ReleasedBytes -ne 0) { throw "GNF child allocation remains after release: role=$($Instance.role)" }
        if ([int]$Metrics.unityProcessPid -le 0) { throw "GNF worker Unity PID is missing: role=$($Instance.role)" }
        $Start = [DateTimeOffset]::Parse($Metrics.unityProcessStartedAt)
        $Finish = [DateTimeOffset]::Parse($Metrics.unityProcessFinishedAt)
        if ($Start -ge $Finish) { throw "GNF worker Unity interval is invalid: role=$($Instance.role)" }

        $ParentKeys += [string]$Metrics.parentKey
        $ParentPaths += [string]$Metrics.parentPath
        $MountPaths += [string]$Metrics.mountPath
        $PhysicalDisks += [string]$Metrics.physicalDiskPath
        $VolumeGUIDs += [string]$Metrics.volumeGuid
        $LocalPackageDigests += [string]$Metrics.localPackagesDigest
        $Starts += $Start
        $Finishes += $Finish
        if ($Metrics.parentCreated) { $Created++ }
        if ($Metrics.parentReused) { $Reused++ }
    }

    if (@($ParentKeys | Select-Object -Unique).Count -ne 1 -or [string]::IsNullOrWhiteSpace($ParentKeys[0])) { throw 'GNF workers did not share one parent key.' }
    if (@($ParentPaths | Select-Object -Unique).Count -ne 1 -or [string]::IsNullOrWhiteSpace($ParentPaths[0])) { throw 'GNF workers did not share one parent path.' }
    if (@($MountPaths | Select-Object -Unique).Count -ne $WorkerCount) { throw 'GNF worker mount paths are not distinct.' }
    if (@($PhysicalDisks | Select-Object -Unique).Count -ne $WorkerCount) { throw 'GNF worker physical disks are not distinct.' }
    if (@($VolumeGUIDs | Select-Object -Unique).Count -ne $WorkerCount) { throw 'GNF worker volume GUIDs are not distinct.' }
    if (@($LocalPackageDigests | Select-Object -Unique).Count -ne 1) { throw 'GNF local package digest differs between workers.' }
    if ($Created -ne $ExpectedParentCreatedCount -or $Reused -lt ($WorkerCount - $ExpectedParentCreatedCount) -or $Reused -gt $WorkerCount) { throw "Unexpected GNF parent creation/reuse counts: created=$Created reused=$Reused" }

    $LatestStart = @($Starts | Sort-Object -Descending)[0]
    $EarliestFinish = @($Finishes | Sort-Object)[0]
    if ($LatestStart -ge $EarliestFinish) { throw 'GNF Unity worker process intervals did not all overlap.' }

    return [ordered]@{
        status = 'PASS'
        workerCount = $WorkerCount
        parentKey = $ParentKeys[0]
        parentPath = $ParentPaths[0]
        localPackagesDigest = $LocalPackageDigests[0]
        parentCreatedCount = $Created
        parentReusedCount = $Reused
        allUnityProcessesOverlapped = $true
        commonIntervalStartedAt = $LatestStart.ToUniversalTime().ToString('o')
        commonIntervalFinishedAt = $EarliestFinish.ToUniversalTime().ToString('o')
        commonIntervalMs = [long]($EarliestFinish - $LatestStart).TotalMilliseconds
        mountPaths = @($MountPaths)
        physicalDisks = @($PhysicalDisks)
        volumeGUIDs = @($VolumeGUIDs)
    }
}

