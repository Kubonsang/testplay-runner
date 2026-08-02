[CmdletBinding()]
param(
    [Parameter()]
    [ValidateSet(1, 5)]
    [int]$Count = 1
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ExpectedRepository = 'C:\Dev\testplay-runner'
$ProbeRoot = 'C:\Dev\testplay-vhdx-probe'

function Get-FileBackedVirtualDiskSnapshot {
    @(
        Get-Disk |
            Where-Object { $_.BusType.ToString() -eq 'File Backed Virtual' } |
            Sort-Object Number |
            Select-Object Number,
                FriendlyName,
                SerialNumber,
                @{
                    Name = 'OperationalStatus'
                    Expression = { @($_.OperationalStatus) -join ',' }
                },
                @{
                    Name = 'PartitionStyle'
                    Expression = { $_.PartitionStyle.ToString() }
                },
                IsOffline,
                IsReadOnly
    )
}

function Get-ProbeMountSnapshot {
    $mounts = @()
    foreach ($partition in @(Get-Partition -ErrorAction SilentlyContinue)) {
        foreach ($accessPath in @($partition.AccessPaths)) {
            if ([string]::IsNullOrWhiteSpace($accessPath)) {
                continue
            }
            $normalized = $accessPath.TrimEnd('\')
            if ($normalized.StartsWith(
                $ProbeRoot,
                [StringComparison]::OrdinalIgnoreCase
            )) {
                $mounts += [pscustomobject]@{
                    DiskNumber = $partition.DiskNumber
                    PartitionNumber = $partition.PartitionNumber
                    AccessPath = $accessPath
                }
            }
        }
    }
    @($mounts)
}

function Get-ProbeArtifactSnapshot {
    if (-not (Test-Path -LiteralPath $ProbeRoot -PathType Container)) {
        return @()
    }
    @(
        Get-ChildItem -LiteralPath $ProbeRoot -Force |
            Select-Object FullName,
                Name,
                @{
                    Name = 'ItemType'
                    Expression = {
                        if ($_.PSIsContainer) { 'Directory' } else { 'File' }
                    }
                },
                Length
    )
}

function Get-ResidualVHDXPath {
    if (-not (Test-Path -LiteralPath $ProbeRoot -PathType Container)) {
        return @()
    }
    $paths = @()
    foreach ($operation in @(
        Get-ChildItem -LiteralPath $ProbeRoot `
            -Directory `
            -Filter 'testplay-vhdx-probe-*' `
            -Force
    )) {
        foreach ($name in @('parent.vhdx', 'child-a.vhdx', 'child-b.vhdx')) {
            $candidate = Join-Path $operation.FullName $name
            if (Test-Path -LiteralPath $candidate -PathType Leaf) {
                $paths += $candidate
            }
        }
    }
    @($paths)
}

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
$isAdmin = $principal.IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator
)

if (-not $isAdmin) {
    throw 'Administrator privileges are required. Open PowerShell as Administrator and run the script again.'
}

$repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
if (-not $repository.Equals(
    $ExpectedRepository,
    [StringComparison]::OrdinalIgnoreCase
)) {
    throw "Unsafe repository path: $repository. Expected exactly $ExpectedRepository."
}

if (Test-Path -LiteralPath $ProbeRoot) {
    $rootItem = Get-Item -LiteralPath $ProbeRoot -Force
    if (-not $rootItem.PSIsContainer) {
        throw "Probe Root is not a directory: $ProbeRoot"
    }
    if (($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Probe Root must not be a reparse point: $ProbeRoot"
    }
    $existing = @(Get-ChildItem -LiteralPath $ProbeRoot -Force)
    if ($existing.Count -ne 0) {
        throw "Probe Root must be empty; found $($existing.Count) item(s). Nothing was deleted."
    }
} else {
    New-Item -ItemType Directory -Path $ProbeRoot | Out-Null
}

Get-Command go -ErrorAction Stop | Out-Null

$beforeVirtualDisks = @(Get-FileBackedVirtualDiskSnapshot)
$beforeMounts = @(Get-ProbeMountSnapshot)
$oldProbeRoot = [Environment]::GetEnvironmentVariable(
    'TESTPLAY_VHDX_PROBE_ROOT',
    'Process'
)
$hadProbeRoot = Test-Path Env:TESTPLAY_VHDX_PROBE_ROOT
$finalExitCode = 1

try {
    $env:TESTPLAY_VHDX_PROBE_ROOT = $ProbeRoot

    [ordered]@{
        phase = 'preflight'
        repository = $repository
        probeRoot = $ProbeRoot
        count = $Count
        administrator = $isAdmin
        beforeVirtualDisks = $beforeVirtualDisks
        beforeProbeMounts = $beforeMounts
    } | ConvertTo-Json -Depth 8

    Push-Location $repository
    try {
        $goArguments = @(
            'test'
            '-tags=vhdx_integration'
            './internal/vhdxprobe'
            '-run'
            '^TestDifferencingVHDXProbe$'
            '-v'
            "-count=$Count"
        )
        & go @goArguments
        $probeExitCode = $LASTEXITCODE
    } finally {
        Pop-Location
    }

    $afterVirtualDisks = @(Get-FileBackedVirtualDiskSnapshot)
    $afterMounts = @(Get-ProbeMountSnapshot)
    $residualArtifacts = @(Get-ProbeArtifactSnapshot)
    $residualVHDX = @(Get-ResidualVHDXPath)
    $beforeDiskJSON = ConvertTo-Json `
        -InputObject @($beforeVirtualDisks) `
        -Depth 6 `
        -Compress
    $afterDiskJSON = ConvertTo-Json `
        -InputObject @($afterVirtualDisks) `
        -Depth 6 `
        -Compress
    $diskDifference = @()
    if ($beforeDiskJSON -ne $afterDiskJSON) {
        $diskDifference = @(
            [pscustomobject]@{
                Before = $beforeVirtualDisks
                After = $afterVirtualDisks
            }
        )
    }

    $success = (
        $probeExitCode -eq 0 -and
        $diskDifference.Count -eq 0 -and
        $afterMounts.Count -eq 0 -and
        $residualArtifacts.Count -eq 0
    )

    [ordered]@{
        phase = 'final'
        success = $success
        probeExitCode = $probeExitCode
        repository = $repository
        probeRoot = $ProbeRoot
        count = $Count
        beforeVirtualDisks = $beforeVirtualDisks
        afterVirtualDisks = $afterVirtualDisks
        virtualDiskDifference = $diskDifference
        residualProbeMounts = $afterMounts
        residualArtifacts = $residualArtifacts
        residualVHDXFiles = $residualVHDX
        cleanupPassed = (
            $diskDifference.Count -eq 0 -and
            $afterMounts.Count -eq 0 -and
            $residualArtifacts.Count -eq 0
        )
    } | ConvertTo-Json -Depth 8

    if ($success) {
        $finalExitCode = 0
    } elseif ($probeExitCode -ne 0) {
        $finalExitCode = $probeExitCode
    } else {
        $finalExitCode = 1
    }
} finally {
    if ($hadProbeRoot) {
        [Environment]::SetEnvironmentVariable(
            'TESTPLAY_VHDX_PROBE_ROOT',
            $oldProbeRoot,
            'Process'
        )
    } else {
        Remove-Item Env:TESTPLAY_VHDX_PROBE_ROOT -ErrorAction SilentlyContinue
    }
}

exit $finalExitCode
