Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'vhdx-diff-fixture-parallel-evidence.ps1')

function New-Worker {
    param([int]$Index, [string]$Start, [string]$Finish)
    return [pscustomobject]@{
        role = "worker-$Index"
        exit_code = 0
        total = 1
        passed = 1
        failed = 0
        skipped = 0
        tests = @([pscustomobject]@{ name = 'Fixture.Test'; result = 'Passed' })
        errors = @()
        workspace_metrics = [pscustomobject]@{
            workspaceBackend = 'vhdx-diff'
            provider = 'vhdx-differencing'
            fallbackUsed = $false
            cleanupState = 'released'
            parentKey = 'shared'
            parentPath = 'C:\store\parent.vhdx'
            parentCreated = $Index -eq 1
            parentReused = $Index -ne 1
            childReadyAllocatedMeasured = $true
            childPeakAllocatedMeasured = $true
            childReleasedAllocatedMeasured = $true
            childReadyAllocatedBytes = 1
            childPeakAllocatedBytes = 2
            childReleasedAllocatedBytes = 0
            unityProcessPid = 1000 + $Index
            unityProcessStartedAt = $Start
            unityProcessFinishedAt = $Finish
            mountPath = "C:\workspace-$Index\Library"
            physicalDiskPath = "\\.\PhysicalDrive$Index"
            volumeGuid = "\\?\Volume{$Index}\\"
        }
    }
}

$Pass = [pscustomobject]@{
    exit_code = 0
    instances = @(
        (New-Worker 1 '2026-08-11T00:00:00Z' '2026-08-11T00:00:10Z'),
        (New-Worker 2 '2026-08-11T00:00:02Z' '2026-08-11T00:00:12Z')
    )
}
$Evidence = Assert-VHDXDiffFixtureParallelResult -Result $Pass -WorkerCount 2 -ExpectedTest 'Fixture.Test'
if (-not $Evidence.allUnityProcessesOverlapped -or $Evidence.commonIntervalMs -ne 8000) { throw 'Overlap evidence is incorrect.' }

$NoOverlap = [pscustomobject]@{
    exit_code = 0
    instances = @(
        (New-Worker 1 '2026-08-11T00:00:00Z' '2026-08-11T00:00:01Z'),
        (New-Worker 2 '2026-08-11T00:00:02Z' '2026-08-11T00:00:03Z')
    )
}
try {
    Assert-VHDXDiffFixtureParallelResult -Result $NoOverlap -WorkerCount 2 -ExpectedTest 'Fixture.Test' | Out-Null
    throw 'Non-overlapping workers were accepted.'
}
catch {
    if ($_.Exception.Message -eq 'Non-overlapping workers were accepted.') { throw }
}

Write-Output 'VHDX_DIFF_FIXTURE_PARALLEL_EVIDENCE_TEST=PASS'

