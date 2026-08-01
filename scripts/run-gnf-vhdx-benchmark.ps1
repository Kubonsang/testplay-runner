[CmdletBinding()]
param(
    [switch]$Smoke,
    [switch]$Full
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Test-Administrator {
    return ([Security.Principal.WindowsPrincipal]::new(
        [Security.Principal.WindowsIdentity]::GetCurrent()
    )).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Resolve-SafePath {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Value
    )
    if ([string]::IsNullOrWhiteSpace($Value)) { throw "$Name is required" }
    if (-not [IO.Path]::IsPathRooted($Value)) { throw "$Name must be absolute: $Value" }
    if ($Value.StartsWith('\\')) { throw "$Name must not be a network path: $Value" }
    $full = [IO.Path]::GetFullPath($Value).TrimEnd('\')
    if ($full -eq [IO.Path]::GetPathRoot($full).TrimEnd('\')) { throw "$Name must not be a drive root: $Value" }
    return $full
}

function Test-PathsOverlap {
    param([string]$Left, [string]$Right)
    $leftPrefix = $Left.TrimEnd('\') + '\'
    $rightPrefix = $Right.TrimEnd('\') + '\'
    return $leftPrefix.StartsWith($rightPrefix, [StringComparison]::OrdinalIgnoreCase) -or
        $rightPrefix.StartsWith($leftPrefix, [StringComparison]::OrdinalIgnoreCase)
}

function Assert-EmptyWorkRoot {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $item = Get-Item -LiteralPath $Path -Force
    if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw "work root must be an ordinary directory: $Path"
    }
    $entries = @(Get-ChildItem -LiteralPath $Path -Force)
    if ($entries.Count -ne 0) { throw "work root must be empty; found $($entries.Count) item(s): $Path" }
}

function Get-FileBackedVirtualDisks {
    return @(
        Get-Disk |
            Where-Object { $_.BusType.ToString() -eq 'File Backed Virtual' } |
            Sort-Object Number |
            Select-Object Number, FriendlyName, SerialNumber,
                          OperationalStatus, PartitionStyle,
                          IsOffline, IsReadOnly
    )
}

function Get-BenchmarkProcesses {
    return @(
        Get-Process 'Unity', 'testplay-storage-helper' -ErrorAction SilentlyContinue |
            Sort-Object Id |
            Select-Object Id, ProcessName
    )
}

function New-GNFBenchmarkFinalReport {
    param(
        [Parameter(Mandatory = $true)][int]$TestExitCode,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$BeforeVirtualDisks,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$AfterVirtualDisks,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$VirtualDiskDifference,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$ProcessDifference,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$ResidualWorkItems,
        [Parameter(Mandatory = $true)][string]$ArtifactRoot
    )
    $residualPaths = @($ResidualWorkItems | ForEach-Object { $_.FullName })
    return [pscustomobject]@{
        phase = 'final'
        success = (
            $TestExitCode -eq 0 -and
            $VirtualDiskDifference.Count -eq 0 -and
            $ProcessDifference.Count -eq 0 -and
            $ResidualWorkItems.Count -eq 0
        )
        testExitCode = $TestExitCode
        beforeVirtualDisks = $BeforeVirtualDisks
        afterVirtualDisks = $AfterVirtualDisks
        virtualDiskDifference = $VirtualDiskDifference
        processDifference = $ProcessDifference
        residualWorkItems = $residualPaths
        artifactRoot = $ArtifactRoot
    }
}

$parseTokens = $null
$parseErrors = $null
[Management.Automation.Language.Parser]::ParseFile(
    $PSCommandPath, [ref]$parseTokens, [ref]$parseErrors
) | Out-Null
if ($parseErrors.Count -ne 0) { throw "PowerShell parser rejected ${PSCommandPath}: $($parseErrors[0].Message)" }

if ([bool]$Smoke -eq [bool]$Full) { throw 'Specify exactly one of -Smoke or -Full.' }
if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required. The script does not request or bypass UAC.' }

$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$expectedBranch = 'codex/gnf-vhdx-single-worker-benchmark'
$branch = (& git -C $repoRoot branch --show-current).Trim()
if ($LASTEXITCODE -ne 0 -or $branch -ne $expectedBranch) { throw "expected Branch $expectedBranch; found $branch" }
$repoStatus = @(& git -C $repoRoot status --short)
if ($LASTEXITCODE -ne 0 -or $repoStatus.Count -ne 0) { throw 'benchmark repository must be clean' }

$editorPath = Resolve-SafePath -Name 'TESTPLAY_UNITY_EDITOR_PATH' -Value $env:TESTPLAY_UNITY_EDITOR_PATH
$projectPath = Resolve-SafePath -Name 'TESTPLAY_GNF_PROJECT_PATH' -Value $env:TESTPLAY_GNF_PROJECT_PATH
$workRoot = Resolve-SafePath -Name 'TESTPLAY_GNF_WORK_ROOT' -Value $env:TESTPLAY_GNF_WORK_ROOT
$artifactRoot = Resolve-SafePath -Name 'TESTPLAY_GNF_ARTIFACT_ROOT' -Value $env:TESTPLAY_GNF_ARTIFACT_ROOT
if ((Test-PathsOverlap $projectPath $workRoot) -or (Test-PathsOverlap $projectPath $artifactRoot) -or (Test-PathsOverlap $workRoot $artifactRoot)) {
    throw 'project, work, and artifact roots must not overlap'
}
if (-not (Test-Path -LiteralPath $editorPath -PathType Leaf)) { throw "Unity Editor not found: $editorPath" }
if (-not (Test-Path -LiteralPath (Join-Path $projectPath 'Assets') -PathType Container)) { throw "GNF_ Assets missing: $projectPath" }
Assert-EmptyWorkRoot -Path $workRoot
New-Item -ItemType Directory -Force -Path $artifactRoot | Out-Null

$projectVersionPath = Join-Path $projectPath 'ProjectSettings\ProjectVersion.txt'
$projectVersion = Get-Content -LiteralPath $projectVersionPath -Raw
if ($projectVersion -notmatch 'm_EditorVersion:\s*6000\.3\.8f1') { throw 'GNF_ ProjectVersion is not 6000.3.8f1' }
$versionOutput = (& $editorPath -version 2>&1 | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $versionOutput -notmatch [regex]::Escape('6000.3.8f1')) { throw "Unity version mismatch: $versionOutput" }

$gnfStatus = @(& git -C $projectPath status --porcelain)
if ($LASTEXITCODE -ne 0) { throw 'TESTPLAY_GNF_PROJECT_PATH is not a readable Git worktree' }
if ($gnfStatus.Count -ne 0) { throw 'GNF_ working tree must be clean; the source is read-only input' }
$sourceRevision = (& git -C $projectPath rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($sourceRevision)) { throw 'GNF_ revision could not be resolved' }

$selectionFile = Get-ChildItem -LiteralPath (Join-Path $projectPath 'Assets') -Recurse -File -Filter 'CodexMovementSmokeTest.cs' |
    Where-Object { Select-String -LiteralPath $_.FullName -SimpleMatch 'TestPlayer_MovesRight_InPlayMode' -Quiet } |
    Select-Object -First 1
if ($null -eq $selectionFile) { throw 'stable GNF_ selection CodexMovementSmokeTest.TestPlayer_MovesRight_InPlayMode was not found' }

$parentGiB = 16
if (-not [string]::IsNullOrWhiteSpace($env:TESTPLAY_GNF_PARENT_VHDX_SIZE_GIB)) {
    $parentGiB = [int]$env:TESTPLAY_GNF_PARENT_VHDX_SIZE_GIB
    if ($parentGiB -le 0) { throw 'TESTPLAY_GNF_PARENT_VHDX_SIZE_GIB must be positive' }
}
$workDrive = Get-PSDrive -Name ([IO.Path]::GetPathRoot($workRoot).TrimEnd(':\'))
$requiredBytes = ([int64]$parentGiB + 10) * 1GB
if ($workDrive.Free -lt $requiredBytes) { throw "insufficient free space: need at least $requiredBytes bytes; available $($workDrive.Free)" }

$beforeVirtualDisks = @(Get-FileBackedVirtualDisks)
$beforeProcesses = @(Get-BenchmarkProcesses)
$helperPath = Join-Path ([IO.Path]::GetTempPath()) "testplay-storage-helper-gnf-$PID.exe"
if (Test-Path -LiteralPath $helperPath) { throw "refusing to overwrite helper path: $helperPath" }
$mode = if ($Smoke) { 'smoke' } else { 'full' }
$goTestTimeout = if ($Smoke) { '2h' } else { '24h' }
$testExitCode = 1

try {
    & go build -o $helperPath ./cmd/testplay-storage-helper
    if ($LASTEXITCODE -ne 0) { throw "building testplay-storage-helper failed: $LASTEXITCODE" }
    $env:TESTPLAY_STORAGE_HELPER_PATH = $helperPath
    $env:TESTPLAY_GNF_BENCHMARK_MODE = $mode
    $env:TESTPLAY_GNF_SOURCE_REVISION = $sourceRevision
    $env:TESTPLAY_GNF_PARENT_VHDX_SIZE_GIB = $parentGiB.ToString([Globalization.CultureInfo]::InvariantCulture)

    [pscustomobject]@{
        phase = 'preflight'
        administrator = $true
        repository = $repoRoot
        branch = $branch
        unityEditorPath = $editorPath
        unityVersion = $versionOutput
        gnfProjectPath = $projectPath
        gnfRevision = $sourceRevision
        testPlatform = 'play_mode'
        testFilter = 'CodexMovementSmokeTest.TestPlayer_MovesRight_InPlayMode'
        workRoot = $workRoot
        artifactRoot = $artifactRoot
        mode = $mode
        goTestTimeout = $goTestTimeout
        concurrency = 1
        freeBytes = $workDrive.Free
        beforeVirtualDisks = $beforeVirtualDisks
    } | ConvertTo-Json -Depth 6

    & go test -tags=gnf_vhdx_integration ./internal/gnfvhdxbenchmark `
        -run '^TestGNFVHDXSingleWorkerBenchmark$' -v -count=1 `
        -timeout $goTestTimeout
    $testExitCode = $LASTEXITCODE
}
finally {
    Remove-Item Env:TESTPLAY_STORAGE_HELPER_PATH -ErrorAction SilentlyContinue
    Remove-Item Env:TESTPLAY_GNF_BENCHMARK_MODE -ErrorAction SilentlyContinue
    Remove-Item Env:TESTPLAY_GNF_SOURCE_REVISION -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $helperPath) { Remove-Item -LiteralPath $helperPath -Force }
}

$afterVirtualDisks = @(Get-FileBackedVirtualDisks)
$afterProcesses = @(Get-BenchmarkProcesses)
$diskDifference = @(Compare-Object $beforeVirtualDisks $afterVirtualDisks -Property Number, FriendlyName, SerialNumber, OperationalStatus, PartitionStyle, IsOffline, IsReadOnly)
$processDifference = @(Compare-Object $beforeProcesses $afterProcesses -Property Id, ProcessName)
$residualWorkItems = @()
if (Test-Path -LiteralPath $workRoot) {
    # A non-empty top-level is already sufficient to fail the cleanup gate.
    # Avoid recursively walking large Unity Libraries or stale mount paths while
    # producing the final diagnostic report.
    $residualWorkItems = @(Get-ChildItem -LiteralPath $workRoot -Force)
}

New-GNFBenchmarkFinalReport -TestExitCode $testExitCode -BeforeVirtualDisks $beforeVirtualDisks -AfterVirtualDisks $afterVirtualDisks -VirtualDiskDifference $diskDifference -ProcessDifference $processDifference -ResidualWorkItems $residualWorkItems -ArtifactRoot $artifactRoot |
    ConvertTo-Json -Depth 6

if ($testExitCode -ne 0 -or $diskDifference.Count -ne 0 -or $processDifference.Count -ne 0 -or $residualWorkItems.Count -ne 0) { exit 1 }
