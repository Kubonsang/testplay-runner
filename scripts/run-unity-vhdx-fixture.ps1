[CmdletBinding()]
param(
    [ValidateSet(1, 5)]
    [int]$Count = 1,

    [switch]$ReuseParent
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

    if ([string]::IsNullOrWhiteSpace($Value)) {
        throw "$Name is required"
    }
    if (-not [IO.Path]::IsPathRooted($Value)) {
        throw "$Name must be an absolute path: $Value"
    }
    if ($Value.StartsWith('\\')) {
        throw "$Name must not be a network path: $Value"
    }
    $full = [IO.Path]::GetFullPath($Value).TrimEnd('\')
    if ($full -eq [IO.Path]::GetPathRoot($full).TrimEnd('\')) {
        throw "$Name must not be a drive root: $Value"
    }
    return $full
}

function Assert-EmptyFixtureRoot {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path)) {
        return
    }
    $item = Get-Item -LiteralPath $Path -Force
    if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw "fixture root must be a real directory: $Path"
    }
    $entries = @(Get-ChildItem -LiteralPath $Path -Force)
    if ($entries.Count -ne 0) {
        throw "fixture root must be empty; found $($entries.Count) item(s): $Path"
    }
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

function New-UnityVHDXFinalReport {
    param(
        [Parameter(Mandatory = $true)][int]$TestExitCode,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$BeforeVirtualDisks,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$AfterVirtualDisks,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$VirtualDiskDifference,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$ResidualFixtureItems,
        [Parameter(Mandatory = $true)][string]$ArtifactRoot
    )

    $residualFixtureItemPaths = @(
        $ResidualFixtureItems |
            ForEach-Object { $_.FullName }
    )

    return [pscustomobject]@{
        phase = 'final'
        success = (
            $TestExitCode -eq 0 -and
            $VirtualDiskDifference.Count -eq 0 -and
            $ResidualFixtureItems.Count -eq 0
        )
        testExitCode = $TestExitCode
        beforeVirtualDisks = $BeforeVirtualDisks
        afterVirtualDisks = $AfterVirtualDisks
        virtualDiskDifference = $VirtualDiskDifference
        residualFixtureItems = $residualFixtureItemPaths
        artifactRoot = $ArtifactRoot
    }
}

$parseTokens = $null
$parseErrors = $null
[Management.Automation.Language.Parser]::ParseFile(
    $PSCommandPath,
    [ref]$parseTokens,
    [ref]$parseErrors
) | Out-Null
if ($parseErrors.Count -ne 0) {
    throw "PowerShell parser rejected ${PSCommandPath}: $($parseErrors[0].Message)"
}

if (-not (Test-Administrator)) {
    throw 'Administrator PowerShell is required. The script does not request or bypass UAC.'
}
if ($Count -eq 5 -and -not $ReuseParent) {
    throw '-Count 5 requires -ReuseParent so one immutable Parent is shared by five new sequential Children.'
}

$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$expectedBranch = 'codex/unity-vhdx-library-fixture'
$branch = (& git -C $repoRoot branch --show-current).Trim()
if ($LASTEXITCODE -ne 0 -or $branch -ne $expectedBranch) {
    throw "expected Branch $expectedBranch; found $branch"
}
$status = @(& git -C $repoRoot status --short)
if ($LASTEXITCODE -ne 0 -or $status.Count -ne 0) {
    throw 'repository must have no tracked or untracked changes before elevated validation'
}

$editorPath = Resolve-SafePath -Name 'TESTPLAY_UNITY_EDITOR_PATH' -Value $env:TESTPLAY_UNITY_EDITOR_PATH
$fixtureRoot = Resolve-SafePath -Name 'TESTPLAY_UNITY_VHDX_FIXTURE_ROOT' -Value $env:TESTPLAY_UNITY_VHDX_FIXTURE_ROOT
$artifactRoot = Resolve-SafePath -Name 'TESTPLAY_UNITY_VHDX_ARTIFACT_ROOT' -Value $env:TESTPLAY_UNITY_VHDX_ARTIFACT_ROOT
if ($fixtureRoot -eq $artifactRoot) {
    throw 'fixture root and artifact root must differ'
}
$fixturePrefix = $fixtureRoot.TrimEnd('\') + '\'
$artifactPrefix = $artifactRoot.TrimEnd('\') + '\'
if ($fixturePrefix.StartsWith($artifactPrefix, [StringComparison]::OrdinalIgnoreCase) -or
    $artifactPrefix.StartsWith($fixturePrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'fixture root and artifact root must not contain one another'
}
if (-not (Test-Path -LiteralPath $editorPath -PathType Leaf)) {
    throw "Unity Editor was not found: $editorPath"
}
Assert-EmptyFixtureRoot -Path $fixtureRoot
New-Item -ItemType Directory -Force -Path $artifactRoot | Out-Null

$versionOutput = (& $editorPath -version 2>&1 | Out-String).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "Unity version query failed with exit code ${LASTEXITCODE}: $versionOutput"
}
if ($versionOutput -notmatch [regex]::Escape('6000.3.8f1')) {
    throw "Unity version mismatch. Fixture requires 6000.3.8f1; Editor reported: $versionOutput"
}

$beforeVirtualDisks = @(Get-FileBackedVirtualDisks)
$helperPath = Join-Path ([IO.Path]::GetTempPath()) "testplay-storage-helper-$PID.exe"
if (Test-Path -LiteralPath $helperPath) {
    throw "refusing to overwrite existing helper path: $helperPath"
}

$env:TESTPLAY_UNITY_VHDX_REPO_ROOT = $repoRoot
$env:TESTPLAY_UNITY_VHDX_COUNT = $Count.ToString([Globalization.CultureInfo]::InvariantCulture)
$env:TESTPLAY_UNITY_VHDX_REUSE_PARENT = if ($ReuseParent) { '1' } else { '0' }
$testExitCode = 1

try {
    & go build -o $helperPath ./cmd/testplay-storage-helper
    if ($LASTEXITCODE -ne 0) {
        throw "building testplay-storage-helper failed with exit code $LASTEXITCODE"
    }
    $env:TESTPLAY_STORAGE_HELPER_PATH = $helperPath

    [pscustomobject]@{
        phase = 'preflight'
        administrator = $true
        repository = $repoRoot
        branch = $branch
        unityEditorPath = $editorPath
        unityVersion = $versionOutput
        fixtureRoot = $fixtureRoot
        artifactRoot = $artifactRoot
        count = $Count
        reuseParent = [bool]$ReuseParent
        beforeVirtualDisks = $beforeVirtualDisks
    } | ConvertTo-Json -Depth 6

    & go test -tags=unity_vhdx_integration ./internal/unityvhdxfixture `
        -run '^TestUnityVHDXLibraryFixture$' `
        -v -count=1
    $testExitCode = $LASTEXITCODE
}
finally {
    Remove-Item Env:TESTPLAY_STORAGE_HELPER_PATH -ErrorAction SilentlyContinue
    Remove-Item Env:TESTPLAY_UNITY_VHDX_REPO_ROOT -ErrorAction SilentlyContinue
    Remove-Item Env:TESTPLAY_UNITY_VHDX_COUNT -ErrorAction SilentlyContinue
    Remove-Item Env:TESTPLAY_UNITY_VHDX_REUSE_PARENT -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $helperPath) {
        Remove-Item -LiteralPath $helperPath -Force
    }
}

$afterVirtualDisks = @(Get-FileBackedVirtualDisks)
$difference = @(
    Compare-Object $beforeVirtualDisks $afterVirtualDisks `
        -Property Number, FriendlyName, SerialNumber,
                  OperationalStatus, PartitionStyle,
                  IsOffline, IsReadOnly
)
$residualFixtureItems = @()
if (Test-Path -LiteralPath $fixtureRoot) {
    $residualFixtureItems = @(Get-ChildItem -LiteralPath $fixtureRoot -Force -Recurse)
}

New-UnityVHDXFinalReport `
    -TestExitCode $testExitCode `
    -BeforeVirtualDisks $beforeVirtualDisks `
    -AfterVirtualDisks $afterVirtualDisks `
    -VirtualDiskDifference $difference `
    -ResidualFixtureItems $residualFixtureItems `
    -ArtifactRoot $artifactRoot |
    ConvertTo-Json -Depth 6

if ($testExitCode -ne 0 -or $difference.Count -ne 0 -or $residualFixtureItems.Count -ne 0) {
    exit 1
}
