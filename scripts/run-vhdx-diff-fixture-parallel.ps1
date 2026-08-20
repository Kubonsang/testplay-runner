[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$UnityEditorPath,

    [ValidateSet(2, 4)]
    [int]$WorkerCount = 2,

    [switch]$InstallApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'vhdx-diff-gnf-evidence.ps1')
. (Join-Path $PSScriptRoot 'vhdx-diff-fixture-parallel-evidence.ps1')

$ExpectedTest = 'TestPlayFixture.Tests.LibraryMountTests.LibraryMountWriteReadTest'

function Test-Administrator {
    $Principal = [Security.Principal.WindowsPrincipal]([Security.Principal.WindowsIdentity]::GetCurrent())
    return $Principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-JsonFile {
    param([string]$LiteralPath, [object]$Value)
    $Json = $Value | ConvertTo-Json -Depth 24
    [IO.File]::WriteAllText($LiteralPath, $Json + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
}

function Invoke-NativeCapture {
    param([string]$LiteralPath, [string[]]$ArgumentList, [string]$OutputPath, [string]$WorkingDirectory)
    $PreviousPreference = $ErrorActionPreference
    Push-Location $WorkingDirectory
    try {
        $ErrorActionPreference = 'Continue'
        $Lines = @(& $LiteralPath @ArgumentList 2>&1)
        $ExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $PreviousPreference
        Pop-Location
    }
    [IO.File]::WriteAllLines($OutputPath, [string[]]@($Lines | ForEach-Object { $_.ToString() }), [Text.UTF8Encoding]::new($false))
    return [pscustomobject]@{ ExitCode = $ExitCode; Lines = @($Lines) }
}

function Read-NativeJson {
    param([string]$LiteralPath)
    $Raw = [IO.File]::ReadAllText($LiteralPath)
    $Start = $Raw.IndexOf('{')
    if ($Start -lt 0) { throw "No JSON object in native output: $LiteralPath" }
    return $Raw.Substring($Start) | ConvertFrom-Json
}

function Get-TreeDigest {
    param([string]$Root)
    $Builder = [Text.StringBuilder]::new()
    $Files = @(Get-ChildItem -LiteralPath $Root -Recurse -File -Force | Sort-Object FullName)
    [long]$Bytes = 0
    foreach ($File in $Files) {
        $Relative = $File.FullName.Substring($Root.TrimEnd('\').Length).TrimStart('\').Replace('\', '/')
        $Hash = (Get-FileHash -LiteralPath $File.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        [void]$Builder.Append($Relative).Append([char]0).Append($Hash).Append([char]0)
        $Bytes += $File.Length
    }
    $SHA = [Security.Cryptography.SHA256]::Create()
    try { $Digest = ([BitConverter]::ToString($SHA.ComputeHash([Text.Encoding]::UTF8.GetBytes($Builder.ToString())))).Replace('-', '').ToLowerInvariant() }
    finally { $SHA.Dispose() }
    return [ordered]@{ digest = $Digest; fileCount = $Files.Count; logicalBytes = $Bytes }
}

function Get-SourceEvidence {
    param([string]$Root)
    return [ordered]@{
        assets = Get-TreeDigest -Root (Join-Path $Root 'Assets')
        packages = Get-TreeDigest -Root (Join-Path $Root 'Packages')
        projectSettings = Get-TreeDigest -Root (Join-Path $Root 'ProjectSettings')
    }
}

function Assert-SourceEqual {
    param([object]$Before, [object]$After)
    foreach ($Name in @('assets', 'packages', 'projectSettings')) {
        if ($Before[$Name].digest -ne $After[$Name].digest -or $Before[$Name].fileCount -ne $After[$Name].fileCount -or $Before[$Name].logicalBytes -ne $After[$Name].logicalBytes) {
            throw "Fixture source tree changed: $Name"
        }
    }
}

function Get-FileBackedDisks {
    return @(Get-Disk -ErrorAction SilentlyContinue | Where-Object { $_.BusType -eq 'File Backed Virtual' } | Select-Object Number, FriendlyName, BusType, PartitionStyle)
}

function Get-DriveLetters {
    return @(Get-Volume -ErrorAction SilentlyContinue | Where-Object { $null -ne $_.DriveLetter } | Select-Object UniqueId, DriveLetter, FileSystem)
}

function Get-RelatedProcesses {
    return @(Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -match '^(Unity|testplay|testplay-vhdx)' } | Select-Object Id, ProcessName, StartTime)
}

if (-not $InstallApproved) { throw 'Pass -InstallApproved after reviewing the exact unique store and cleanup contract.' }
if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }
if (-not (Test-Path -LiteralPath $UnityEditorPath -PathType Leaf)) { throw "Unity Editor was not found: $UnityEditorPath" }

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$FixtureTemplate = Join-Path $RepositoryRoot 'testdata\unity-vhdx-fixture'
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-fixture-${WorkerCount}worker-$Stamp"
$FixtureSource = Join-Path $ArtifactRoot 'fixture-source'
$StoreRoot = Join-Path $env:ProgramData "TestPlay\VHDXDiffFixture${WorkerCount}Worker-$Stamp"
$WorkspaceRoot = Join-Path $env:LOCALAPPDATA 'TestPlay\Workspaces'
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-fixture-parallel.exe'
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$ScenarioOutputPath = Join-Path $ArtifactRoot 'scenario-output.txt'
$ZipPath = "$ArtifactRoot.zip"

if (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) { throw 'TestPlayStorageBroker already exists; refusing to replace it.' }
foreach ($Path in @($ReceiptPath, $StoreRoot, $ArtifactRoot, $WorkspaceRoot)) {
    if (Test-Path -LiteralPath $Path) { throw "Pre-existing state is outside this harness ownership: $Path" }
}

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
New-Item -ItemType Directory -Path $FixtureSource | Out-Null
foreach ($Name in @('Assets', 'Packages', 'ProjectSettings')) {
    Copy-Item -LiteralPath (Join-Path $FixtureTemplate $Name) -Destination $FixtureSource -Recurse
}

$SourceBefore = Get-SourceEvidence -Root $FixtureSource
$PreDisks = @(Get-FileBackedDisks)
$PreLetters = @(Get-DriveLetters)
$PreProcesses = @(Get-RelatedProcesses)
$Installed = $false
$Uninstalled = $false
$Failure = $null
$ScenarioResult = $null
$ParallelEvidence = $null
$ParentEvidence = $null
$StatusJSON = $null
$Started = Get-Date

Start-Transcript -Path (Join-Path $ArtifactRoot 'terminal-transcript.txt') -Force | Out-Null
try {
    Push-Location $RepositoryRoot
    try {
        & go build -o $ExecutablePath .\cmd\testplay
        if ($LASTEXITCODE -ne 0) { throw "go build failed: exit=$LASTEXITCODE" }
    }
    finally { Pop-Location }

    $Install = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'install', '--root', $StoreRoot) -OutputPath (Join-Path $ArtifactRoot 'storage-install.txt') -WorkingDirectory $ArtifactRoot
    if ($Install.ExitCode -ne 0) { throw "storage install failed: exit=$($Install.ExitCode)" }
    $Installed = $true

    $Instances = @()
    for ($Index = 1; $Index -le $WorkerCount; $Index++) {
        $Role = "worker-$Index"
        $ConfigName = "testplay-$Role.json"
        $ConfigPath = Join-Path $ArtifactRoot $ConfigName
        Write-JsonFile -LiteralPath $ConfigPath -Value ([ordered]@{
            schema_version = '1'
            unity_path = $UnityEditorPath
            project_path = $FixtureSource
            test_platform = 'edit_mode'
            timeout = [ordered]@{ total_ms = 900000 }
            result_dir = (Join-Path $ArtifactRoot "results-$Role")
            workspace = [ordered]@{
                backend = 'vhdx-diff'
                store_root = $StoreRoot
                store_max_allocated_bytes = 34359738368
                minimum_host_free_bytes = 21474836480
            }
        })
        $Instances += [ordered]@{
            role = $Role
            config = $ConfigName
            env = [ordered]@{ TESTPLAY_UNITY_FIXTURE_MARKER = "vhdx-diff-$Stamp-$Role" }
        }
    }
    $ScenarioPath = Join-Path $ArtifactRoot 'scenario.json'
    Write-JsonFile -LiteralPath $ScenarioPath -Value ([ordered]@{ schema_version = '1'; instances = @($Instances) })

    $ScenarioRun = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('--config', (Join-Path $ArtifactRoot 'testplay-worker-1.json'), 'run', '--scenario', $ScenarioPath, '--filter', $ExpectedTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge') -OutputPath $ScenarioOutputPath -WorkingDirectory $ArtifactRoot
    if ($ScenarioRun.ExitCode -ne 0) { throw "fixture scenario failed: exit=$($ScenarioRun.ExitCode)" }
    $ScenarioResult = Read-NativeJson -LiteralPath $ScenarioOutputPath
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'scenario-result.json') -Value $ScenarioResult
    $ParallelEvidence = Assert-VHDXDiffFixtureParallelResult -Result $ScenarioResult -WorkerCount $WorkerCount -ExpectedTest $ExpectedTest
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'parallel-evidence.json') -Value $ParallelEvidence

    $ParentPath = $ParallelEvidence.parentPath
    $MetadataPath = Join-Path (Split-Path -Parent $ParentPath) 'metadata.json'
    $ParentMetadata = Get-Content -LiteralPath $MetadataPath -Raw | ConvertFrom-Json
    $ParentFile = Get-Item -LiteralPath $ParentPath -Force
    $ParentHash = (Get-FileHash -LiteralPath $ParentPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($ParentHash -ne ([string]$ParentMetadata.committedSha256).ToLowerInvariant()) { throw 'Immutable parent hash does not match committed metadata.' }
    if (-not $ParentFile.IsReadOnly) { throw 'Committed parent is not read-only.' }
    $ParentEvidence = [ordered]@{
        path = $ParentPath
        key = $ParallelEvidence.parentKey
        sha256 = $ParentHash
        length = $ParentFile.Length
        lastWriteTimeUtc = $ParentFile.LastWriteTimeUtc.ToString('o')
        readOnly = $ParentFile.IsReadOnly
        metadata = $ParentMetadata
    }
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'parent-evidence.json') -Value $ParentEvidence

    $Status = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'storage-status.json') -WorkingDirectory $ArtifactRoot
    if ($Status.ExitCode -ne 0) { throw "storage status failed: exit=$($Status.ExitCode)" }
    $StatusJSON = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'storage-status.json')
    Assert-VHDXDiffStorageStatus -Status $StatusJSON

    $SourceAfterRuns = Get-SourceEvidence -Root $FixtureSource
    Assert-SourceEqual -Before $SourceBefore -After $SourceAfterRuns
}
catch { $Failure = $_.Exception.ToString() }
finally {
    if ($Installed) {
        try {
            $Uninstall = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'uninstall') -OutputPath (Join-Path $ArtifactRoot 'storage-uninstall.txt') -WorkingDirectory $ArtifactRoot
            $Uninstalled = $Uninstall.ExitCode -eq 0
            if (-not $Uninstalled -and $null -eq $Failure) { $Failure = "storage uninstall failed: exit=$($Uninstall.ExitCode)" }
        }
        catch { if ($null -eq $Failure) { $Failure = $_.Exception.ToString() } }
    }
    Stop-Transcript | Out-Null
}

$SourceAfter = Get-SourceEvidence -Root $FixtureSource
try { Assert-SourceEqual -Before $SourceBefore -After $SourceAfter }
catch { if ($null -eq $Failure) { $Failure = $_.Exception.ToString() } }
$PostDisks = @(Get-FileBackedDisks)
$PostLetters = @(Get-DriveLetters)
$PostProcesses = @(Get-RelatedProcesses)
$PreDiskIDs = @($PreDisks | ForEach-Object { $_.Number })
$PreLetterIDs = @($PreLetters | ForEach-Object { $_.UniqueId })
$PreProcessIDs = @($PreProcesses | ForEach-Object { $_.Id })
$NewDisks = @($PostDisks | Where-Object { $PreDiskIDs -notcontains $_.Number })
$NewLetters = @($PostLetters | Where-Object { $PreLetterIDs -notcontains $_.UniqueId })
$NewProcesses = @($PostProcesses | Where-Object { $PreProcessIDs -notcontains $_.Id })
$ResidualZero = $NewDisks.Count -eq 0 -and $NewLetters.Count -eq 0 -and $NewProcesses.Count -eq 0 -and -not (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) -and -not (Test-Path -LiteralPath $ReceiptPath) -and -not (Test-Path -LiteralPath $StoreRoot) -and -not (Test-Path -LiteralPath $WorkspaceRoot)
if (-not $ResidualZero -and $null -eq $Failure) { $Failure = 'Outer residual is nonzero.' }
$Passed = $null -eq $Failure -and $Uninstalled -and $ResidualZero
$Verdict = if ($WorkerCount -eq 2) { 'UNITY_VHDX_DIFF_TWO_WORKERS_COMPATIBLE' } else { 'UNITY_VHDX_DIFF_FOUR_WORKERS_COMPATIBLE' }

$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { $Verdict } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    repository = $RepositoryRoot
    unityEditor = $UnityEditorPath
    fixtureTemplate = $FixtureTemplate
    fixtureSource = $FixtureSource
    workerCount = $WorkerCount
    selectedTest = $ExpectedTest
    sourceBefore = $SourceBefore
    sourceAfter = $SourceAfter
    scenario = $ScenarioResult
    parallelEvidence = $ParallelEvidence
    parent = $ParentEvidence
    storageStatus = $StatusJSON
    installed = $Installed
    uninstalled = $Uninstalled
    residualZero = $ResidualZero
    preFileBackedDisks = @($PreDisks)
    postFileBackedDisks = @($PostDisks)
    newFileBackedDisks = @($NewDisks)
    preDriveLetters = @($PreLetters)
    postDriveLetters = @($PostLetters)
    newDriveLetters = @($NewLetters)
    preProcesses = @($PreProcesses)
    postProcesses = @($PostProcesses)
    newProcesses = @($NewProcesses)
    failure = $Failure
    notMeasured = @('GNF two/four workers', 'fixture forced termination recovery', 'broker restart recovery', 'Windows reboot recovery', 'quota/LRU native behavior', 'performance superiority', 'production readiness', 'release readiness')
}
Write-JsonFile -LiteralPath $SummaryPath -Value $Summary
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash
Write-Output "VHDX_DIFF_FIXTURE_PARALLEL_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_FIXTURE_PARALLEL_VERDICT=$($Summary.verdict)"
Write-Output "VHDX_DIFF_FIXTURE_PARALLEL_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_FIXTURE_PARALLEL_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }

