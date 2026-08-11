[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)] [string]$UnityEditorPath,
    [Parameter(Mandatory = $true)] [string]$GNFProjectPath,
    [Parameter(Mandatory = $true)] [string]$LocalPackagePath,
    [Parameter(Mandatory = $true)] [string]$ReferenceArtifactZip,
    [Parameter(Mandatory = $true)] [string]$ReferenceArtifactSHA256,
    [ValidateSet(2, 4)] [int]$WorkerCount = 2,
    [switch]$InstallApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'vhdx-diff-gnf-evidence.ps1')
. (Join-Path $PSScriptRoot 'vhdx-diff-gnf-parallel-evidence.ps1')

$ExpectedGNFRevision = '19a17074f6366038cd5b17c01e0a904f0d585470'
$ExpectedPackageRevision = '149896faeb3b5165a3af4739342c637ed66d94b6'
$ExpectedUnityVersion = '6000.3.8f1'
$PackageName = 'com.youngwoocho02.unity-cli-connector'
$EditTest = 'GNF.DungeonGen.Tests.WallPropValidatorTests.NullPrefab_Error'
$PlayTest = 'DOOR_CONSENSUS_Tests.Proximity_CountsNearestExitWithinRadius'

function Test-Administrator {
    $Principal = [Security.Principal.WindowsPrincipal]([Security.Principal.WindowsIdentity]::GetCurrent())
    return $Principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-JsonFile {
    param([string]$LiteralPath, [object]$Value)
    $Json = $Value | ConvertTo-Json -Depth 30
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
    finally { $ErrorActionPreference = $PreviousPreference; Pop-Location }
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

function Read-ZipJson {
    param([string]$ZipPath, [string]$EntryName)
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $Archive = [IO.Compression.ZipFile]::OpenRead($ZipPath)
    try {
        $Entry = $Archive.GetEntry($EntryName)
        if ($null -eq $Entry) { throw "Reference artifact entry is missing: $EntryName" }
        $Reader = [IO.StreamReader]::new($Entry.Open())
        try { return $Reader.ReadToEnd() | ConvertFrom-Json }
        finally { $Reader.Dispose() }
    }
    finally { $Archive.Dispose() }
}

function Get-GitText {
    param([string]$Repository, [string[]]$Arguments)
    $Lines = @(& git -C $Repository @Arguments 2>&1)
    if ($LASTEXITCODE -ne 0) { throw "git failed in ${Repository}: $($Lines -join [Environment]::NewLine)" }
    return ($Lines -join [Environment]::NewLine).Trim()
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
        revision = Get-GitText -Repository $Root -Arguments @('rev-parse', 'HEAD')
        branch = Get-GitText -Repository $Root -Arguments @('branch', '--show-current')
        status = Get-GitText -Repository $Root -Arguments @('status', '--porcelain=v1', '--untracked-files=all')
        assets = Get-TreeDigest -Root (Join-Path $Root 'Assets')
        packages = Get-TreeDigest -Root (Join-Path $Root 'Packages')
        projectSettings = Get-TreeDigest -Root (Join-Path $Root 'ProjectSettings')
        packagesLockSHA256 = (Get-FileHash -LiteralPath (Join-Path $Root 'Packages\packages-lock.json') -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

function Assert-SourceEqual {
    param([object]$Before, [object]$After)
    foreach ($Name in @('revision', 'branch', 'status', 'packagesLockSHA256')) {
        if ($Before[$Name] -ne $After[$Name]) { throw "GNF source changed: $Name" }
    }
    foreach ($Name in @('assets', 'packages', 'projectSettings')) {
        if ($Before[$Name].digest -ne $After[$Name].digest -or $Before[$Name].fileCount -ne $After[$Name].fileCount -or $Before[$Name].logicalBytes -ne $After[$Name].logicalBytes) { throw "GNF source tree changed: $Name" }
    }
}

function Get-CanonicalDigest {
    param([object]$Value)
    $Json = $Value | ConvertTo-Json -Depth 10 -Compress
    $SHA = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($SHA.ComputeHash([Text.Encoding]::UTF8.GetBytes($Json)))).Replace('-', '').ToLowerInvariant() }
    finally { $SHA.Dispose() }
}

function Compare-ReferenceResult {
    param([object]$Reference, [object]$Worker, [string]$Platform, [string]$Role)
    $ReferenceTests = @($Reference.tests | ForEach-Object { [ordered]@{ name = $_.fullName; result = $_.outcome } })
    $WorkerTests = @($Worker.tests | ForEach-Object { [ordered]@{ name = $_.name; result = $_.result } })
    $ReferenceCanonical = [ordered]@{ platform = $Platform; exitCode = $Reference.exitCode; total = $Reference.total; passed = $Reference.passed; failed = $Reference.failed; skipped = $Reference.skipped; tests = $ReferenceTests; compileErrors = @($Reference.compileErrors) }
    $WorkerCanonical = [ordered]@{ platform = $Platform; exitCode = $Worker.exit_code; total = $Worker.total; passed = $Worker.passed; failed = $Worker.failed; skipped = $Worker.skipped; tests = $WorkerTests; compileErrors = @($Worker.errors) }
    $ReferenceDigest = Get-CanonicalDigest -Value $ReferenceCanonical
    $WorkerDigest = Get-CanonicalDigest -Value $WorkerCanonical
    if ($ReferenceDigest -ne $WorkerDigest) { throw "Reference/GNF worker semantic mismatch: platform=$Platform role=$Role" }
    return [ordered]@{ status = 'PASS'; role = $Role; referenceDigest = $ReferenceDigest; workerDigest = $WorkerDigest; historicalReferenceDigest = $Reference.semanticDigest }
}

function Get-FileBackedDisks { return @(Get-Disk -ErrorAction SilentlyContinue | Where-Object { $_.BusType -eq 'File Backed Virtual' } | Select-Object Number, FriendlyName, BusType, PartitionStyle) }
function Get-DriveLetters { return @(Get-Volume -ErrorAction SilentlyContinue | Where-Object { $null -ne $_.DriveLetter } | Select-Object UniqueId, DriveLetter, FileSystem) }
function Get-RelatedProcesses { return @(Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -match '^(Unity|testplay|testplay-vhdx)' } | Select-Object Id, ProcessName, StartTime) }

if (-not $InstallApproved) { throw 'Pass -InstallApproved after reviewing the exact unique store and cleanup contract.' }
if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }
foreach ($Path in @($UnityEditorPath, $ReferenceArtifactZip)) { if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "Required file was not found: $Path" } }
foreach ($Path in @($GNFProjectPath, $LocalPackagePath)) { if (-not (Test-Path -LiteralPath $Path -PathType Container)) { throw "Required directory was not found: $Path" } }

$UnityEditorPath = (Resolve-Path -LiteralPath $UnityEditorPath).Path
$GNFProjectPath = (Resolve-Path -LiteralPath $GNFProjectPath).Path
$LocalPackagePath = (Resolve-Path -LiteralPath $LocalPackagePath).Path
$ReferenceArtifactZip = (Resolve-Path -LiteralPath $ReferenceArtifactZip).Path
$ReferenceHash = (Get-FileHash -LiteralPath $ReferenceArtifactZip -Algorithm SHA256).Hash
if ($ReferenceHash -ne $ReferenceArtifactSHA256) { throw "Reference artifact SHA-256 mismatch: actual=$ReferenceHash" }
$ReferenceSummary = Read-ZipJson -ZipPath $ReferenceArtifactZip -EntryName 'summary.json'
$ReferenceSelection = Read-ZipJson -ZipPath $ReferenceArtifactZip -EntryName 'test-selection.json'
$ReferenceRun = Read-ZipJson -ZipPath $ReferenceArtifactZip -EntryName 'reference-smoke-1.json'
if ($ReferenceSummary.status -ne 'PASS' -or $ReferenceSummary.verdict -ne 'GNF_NTFS_REFERENCE_STABLE') { throw 'Pinned NTFS reference evidence is not PASS.' }
if (@($ReferenceSelection.editMode).Count -ne 1 -or $ReferenceSelection.editMode[0] -ne $EditTest -or @($ReferenceSelection.playMode).Count -ne 1 -or $ReferenceSelection.playMode[0] -ne $PlayTest) { throw 'Pinned NTFS test selection does not match the frozen selection.' }

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-gnf-${WorkerCount}worker-$Stamp"
$StoreRoot = Join-Path $env:ProgramData "TestPlay\VHDXDiffGNF${WorkerCount}Worker-$Stamp"
$WorkspaceRoot = Join-Path $env:LOCALAPPDATA 'TestPlay\Workspaces'
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$SourceTestPlayRoot = Join-Path $GNFProjectPath '.testplay'
$ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-gnf-parallel.exe'
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$ZipPath = "$ArtifactRoot.zip"

if (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) { throw 'TestPlayStorageBroker already exists; refusing to replace it.' }
foreach ($Path in @($ReceiptPath, $StoreRoot, $ArtifactRoot, $WorkspaceRoot, $SourceTestPlayRoot)) { if (Test-Path -LiteralPath $Path) { throw "Pre-existing state is outside this harness ownership: $Path" } }
$ProjectVersion = (Select-String -LiteralPath (Join-Path $GNFProjectPath 'ProjectSettings\ProjectVersion.txt') -Pattern '^m_EditorVersion:\s*(.+)$').Matches[0].Groups[1].Value.Trim()
if ($ProjectVersion -ne $ExpectedUnityVersion -or $UnityEditorPath -notmatch [regex]::Escape("\$ExpectedUnityVersion\")) { throw "Unity version mismatch: project=$ProjectVersion editor=$UnityEditorPath" }
$SourceBefore = Get-SourceEvidence -Root $GNFProjectPath
if ($SourceBefore.revision -ne $ExpectedGNFRevision -or $SourceBefore.status -ne '') { throw "GNF source is not the exact clean pinned revision: revision=$($SourceBefore.revision) status=$($SourceBefore.status)" }
$PackageRevision = Get-GitText -Repository $LocalPackagePath -Arguments @('rev-parse', 'HEAD')
$PackageStatus = Get-GitText -Repository $LocalPackagePath -Arguments @('status', '--porcelain=v1', '--untracked-files=all')
if ($PackageRevision -ne $ExpectedPackageRevision -or $PackageStatus -ne '') { throw "Portable package is not the exact clean pinned revision: revision=$PackageRevision status=$PackageStatus" }

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
$PreDisks = @(Get-FileBackedDisks)
$PreLetters = @(Get-DriveLetters)
$PreProcesses = @(Get-RelatedProcesses)
$Installed = $false
$Uninstalled = $false
$Failure = $null
$ScenarioResults = [ordered]@{}
$ParallelEvidence = [ordered]@{}
$Parity = [ordered]@{}
$ParentEvidence = $null
$StatusJSON = $null
$Started = Get-Date

Start-Transcript -Path (Join-Path $ArtifactRoot 'terminal-transcript.txt') -Force | Out-Null
try {
    Push-Location $RepositoryRoot
    try { & go build -o $ExecutablePath .\cmd\testplay; if ($LASTEXITCODE -ne 0) { throw "go build failed: exit=$LASTEXITCODE" } }
    finally { Pop-Location }
    $Install = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'install', '--root', $StoreRoot) -OutputPath (Join-Path $ArtifactRoot 'storage-install.txt') -WorkingDirectory $ArtifactRoot
    if ($Install.ExitCode -ne 0) { throw "storage install failed: exit=$($Install.ExitCode)" }
    $Installed = $true

    $Phases = @(
        [pscustomobject]@{ Name = 'edit_mode'; Test = $EditTest; Reference = $ReferenceRun.editMode.result; Created = 1 },
        [pscustomobject]@{ Name = 'play_mode'; Test = $PlayTest; Reference = $ReferenceRun.playMode.result; Created = 0 }
    )
    foreach ($Phase in $Phases) {
        $Instances = @()
        for ($Index = 1; $Index -le $WorkerCount; $Index++) {
            $Role = "$($Phase.Name)-worker-$Index"
            $ConfigName = "testplay-$Role.json"
            $Overrides = [ordered]@{}; $Overrides[$PackageName] = $LocalPackagePath
            Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot $ConfigName) -Value ([ordered]@{
                schema_version = '1'; unity_path = $UnityEditorPath; project_path = $GNFProjectPath; test_platform = $Phase.Name
                timeout = [ordered]@{ total_ms = 1800000 }; result_dir = (Join-Path $ArtifactRoot "results-$Role")
                workspace = [ordered]@{ backend = 'vhdx-diff'; store_root = $StoreRoot; store_max_allocated_bytes = 34359738368; minimum_host_free_bytes = 21474836480; local_package_overrides = $Overrides }
            })
            $Instances += [ordered]@{ role = $Role; config = $ConfigName }
        }
        $ScenarioPath = Join-Path $ArtifactRoot "scenario-$($Phase.Name).json"
        Write-JsonFile -LiteralPath $ScenarioPath -Value ([ordered]@{ schema_version = '1'; instances = @($Instances) })
        $OutputPath = Join-Path $ArtifactRoot "scenario-$($Phase.Name)-output.txt"
        $Run = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('--config', (Join-Path $ArtifactRoot "testplay-$($Phase.Name)-worker-1.json"), 'run', '--scenario', $ScenarioPath, '--filter', $Phase.Test, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge') -OutputPath $OutputPath -WorkingDirectory $ArtifactRoot
        $Result = Read-NativeJson -LiteralPath $OutputPath
        $ScenarioResults[$Phase.Name] = $Result
        Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot "scenario-$($Phase.Name)-result.json") -Value $Result
        if ($Run.ExitCode -ne 0) { throw "$($Phase.Name) GNF scenario failed: exit=$($Run.ExitCode)" }
        $ParallelEvidence[$Phase.Name] = Assert-VHDXDiffGNFParallelScenario -Result $Result -WorkerCount $WorkerCount -ExpectedTest $Phase.Test -ExpectedParentCreatedCount $Phase.Created
        $Parity[$Phase.Name] = @($Result.instances | ForEach-Object { Compare-ReferenceResult -Reference $Phase.Reference -Worker $_ -Platform $Phase.Name -Role $_.role })
        Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot "parallel-$($Phase.Name)-evidence.json") -Value $ParallelEvidence[$Phase.Name]
        Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot "semantic-$($Phase.Name)-parity.json") -Value $Parity[$Phase.Name]
    }

    if ($ParallelEvidence.edit_mode.parentKey -ne $ParallelEvidence.play_mode.parentKey -or $ParallelEvidence.edit_mode.parentPath -ne $ParallelEvidence.play_mode.parentPath) { throw 'GNF EditMode and PlayMode workers did not share one immutable parent.' }
    if ($ParallelEvidence.edit_mode.localPackagesDigest -ne $ParallelEvidence.play_mode.localPackagesDigest) { throw 'GNF local package digest changed between phases.' }
    $ParentPath = $ParallelEvidence.play_mode.parentPath
    $MetadataPath = Join-Path (Split-Path -Parent $ParentPath) 'metadata.json'
    $ParentMetadata = Get-Content -LiteralPath $MetadataPath -Raw | ConvertFrom-Json
    $ParentFile = Get-Item -LiteralPath $ParentPath -Force
    $ParentHash = (Get-FileHash -LiteralPath $ParentPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($ParentHash -ne ([string]$ParentMetadata.committedSha256).ToLowerInvariant()) { throw 'Immutable GNF parent hash does not match committed metadata.' }
    if (-not $ParentFile.IsReadOnly) { throw 'Committed GNF parent is not read-only.' }
    $ParentEvidence = [ordered]@{ path = $ParentPath; key = $ParallelEvidence.play_mode.parentKey; sha256 = $ParentHash; length = $ParentFile.Length; lastWriteTimeUtc = $ParentFile.LastWriteTimeUtc.ToString('o'); readOnly = $ParentFile.IsReadOnly; metadata = $ParentMetadata }
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'parent-evidence.json') -Value $ParentEvidence

    $Status = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'storage-status.json') -WorkingDirectory $ArtifactRoot
    if ($Status.ExitCode -ne 0) { throw "storage status failed: exit=$($Status.ExitCode)" }
    $StatusJSON = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'storage-status.json')
    Assert-VHDXDiffStorageStatus -Status $StatusJSON
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
    if (Test-Path -LiteralPath $SourceTestPlayRoot) {
        try {
            Copy-Item -LiteralPath $SourceTestPlayRoot -Destination (Join-Path $ArtifactRoot 'source-testplay-artifacts') -Recurse
            Remove-Item -LiteralPath $SourceTestPlayRoot -Recurse -Force
        }
        catch { if ($null -eq $Failure) { $Failure = $_.Exception.ToString() } }
    }
    Stop-Transcript | Out-Null
}

$SourceAfter = Get-SourceEvidence -Root $GNFProjectPath
try { Assert-SourceEqual -Before $SourceBefore -After $SourceAfter }
catch { if ($null -eq $Failure) { $Failure = $_.Exception.ToString() } }
$PostDisks = @(Get-FileBackedDisks); $PostLetters = @(Get-DriveLetters); $PostProcesses = @(Get-RelatedProcesses)
$PreDiskIDs = @($PreDisks | ForEach-Object { $_.Number }); $PreLetterIDs = @($PreLetters | ForEach-Object { $_.UniqueId }); $PreProcessIDs = @($PreProcesses | ForEach-Object { $_.Id })
$NewDisks = @($PostDisks | Where-Object { $PreDiskIDs -notcontains $_.Number }); $NewLetters = @($PostLetters | Where-Object { $PreLetterIDs -notcontains $_.UniqueId }); $NewProcesses = @($PostProcesses | Where-Object { $PreProcessIDs -notcontains $_.Id })
$ResidualZero = $NewDisks.Count -eq 0 -and $NewLetters.Count -eq 0 -and $NewProcesses.Count -eq 0 -and -not (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) -and -not (Test-Path -LiteralPath $ReceiptPath) -and -not (Test-Path -LiteralPath $StoreRoot) -and -not (Test-Path -LiteralPath $WorkspaceRoot) -and -not (Test-Path -LiteralPath $SourceTestPlayRoot)
if (-not $ResidualZero -and $null -eq $Failure) { $Failure = 'Outer residual is nonzero.' }
$Passed = $null -eq $Failure -and $Uninstalled -and $ResidualZero
$Verdict = if ($WorkerCount -eq 2) { 'GNF_VHDX_DIFF_TWO_WORKERS_COMPATIBLE' } else { 'GNF_VHDX_DIFF_FOUR_WORKERS_COMPATIBLE' }
$Summary = [ordered]@{
    schemaVersion = 1; status = if ($Passed) { 'PASS' } else { 'FAILED' }; verdict = if ($Passed) { $Verdict } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o'); finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    repository = $RepositoryRoot; gnfProject = $GNFProjectPath; gnfRevision = $SourceBefore.revision; workerCount = $WorkerCount
    unityEditor = $UnityEditorPath; unityVersion = $ExpectedUnityVersion
    localPackage = [ordered]@{ path = $LocalPackagePath; revision = $PackageRevision; name = $PackageName }
    referenceArtifact = [ordered]@{ path = $ReferenceArtifactZip; sha256 = $ReferenceHash; verdict = $ReferenceSummary.verdict }
    selection = [ordered]@{ editMode = @($EditTest); playMode = @($PlayTest) }
    sourceBefore = $SourceBefore; sourceAfter = $SourceAfter; scenarios = $ScenarioResults; parallelEvidence = $ParallelEvidence; semanticParity = $Parity
    parent = $ParentEvidence; storageStatus = $StatusJSON; installed = $Installed; uninstalled = $Uninstalled; residualZero = $ResidualZero
    preFileBackedDisks = @($PreDisks); postFileBackedDisks = @($PostDisks); newFileBackedDisks = @($NewDisks)
    preDriveLetters = @($PreLetters); postDriveLetters = @($PostLetters); newDriveLetters = @($NewLetters)
    preProcesses = @($PreProcesses); postProcesses = @($PostProcesses); newProcesses = @($NewProcesses)
    failure = $Failure
    notMeasured = @('GNF four/eight workers', 'forced termination recovery', 'broker restart recovery', 'Windows reboot recovery', 'quota/LRU native behavior', 'performance superiority', 'production readiness', 'release readiness')
}
Write-JsonFile -LiteralPath $SummaryPath -Value $Summary
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash
Write-Output "VHDX_DIFF_GNF_PARALLEL_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_GNF_PARALLEL_VERDICT=$($Summary.verdict)"
Write-Output "VHDX_DIFF_GNF_PARALLEL_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_GNF_PARALLEL_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }

