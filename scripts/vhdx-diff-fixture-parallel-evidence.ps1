Set-StrictMode -Version Latest

function Get-VHDXDiffMetric {
    param([object]$Metrics, [string]$Name)
    $Property = $Metrics.PSObject.Properties[$Name]
    if ($null -eq $Property) { return $null }
    return $Property.Value
}

function Assert-VHDXDiffFixtureParallelResult {
    param(
        [object]$Result,
        [int]$WorkerCount,
        [string]$ExpectedTest
    )

    if ($Result.exit_code -ne 0) { throw "Scenario failed: exit=$($Result.exit_code)" }
    $Instances = @($Result.instances)
    if ($Instances.Count -ne $WorkerCount) { throw "Unexpected worker count: $($Instances.Count)" }

    $ParentKeys = @()
    $ParentPaths = @()
    $MountPaths = @()
    $PhysicalDisks = @()
    $VolumeGUIDs = @()
    $Starts = @()
    $Finishes = @()
    [int]$Created = 0
    [int]$Reused = 0

    foreach ($Instance in $Instances) {
        if ($Instance.exit_code -ne 0 -or $Instance.total -ne 1 -or $Instance.passed -ne 1 -or $Instance.failed -ne 0 -or $Instance.skipped -ne 0) {
            throw "Worker test result is not PASS: role=$($Instance.role)"
        }
        $Tests = @($Instance.tests)
        if ($Tests.Count -ne 1 -or $Tests[0].name -ne $ExpectedTest -or $Tests[0].result -ne 'Passed') {
            throw "Worker test selection differs: role=$($Instance.role)"
        }
        if (@($Instance.errors).Count -ne 0) { throw "Worker compile errors were reported: role=$($Instance.role)" }

        $Metrics = $Instance.workspace_metrics
        if ($null -eq $Metrics -or $Metrics.workspaceBackend -ne 'vhdx-diff' -or $Metrics.provider -ne 'vhdx-differencing') {
            throw "Worker vhdx-diff metrics are missing: role=$($Instance.role)"
        }
        if ($Metrics.fallbackUsed -or $Metrics.cleanupState -ne 'released') {
            throw "Worker used fallback or was not released: role=$($Instance.role)"
        }
        foreach ($MeasuredName in @('childReadyAllocatedMeasured', 'childPeakAllocatedMeasured', 'childReleasedAllocatedMeasured')) {
            if ((Get-VHDXDiffMetric -Metrics $Metrics -Name $MeasuredName) -ne $true) {
                throw "Worker allocation metric is not measured: role=$($Instance.role) metric=$MeasuredName"
            }
        }
        if ([long]$Metrics.childPeakAllocatedBytes -le [long]$Metrics.childReadyAllocatedBytes) {
            throw "Worker child did not grow: role=$($Instance.role)"
        }
        if ([long]$Metrics.childReleasedAllocatedBytes -ne 0) {
            throw "Worker child allocation remains after release: role=$($Instance.role)"
        }
        if ([int]$Metrics.unityProcessPid -le 0) { throw "Worker Unity PID is missing: role=$($Instance.role)" }
        $Start = [DateTimeOffset]::Parse($Metrics.unityProcessStartedAt)
        $Finish = [DateTimeOffset]::Parse($Metrics.unityProcessFinishedAt)
        if ($Start -ge $Finish) { throw "Worker Unity interval is invalid: role=$($Instance.role)" }

        $ParentKeys += [string]$Metrics.parentKey
        $ParentPaths += [string]$Metrics.parentPath
        $MountPaths += [string]$Metrics.mountPath
        $PhysicalDisks += [string]$Metrics.physicalDiskPath
        $VolumeGUIDs += [string]$Metrics.volumeGuid
        $Starts += $Start
        $Finishes += $Finish
        if ($Metrics.parentCreated) { $Created++ }
        if ($Metrics.parentReused) { $Reused++ }
    }

    if (@($ParentKeys | Select-Object -Unique).Count -ne 1 -or [string]::IsNullOrWhiteSpace($ParentKeys[0])) { throw 'Workers did not share one parent key.' }
    if (@($ParentPaths | Select-Object -Unique).Count -ne 1 -or [string]::IsNullOrWhiteSpace($ParentPaths[0])) { throw 'Workers did not share one parent path.' }
    if (@($MountPaths | Select-Object -Unique).Count -ne $WorkerCount) { throw 'Worker mount paths are not distinct.' }
    if (@($PhysicalDisks | Select-Object -Unique).Count -ne $WorkerCount) { throw 'Worker physical disks are not distinct.' }
    if (@($VolumeGUIDs | Select-Object -Unique).Count -ne $WorkerCount) { throw 'Worker volume GUIDs are not distinct.' }
    if ($Created -ne 1 -or $Reused -ne ($WorkerCount - 1)) { throw "Unexpected parent creation/reuse counts: created=$Created reused=$Reused" }

    $LatestStart = @($Starts | Sort-Object -Descending)[0]
    $EarliestFinish = @($Finishes | Sort-Object)[0]
    if ($LatestStart -ge $EarliestFinish) { throw 'Unity worker process intervals did not all overlap.' }

    return [ordered]@{
        status = 'PASS'
        workerCount = $WorkerCount
        parentKey = $ParentKeys[0]
        parentPath = $ParentPaths[0]
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

