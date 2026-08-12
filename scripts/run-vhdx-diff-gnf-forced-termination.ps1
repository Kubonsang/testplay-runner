[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)] [string]$UnityEditorPath,
    [Parameter(Mandatory = $true)] [string]$GNFProjectPath,
    [Parameter(Mandatory = $true)] [string]$LocalPackagePath,
    [Parameter(Mandatory = $true)] [string]$ReferenceArtifactZip,
    [Parameter(Mandatory = $true)] [string]$ReferenceArtifactSHA256,
    [switch]$InstallApproved,
    [switch]$TerminationApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'vhdx-diff-gnf-forced-termination-evidence.ps1')

$ExpectedGNFRevision = '19a17074f6366038cd5b17c01e0a904f0d585470'
$ExpectedPackageRevision = '149896faeb3b5165a3af4739342c637ed66d94b6'
$ExpectedUnityVersion = '6000.3.8f1'
$PackageName = 'com.youngwoocho02.unity-cli-connector'
$ExpectedTest = 'GNF.DungeonGen.Tests.WallPropValidatorTests.NullPrefab_Error'
$WorkspaceOwnerFile = '.testplay-vhdx-workspace-owner.json'

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
    param([string]$Root, [switch]$ExcludeGitMetadata)
    $Builder = [Text.StringBuilder]::new()
    $GitRoot = Join-Path $Root '.git'
    $Files = @(
        Get-ChildItem -LiteralPath $Root -Recurse -File -Force |
            Where-Object { -not $ExcludeGitMetadata -or -not $_.FullName.StartsWith($GitRoot + '\', [StringComparison]::OrdinalIgnoreCase) } |
            Sort-Object FullName
    )
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
    return [pscustomobject]@{ digest = $Digest; fileCount = $Files.Count; logicalBytes = $Bytes }
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

function Test-SourceEqual {
    param([object]$Before, [object]$After)
    foreach ($Name in @('revision', 'branch', 'status', 'packagesLockSHA256')) {
        if ($Before.$Name -ne $After.$Name) { return $false }
    }
    foreach ($Name in @('assets', 'packages', 'projectSettings')) {
        if ($Before.$Name.digest -ne $After.$Name.digest -or $Before.$Name.fileCount -ne $After.$Name.fileCount -or $Before.$Name.logicalBytes -ne $After.$Name.logicalBytes) { return $false }
    }
    return $true
}

function Get-PackageEvidence {
    param([string]$Root)
    return [ordered]@{
        revision = Get-GitText -Repository $Root -Arguments @('rev-parse', 'HEAD')
        status = Get-GitText -Repository $Root -Arguments @('status', '--porcelain=v1', '--untracked-files=all')
        tree = Get-TreeDigest -Root $Root -ExcludeGitMetadata
    }
}

function Test-PackageEqual {
    param([object]$Before, [object]$After)
    return $Before.revision -eq $After.revision -and $Before.status -eq $After.status -and $Before.tree.digest -eq $After.tree.digest -and $Before.tree.fileCount -eq $After.tree.fileCount -and $Before.tree.logicalBytes -eq $After.tree.logicalBytes
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

function Test-SamePath {
    param([string]$Left, [string]$Right)
    if ([string]::IsNullOrWhiteSpace($Left) -or [string]::IsNullOrWhiteSpace($Right)) { return $false }
    return [string]::Equals([IO.Path]::GetFullPath($Left).TrimEnd('\'), [IO.Path]::GetFullPath($Right).TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)
}

function Get-ProcessIdentity {
    param([int]$ProcessID)
    $Process = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessID" -ErrorAction SilentlyContinue
    if ($null -eq $Process) { return $null }
    return [ordered]@{ processId = [int]$Process.ProcessId; parentProcessId = [int]$Process.ParentProcessId; name = [string]$Process.Name; executablePath = [string]$Process.ExecutablePath; commandLine = [string]$Process.CommandLine; creationDate = [string]$Process.CreationDate }
}

function Wait-ProcessAbsent {
    param([int]$ProcessID, [int]$TimeoutSeconds = 15)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $Deadline) {
        if ($null -eq (Get-Process -Id $ProcessID -ErrorAction SilentlyContinue)) { return $true }
        Start-Sleep -Milliseconds 100
    }
    return $false
}

function Get-DirectoryEntryCount {
    param([string]$LiteralPath)
    if (-not (Test-Path -LiteralPath $LiteralPath -PathType Container)) { return 0 }
    return @(Get-ChildItem -LiteralPath $LiteralPath -Force -ErrorAction Stop).Count
}

function Copy-OwnedRunArtifacts {
    param([string]$ProjectRoot, [string]$RunID, [string]$Destination)
    $Source = Join-Path $ProjectRoot ".testplay\runs\$RunID"
    if (Test-Path -LiteralPath $Source -PathType Container) { Copy-Item -LiteralPath $Source -Destination $Destination -Recurse }
}

function Remove-OwnedRunArtifacts {
    param([string]$ProjectRoot, [string[]]$RunIDs)
    foreach ($RunID in @($RunIDs)) {
        $Owned = Join-Path $ProjectRoot ".testplay\runs\$RunID"
        if (Test-Path -LiteralPath $Owned) { Remove-Item -LiteralPath $Owned -Recurse -Force }
    }
    $Runs = Join-Path $ProjectRoot '.testplay\runs'
    if ((Test-Path -LiteralPath $Runs) -and @(Get-ChildItem -LiteralPath $Runs -Force).Count -eq 0) { Remove-Item -LiteralPath $Runs -Force }
    $TestPlay = Join-Path $ProjectRoot '.testplay'
    if ((Test-Path -LiteralPath $TestPlay) -and @(Get-ChildItem -LiteralPath $TestPlay -Force).Count -eq 0) { Remove-Item -LiteralPath $TestPlay -Force }
}

function Get-ParentEvidence {
    param([string]$ParentPath, [string]$ExpectedKey)
    $MetadataPath = Join-Path (Split-Path -Parent $ParentPath) 'metadata.json'
    $Metadata = [IO.File]::ReadAllText($MetadataPath) | ConvertFrom-Json
    $File = Get-Item -LiteralPath $ParentPath -Force
    $Hash = (Get-FileHash -LiteralPath $ParentPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($Metadata.compatibilityKey.digest -ne $ExpectedKey -or $Hash -ne ([string]$Metadata.committedSha256).ToLowerInvariant() -or -not $File.IsReadOnly) { throw 'Immutable parent evidence is invalid.' }
    return [ordered]@{ key = $ExpectedKey; path = $ParentPath; sha256 = $Hash; length = $File.Length; lastWriteTimeUtc = $File.LastWriteTimeUtc.ToString('o'); readOnly = $File.IsReadOnly; metadata = $Metadata }
}

if (-not $InstallApproved) { throw 'Pass -InstallApproved after reviewing the unique store and cleanup contract.' }
if (-not $TerminationApproved) { throw 'Pass -TerminationApproved to terminate only the exact harness-owned client and Unity PIDs.' }
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
if ($ReferenceSummary.status -ne 'PASS' -or $ReferenceSummary.verdict -ne 'GNF_NTFS_REFERENCE_STABLE' -or @($ReferenceSelection.editMode).Count -ne 1 -or $ReferenceSelection.editMode[0] -ne $ExpectedTest) { throw 'Pinned NTFS reference evidence/selection is invalid.' }

$ProjectVersion = (Select-String -LiteralPath (Join-Path $GNFProjectPath 'ProjectSettings\ProjectVersion.txt') -Pattern '^m_EditorVersion:\s*(.+)$').Matches[0].Groups[1].Value.Trim()
if ($ProjectVersion -ne $ExpectedUnityVersion -or $UnityEditorPath -notmatch [regex]::Escape("\$ExpectedUnityVersion\")) { throw "Unity version mismatch: project=$ProjectVersion editor=$UnityEditorPath" }
$SourceBefore = Get-SourceEvidence -Root $GNFProjectPath
$PackageBefore = Get-PackageEvidence -Root $LocalPackagePath
if ($SourceBefore.revision -ne $ExpectedGNFRevision -or $SourceBefore.status -ne '') { throw "GNF source is not the exact clean revision: revision=$($SourceBefore.revision) status=$($SourceBefore.status)" }
if ($PackageBefore.revision -ne $ExpectedPackageRevision -or $PackageBefore.status -ne '') { throw "Portable package is not the exact clean revision: revision=$($PackageBefore.revision) status=$($PackageBefore.status)" }

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-gnf-forced-termination-$Stamp"
$StoreRoot = Join-Path $env:ProgramData "TestPlay\VHDXDiffGNFForcedTermination-$Stamp"
$WorkspaceRoot = Join-Path $env:LOCALAPPDATA 'TestPlay\Workspaces'
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-gnf-forced-termination.exe'
$ConfigPath = Join-Path $ArtifactRoot 'testplay.json'
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$ZipPath = "$ArtifactRoot.zip"
$UserSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$UserStoreRoot = Join-Path $StoreRoot $UserSID
$LeaseRoot = Join-Path $UserStoreRoot 'leases'
$ChildRoot = Join-Path $UserStoreRoot 'children'

if (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) { throw 'TestPlayStorageBroker already exists; refusing replacement.' }
foreach ($Path in @($ReceiptPath, $StoreRoot, $ArtifactRoot, $WorkspaceRoot, (Join-Path $GNFProjectPath '.testplay'))) { if (Test-Path -LiteralPath $Path) { throw "Pre-existing state is outside this harness ownership: $Path" } }
$PreProcesses = @(Get-RelatedProcesses)
if ($PreProcesses.Count -ne 0) { throw 'Related Unity/testplay processes already exist; refusing ambiguous termination.' }

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
$Overrides = [ordered]@{}; $Overrides[$PackageName] = $LocalPackagePath
Write-JsonFile -LiteralPath $ConfigPath -Value ([ordered]@{
    schema_version = '1'; unity_path = $UnityEditorPath; project_path = $GNFProjectPath; test_platform = 'edit_mode'; timeout = [ordered]@{ total_ms = 1800000 }; result_dir = (Join-Path $ArtifactRoot 'results')
    workspace = [ordered]@{ backend = 'vhdx-diff'; store_root = $StoreRoot; store_max_allocated_bytes = 34359738368; minimum_host_free_bytes = 21474836480; local_package_overrides = $Overrides }
})

$PreDisks = @(Get-FileBackedDisks)
$PreLetters = @(Get-DriveLetters)
$Started = Get-Date
$Installed = $false
$Uninstalled = $false
$CrashInitiated = $false
$RecoveryVerified = $false
$CleanupState = 'not-started'
$Failure = $null
$Warmup = $null
$CrashJournal = $null
$CrashMarker = $null
$ClientIdentity = $null
$UnityIdentity = $null
$Termination = $null
$RecoveredStatus = $null
$ParentBefore = $null
$ParentAfter = $null
$SourceAfter = $null
$PackageAfter = $null
$SourceUnchanged = $false
$PackageUnchanged = $false
$ParentUnchanged = $false
$StartedClient = $null
$OwnedRunIDs = @()

Start-Transcript -Path (Join-Path $ArtifactRoot 'terminal-transcript.txt') -Force | Out-Null
try {
    Push-Location $RepositoryRoot
    try {
        & go build -buildvcs=false -o $ExecutablePath .\cmd\testplay
        if ($LASTEXITCODE -ne 0) { throw "go build failed: exit=$LASTEXITCODE" }
    }
    finally { Pop-Location }

    $Install = Invoke-NativeCapture $ExecutablePath @('storage', 'install', '--root', $StoreRoot) (Join-Path $ArtifactRoot 'storage-install.txt') $ArtifactRoot
    if ($Install.ExitCode -ne 0) { throw "storage install failed: exit=$($Install.ExitCode)" }
    $Installed = $true

    $WarmupRun = Invoke-NativeCapture $ExecutablePath @('--config', $ConfigPath, 'run', '--filter', $ExpectedTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge') (Join-Path $ArtifactRoot 'warmup-run.txt') $ArtifactRoot
    if ($WarmupRun.ExitCode -ne 0) { throw "GNF warm-up failed: exit=$($WarmupRun.ExitCode)" }
    $Warmup = Read-NativeJson (Join-Path $ArtifactRoot 'warmup-run.txt')
    if ($Warmup.total -ne 1 -or $Warmup.passed -ne 1 -or $Warmup.failed -ne 0 -or @($Warmup.errors).Count -ne 0 -or @($Warmup.tests).Count -ne 1 -or $Warmup.tests[0].name -ne $ExpectedTest -or $Warmup.tests[0].result -ne 'Passed') { throw 'GNF warm-up result is not the frozen passing test.' }
    if ($Warmup.workspace_metrics.provider -ne 'vhdx-differencing' -or $Warmup.workspace_metrics.fallbackUsed -or $Warmup.workspace_metrics.cleanupState -ne 'released') { throw 'GNF warm-up workspace evidence is invalid.' }
    $OwnedRunIDs += [string]$Warmup.run_id
    Copy-OwnedRunArtifacts $GNFProjectPath ([string]$Warmup.run_id) (Join-Path $ArtifactRoot 'warmup-run-artifacts')
    Remove-OwnedRunArtifacts $GNFProjectPath @([string]$Warmup.run_id)
    if ((Get-DirectoryEntryCount $LeaseRoot) -ne 0 -or (Get-DirectoryEntryCount $ChildRoot) -ne 0 -or (Get-DirectoryEntryCount $WorkspaceRoot) -ne 0) { throw 'GNF warm-up did not release lease, child, and workspace.' }
    $ParentBefore = Get-ParentEvidence ([string]$Warmup.workspace_metrics.parentPath) ([string]$Warmup.workspace_metrics.parentKey)

    $ClientStdout = Join-Path $ArtifactRoot 'crash-client-stdout.txt'
    $ClientStderr = Join-Path $ArtifactRoot 'crash-client-stderr.txt'
    $Arguments = @('--config', $ConfigPath, 'run', '--filter', $ExpectedTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge')
    $StartedClient = Start-Process -FilePath $ExecutablePath -ArgumentList $Arguments -WorkingDirectory $ArtifactRoot -NoNewWindow -PassThru -RedirectStandardOutput $ClientStdout -RedirectStandardError $ClientStderr

    $ReadyDeadline = (Get-Date).AddMinutes(10)
    $JournalPath = $null
    while ((Get-Date) -lt $ReadyDeadline) {
        if ($StartedClient.HasExited) { throw "Crash client exited before termination gate: exit=$($StartedClient.ExitCode)" }
        $Candidates = @(Get-ChildItem -LiteralPath $LeaseRoot -Filter '*.json' -File -ErrorAction SilentlyContinue)
        if ($Candidates.Count -eq 1) {
            try {
                $Candidate = [IO.File]::ReadAllText($Candidates[0].FullName) | ConvertFrom-Json
                if ($Candidate.state -eq 'ready' -and [int]$Candidate.clientPid -eq $StartedClient.Id -and [int]$Candidate.unityPid -gt 0) { $JournalPath = $Candidates[0].FullName; $CrashJournal = $Candidate; break }
            }
            catch { }
        }
        Start-Sleep -Milliseconds 100
    }
    if ($null -eq $CrashJournal) { throw 'Timed out waiting for one exact ready GNF lease with client and Unity PIDs.' }
    $OwnedRunIDs += [string]$CrashJournal.runId
    if (-not (Test-SamePath $CrashJournal.workspacePath (Join-Path $WorkspaceRoot $CrashJournal.workspaceId)) -or -not (Test-SamePath $CrashJournal.mountPath (Join-Path $CrashJournal.workspacePath 'Library'))) { throw 'Crash journal workspace/mount identity mismatch.' }
    if (-not (Test-Path -LiteralPath $CrashJournal.childPath -PathType Leaf) -or $CrashJournal.parentKey -ne $ParentBefore.key -or -not (Test-SamePath $CrashJournal.parentPath $ParentBefore.path)) { throw 'Crash journal child/parent identity mismatch.' }
    $MarkerPath = Join-Path $CrashJournal.workspacePath $WorkspaceOwnerFile
    if (-not (Test-Path -LiteralPath $MarkerPath -PathType Leaf)) { throw 'Crash workspace owner marker is missing.' }
    $CrashMarker = [IO.File]::ReadAllText($MarkerPath) | ConvertFrom-Json
    foreach ($Property in @('leaseId', 'runId', 'workspaceId', 'ownershipToken')) { if ([string]$CrashMarker.$Property -ne [string]$CrashJournal.$Property) { throw "Crash marker mismatch: $Property" } }

    Copy-Item -LiteralPath $JournalPath -Destination (Join-Path $ArtifactRoot 'crash-lease-journal.json')
    Copy-Item -LiteralPath $MarkerPath -Destination (Join-Path $ArtifactRoot 'crash-workspace-owner.json')
    $ClientIdentity = Get-ProcessIdentity ([int]$CrashJournal.clientPid)
    $UnityIdentity = Get-ProcessIdentity ([int]$CrashJournal.unityPid)
    if ($null -eq $ClientIdentity -or -not (Test-SamePath $ClientIdentity.executablePath $ExecutablePath)) { throw 'Client process is not the exact harness executable.' }
    if ($null -eq $UnityIdentity -or -not (Test-SamePath $UnityIdentity.executablePath $UnityEditorPath)) { throw 'Unity process is not the configured Editor.' }
    Write-JsonFile (Join-Path $ArtifactRoot 'pre-termination.json') ([ordered]@{ journal = $CrashJournal; workspaceOwner = $CrashMarker; journalSha256 = (Get-FileHash $JournalPath -Algorithm SHA256).Hash; workspaceOwnerSha256 = (Get-FileHash $MarkerPath -Algorithm SHA256).Hash; client = $ClientIdentity; unity = $UnityIdentity; parent = $ParentBefore; fileBackedDisks = @(Get-FileBackedDisks) })

    $CrashInitiated = $true
    $TerminationStarted = Get-Date
    Stop-Process -Id ([int]$CrashJournal.clientPid) -Force -ErrorAction Stop
    $UnityAtTermination = Get-Process -Id ([int]$CrashJournal.unityPid) -ErrorAction SilentlyContinue
    if ($null -ne $UnityAtTermination) {
        Stop-Process -Id ([int]$CrashJournal.unityPid) -Force -ErrorAction Stop
    }
    $ClientStopped = Wait-ProcessAbsent ([int]$CrashJournal.clientPid)
    $UnityStopped = Wait-ProcessAbsent ([int]$CrashJournal.unityPid)
    if (-not $ClientStopped -or -not $UnityStopped) { throw 'Exact GNF client/Unity processes did not terminate within the bound.' }
    $Termination = [ordered]@{ approved = $true; startedAt = $TerminationStarted.ToUniversalTime().ToString('o'); finishedAt = (Get-Date).ToUniversalTime().ToString('o'); clientPid = [int]$CrashJournal.clientPid; unityPid = [int]$CrashJournal.unityPid; clientStopped = $ClientStopped; unityStopped = $UnityStopped }
    Write-JsonFile (Join-Path $ArtifactRoot 'termination.json') $Termination

    $RecoveryDeadline = (Get-Date).AddMinutes(5)
    while ((Get-Date) -lt $RecoveryDeadline) {
        $PreDiskIDs = @($PreDisks | ForEach-Object { $_.Number })
        $NewRecoveryDisks = @(Get-FileBackedDisks | Where-Object { $PreDiskIDs -notcontains $_.Number })
        if (-not (Test-Path -LiteralPath $JournalPath) -and -not (Test-Path -LiteralPath $CrashJournal.childPath) -and -not (Test-Path -LiteralPath $CrashJournal.workspacePath) -and $NewRecoveryDisks.Count -eq 0) { $RecoveryVerified = $true; break }
        Start-Sleep -Seconds 1
    }
    if (-not $RecoveryVerified) { throw 'Broker did not release exact orphan GNF lease, child, workspace, and disk.' }

    $Status = Invoke-NativeCapture $ExecutablePath @('storage', 'status', '--json') (Join-Path $ArtifactRoot 'recovered-storage-status.json') $ArtifactRoot
    if ($Status.ExitCode -ne 0) { throw "Recovered storage status failed: exit=$($Status.ExitCode)" }
    $RecoveredStatus = Read-NativeJson (Join-Path $ArtifactRoot 'recovered-storage-status.json')
    foreach ($Property in @('activeChildCount', 'retainedChildCount', 'pendingCount', 'quarantineCount')) { if ([int]$RecoveredStatus.$Property -ne 0) { throw "Recovered storage is nonzero: $Property=$($RecoveredStatus.$Property)" } }
    if ([bool]$RecoveredStatus.manualRecoveryRequired -or [int]$RecoveredStatus.parentCount -ne 1) { throw 'Recovered storage parent/manual-recovery evidence is invalid.' }

    Copy-OwnedRunArtifacts $GNFProjectPath ([string]$CrashJournal.runId) (Join-Path $ArtifactRoot 'crash-run-artifacts')
    Remove-OwnedRunArtifacts $GNFProjectPath @([string]$CrashJournal.runId)
    $ParentAfter = Get-ParentEvidence $ParentBefore.path $ParentBefore.key
    $ParentUnchanged = $ParentBefore.sha256 -eq $ParentAfter.sha256 -and $ParentBefore.length -eq $ParentAfter.length -and $ParentBefore.lastWriteTimeUtc -eq $ParentAfter.lastWriteTimeUtc
    if (-not $ParentUnchanged) { throw 'Immutable GNF parent changed across forced termination.' }
    $SourceAfter = Get-SourceEvidence $GNFProjectPath
    $PackageAfter = Get-PackageEvidence $LocalPackagePath
    $SourceUnchanged = Test-SourceEqual $SourceBefore $SourceAfter
    $PackageUnchanged = Test-PackageEqual $PackageBefore $PackageAfter
    if (-not $SourceUnchanged -or -not $PackageUnchanged) { throw 'GNF source or portable package changed.' }
    $CleanupState = 'recovered'
}
catch {
    $Failure = $_.Exception.ToString()
    if ($null -ne $StartedClient -and -not $StartedClient.HasExited -and -not $CrashInitiated) { $CleanupState = 'preserved' }
    elseif ($CrashInitiated -and -not $RecoveryVerified) { $CleanupState = 'preserved' }
    else { $CleanupState = 'failed-before-crash' }
}
finally {
    if ($Installed -and (-not $CrashInitiated -or $RecoveryVerified) -and $CleanupState -ne 'preserved') {
        try {
            $Uninstall = Invoke-NativeCapture $ExecutablePath @('storage', 'uninstall') (Join-Path $ArtifactRoot 'storage-uninstall.txt') $ArtifactRoot
            $Uninstalled = $Uninstall.ExitCode -eq 0
            if (-not $Uninstalled -and $null -eq $Failure) { $Failure = "storage uninstall failed: exit=$($Uninstall.ExitCode)" }
            if ($Uninstalled) { $CleanupState = 'released' }
        }
        catch { if ($null -eq $Failure) { $Failure = $_.Exception.ToString() } }
    }
    if ($CleanupState -ne 'preserved') {
        try { Remove-OwnedRunArtifacts $GNFProjectPath $OwnedRunIDs }
        catch { if ($null -eq $Failure) { $Failure = $_.Exception.ToString() } }
    }
    Stop-Transcript | Out-Null
}

if ($null -eq $SourceAfter) { $SourceAfter = Get-SourceEvidence $GNFProjectPath; $SourceUnchanged = Test-SourceEqual $SourceBefore $SourceAfter }
if ($null -eq $PackageAfter) { $PackageAfter = Get-PackageEvidence $LocalPackagePath; $PackageUnchanged = Test-PackageEqual $PackageBefore $PackageAfter }
$PostDisks = @(Get-FileBackedDisks)
$PostLetters = @(Get-DriveLetters)
$PostProcesses = @(Get-RelatedProcesses)
$PreDiskIDs = @($PreDisks | ForEach-Object { $_.Number })
$PreLetterIDs = @($PreLetters | ForEach-Object { $_.UniqueId })
$PreProcessIDs = @($PreProcesses | ForEach-Object { $_.Id })
$NewDisks = @($PostDisks | Where-Object { $PreDiskIDs -notcontains $_.Number })
$NewLetters = @($PostLetters | Where-Object { $PreLetterIDs -notcontains $_.UniqueId })
$NewProcesses = @($PostProcesses | Where-Object { $PreProcessIDs -notcontains $_.Id })
$ResidualZero = $NewDisks.Count -eq 0 -and $NewLetters.Count -eq 0 -and $NewProcesses.Count -eq 0 -and -not (Get-Service TestPlayStorageBroker -ErrorAction SilentlyContinue) -and -not (Test-Path $ReceiptPath) -and -not (Test-Path $StoreRoot) -and -not (Test-Path $WorkspaceRoot) -and -not (Test-Path (Join-Path $GNFProjectPath '.testplay'))
if ($RecoveryVerified -and $Uninstalled -and -not $ResidualZero -and $null -eq $Failure) { $Failure = 'Final GNF forced-termination residual is nonzero.'; $CleanupState = 'uncertain' }
$Passed = $null -eq $Failure -and $CrashInitiated -and $RecoveryVerified -and $ParentUnchanged -and $SourceUnchanged -and $PackageUnchanged -and $Uninstalled -and $ResidualZero
$Summary = [ordered]@{
    schemaVersion = 1; status = if ($Passed) { 'PASS' } else { 'FAILED' }; verdict = if ($Passed) { 'GNF_VHDX_DIFF_FORCED_TERMINATION_RECOVERY_PASS' } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o'); finishedAt = (Get-Date).ToUniversalTime().ToString('o'); repository = $RepositoryRoot
    gnfProject = $GNFProjectPath; gnfRevision = $SourceBefore.revision; unityEditor = $UnityEditorPath; unityVersion = $ExpectedUnityVersion
    localPackage = [ordered]@{ path = $LocalPackagePath; revision = $PackageBefore.revision; name = $PackageName }
    referenceArtifact = [ordered]@{ path = $ReferenceArtifactZip; sha256 = $ReferenceHash; verdict = $ReferenceSummary.verdict }; selectedTest = $ExpectedTest
    sourceBefore = $SourceBefore; sourceAfter = $SourceAfter; sourceUnchanged = $SourceUnchanged
    localPackageBefore = $PackageBefore; localPackageAfter = $PackageAfter; localPackageUnchanged = $PackageUnchanged
    warmup = $Warmup; parentBefore = $ParentBefore; parentAfter = $ParentAfter; parentUnchanged = $ParentUnchanged
    crashJournal = $CrashJournal; crashWorkspaceOwner = $CrashMarker; clientProcess = $ClientIdentity; unityProcess = $UnityIdentity; termination = $Termination
    recoveryVerified = $RecoveryVerified; recoveredStorageStatus = $RecoveredStatus; installed = $Installed; uninstalled = $Uninstalled; cleanupState = $CleanupState; residualZero = $ResidualZero
    preFileBackedDisks = @($PreDisks); postFileBackedDisks = @($PostDisks); newFileBackedDisks = @($NewDisks)
    preDriveLetters = @($PreLetters); postDriveLetters = @($PostLetters); newDriveLetters = @($NewLetters)
    preProcesses = @($PreProcesses); postProcesses = @($PostProcesses); newProcesses = @($NewProcesses)
    failure = $Failure; notMeasured = @('GNF eight-worker compatibility', 'long-running child growth', 'performance superiority', 'production readiness', 'release readiness')
}
Write-JsonFile $SummaryPath $Summary
if ($Passed) { Assert-VHDXDiffGNFForcedTerminationEvidence $Summary }
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash
Write-Output "VHDX_DIFF_GNF_FORCED_TERMINATION_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_GNF_FORCED_TERMINATION_VERDICT=$($Summary.verdict)"
Write-Output "VHDX_DIFF_GNF_FORCED_TERMINATION_CLEANUP=$CleanupState"
Write-Output "VHDX_DIFF_GNF_FORCED_TERMINATION_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_GNF_FORCED_TERMINATION_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
