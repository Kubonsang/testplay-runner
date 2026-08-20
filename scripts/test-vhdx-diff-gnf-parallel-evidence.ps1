Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'vhdx-diff-gnf-evidence.ps1')
. (Join-Path $PSScriptRoot 'vhdx-diff-gnf-parallel-evidence.ps1')

function New-GNFWorker {
    param([int]$Index, [bool]$Created, [string]$Start, [string]$Finish)
    return [pscustomobject]@{
        role = "worker-$Index"; exit_code = 0; total = 1; passed = 1; failed = 0; skipped = 0
        tests = @([pscustomobject]@{ name = 'GNF.Test'; result = 'Passed' }); errors = @()
        workspace_metrics = [pscustomobject]@{
            workspaceBackend = 'vhdx-diff'; provider = 'vhdx-differencing'; fallbackUsed = $false; cleanupState = 'released'
            parentKey = 'shared'; parentPath = 'C:\store\parent.vhdx'; parentCreated = $Created; parentReused = $true
            localPackageOverrideCount = 1; localPackagesDigest = 'package-digest'
            childReadyAllocatedMeasured = $true; childPeakAllocatedMeasured = $true; childReleasedAllocatedMeasured = $true
            childReadyAllocatedBytes = 10; childPeakAllocatedBytes = 10
            unityProcessPid = 2000 + $Index; unityProcessStartedAt = $Start; unityProcessFinishedAt = $Finish
            mountPath = "C:\workspace-$Index\Library"; physicalDiskPath = "\\.\PhysicalDrive$Index"; volumeGuid = "\\?\Volume{$Index}\\"
        }
    }
}

$Result = [pscustomobject]@{ exit_code = 0; instances = @(
    (New-GNFWorker 1 $true '2026-08-11T00:00:00Z' '2026-08-11T00:00:10Z'),
    (New-GNFWorker 2 $false '2026-08-11T00:00:02Z' '2026-08-11T00:00:12Z')
) }
$Evidence = Assert-VHDXDiffGNFParallelScenario -Result $Result -WorkerCount 2 -ExpectedTest 'GNF.Test' -ExpectedParentCreatedCount 1
if ($Evidence.commonIntervalMs -ne 8000 -or $Evidence.parentCreatedCount -ne 1) { throw 'GNF parallel evidence is incorrect.' }

$TwoNotMeasured = @(Get-VHDXDiffGNFParallelNotMeasured -WorkerCount 2)
if ($TwoNotMeasured -notcontains 'GNF four workers' -or $TwoNotMeasured -notcontains 'GNF eight workers') {
    throw 'Two-worker NOT MEASURED scale evidence is incorrect.'
}
$FourNotMeasured = @(Get-VHDXDiffGNFParallelNotMeasured -WorkerCount 4)
if ($FourNotMeasured -contains 'GNF four workers' -or $FourNotMeasured -notcontains 'GNF eight workers') {
    throw 'Four-worker NOT MEASURED scale evidence is incorrect.'
}

$NoOverlap = [pscustomobject]@{ exit_code = 0; instances = @(
    (New-GNFWorker 1 $true '2026-08-11T00:00:00Z' '2026-08-11T00:00:01Z'),
    (New-GNFWorker 2 $false '2026-08-11T00:00:02Z' '2026-08-11T00:00:03Z')
) }
try {
    Assert-VHDXDiffGNFParallelScenario -Result $NoOverlap -WorkerCount 2 -ExpectedTest 'GNF.Test' -ExpectedParentCreatedCount 1 | Out-Null
    throw 'Non-overlapping GNF workers were accepted.'
}
catch { if ($_.Exception.Message -eq 'Non-overlapping GNF workers were accepted.') { throw } }

Write-Output 'VHDX_DIFF_GNF_PARALLEL_EVIDENCE_TEST=PASS'
