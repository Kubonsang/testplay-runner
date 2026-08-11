[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$UnityEditorPath,

    [switch]$InstallApproved,

    [switch]$TerminationApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ExpectedTest = 'TestPlayFixture.Tests.LibraryMountTests.LibraryMountWriteReadTest'
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
    [IO.File]::WriteAllLines(
        $OutputPath,
        [string[]]@($Lines | ForEach-Object { $_.ToString() }),
        [Text.UTF8Encoding]::new($false)
    )
    return [pscustomobject]@{ ExitCode = $ExitCode; Lines = @($Lines) }
}

function Read-NativeJson {
    param([string]$LiteralPath)
    $Raw = [IO.File]::ReadAllText($LiteralPath)
    $Start = $Raw.IndexOf('{')
    if ($Start -lt 0) { throw "No JSON object in native output: $LiteralPath" }
    return $Raw.Substring($Start) | ConvertFrom-Json
}

function Get-FileBackedDisks {
    return @(
        Get-Disk -ErrorAction SilentlyContinue |
            Where-Object { $_.BusType -eq 'File Backed Virtual' } |
            Select-Object Number, FriendlyName, BusType, PartitionStyle
    )
}

function Get-DriveLetters {
    return @(
        Get-Volume -ErrorAction SilentlyContinue |
            Where-Object { $null -ne $_.DriveLetter } |
            Select-Object UniqueId, DriveLetter, FileSystem
    )
}

function Get-RelatedProcesses {
    return @(
        Get-Process -ErrorAction SilentlyContinue |
            Where-Object { $_.ProcessName -match '^(Unity|testplay|testplay-vhdx)' } |
            Select-Object Id, ProcessName, StartTime
    )
}

function Test-SamePath {
    param([string]$Left, [string]$Right)
    if ([string]::IsNullOrWhiteSpace($Left) -or [string]::IsNullOrWhiteSpace($Right)) { return $false }
    return [string]::Equals(
        [IO.Path]::GetFullPath($Left).TrimEnd('\'),
        [IO.Path]::GetFullPath($Right).TrimEnd('\'),
        [StringComparison]::OrdinalIgnoreCase
    )
}

function Get-ProcessIdentity {
    param([int]$ProcessID)
    $Process = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessID" -ErrorAction SilentlyContinue
    if ($null -eq $Process) { return $null }
    return [ordered]@{
        processId = [int]$Process.ProcessId
        parentProcessId = [int]$Process.ParentProcessId
        name = [string]$Process.Name
        executablePath = [string]$Process.ExecutablePath
        commandLine = [string]$Process.CommandLine
        creationDate = [string]$Process.CreationDate
    }
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

if (-not $InstallApproved) { throw 'Pass -InstallApproved after reviewing the unique store and ownership-safe cleanup contract.' }
if (-not $TerminationApproved) { throw 'Pass -TerminationApproved to terminate only the exact harness-owned client and Unity PIDs.' }
if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }
if (-not (Test-Path -LiteralPath $UnityEditorPath -PathType Leaf)) { throw "Unity Editor was not found: $UnityEditorPath" }

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$FixtureTemplate = Join-Path $RepositoryRoot 'testdata\unity-vhdx-fixture'
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-forced-termination-$Stamp"
$FixtureSource = Join-Path $ArtifactRoot 'fixture-source'
$StoreRoot = Join-Path $env:ProgramData "TestPlay\VHDXDiffForcedTermination-$Stamp"
$WorkspaceRoot = Join-Path $env:LOCALAPPDATA 'TestPlay\Workspaces'
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-forced-termination.exe'
$ConfigPath = Join-Path $ArtifactRoot 'testplay.json'
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$ZipPath = "$ArtifactRoot.zip"
$UserSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$UserStoreRoot = Join-Path $StoreRoot $UserSID
$LeaseRoot = Join-Path $UserStoreRoot 'leases'
$ChildRoot = Join-Path $UserStoreRoot 'children'

if (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) { throw 'TestPlayStorageBroker already exists; refusing to replace it.' }
foreach ($Path in @($ReceiptPath, $StoreRoot, $ArtifactRoot, $WorkspaceRoot)) {
    if (Test-Path -LiteralPath $Path) { throw "Pre-existing state is outside this harness ownership: $Path" }
}
$PreProcesses = @(Get-RelatedProcesses)
if ($PreProcesses.Count -ne 0) { throw 'Related Unity/testplay processes already exist; refusing an ambiguous termination test.' }

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
New-Item -ItemType Directory -Path $FixtureSource | Out-Null
foreach ($Name in @('Assets', 'Packages', 'ProjectSettings')) {
    Copy-Item -LiteralPath (Join-Path $FixtureTemplate $Name) -Destination $FixtureSource -Recurse
}
Write-JsonFile -LiteralPath $ConfigPath -Value ([ordered]@{
    schema_version = '1'
    unity_path = $UnityEditorPath
    project_path = $FixtureSource
    test_platform = 'edit_mode'
    timeout = [ordered]@{ total_ms = 900000 }
    result_dir = (Join-Path $ArtifactRoot 'results')
    workspace = [ordered]@{
        backend = 'vhdx-diff'
        store_root = $StoreRoot
        store_max_allocated_bytes = 34359738368
        minimum_host_free_bytes = 21474836480
    }
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
$StartedClient = $null
$PreviousMarkerWasSet = Test-Path Env:TESTPLAY_UNITY_FIXTURE_MARKER
$PreviousMarker = $env:TESTPLAY_UNITY_FIXTURE_MARKER

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

    $env:TESTPLAY_UNITY_FIXTURE_MARKER = "vhdx-diff-forced-termination-warmup-$Stamp"
    $WarmupRun = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('--config', $ConfigPath, 'run', '--filter', $ExpectedTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge') -OutputPath (Join-Path $ArtifactRoot 'warmup-run.txt') -WorkingDirectory $ArtifactRoot
    if ($WarmupRun.ExitCode -ne 0) { throw "warm-up fixture run failed: exit=$($WarmupRun.ExitCode)" }
    $Warmup = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'warmup-run.txt')
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'warmup-result.json') -Value $Warmup
    if ((Get-DirectoryEntryCount -LiteralPath $LeaseRoot) -ne 0 -or (Get-DirectoryEntryCount -LiteralPath $ChildRoot) -ne 0 -or (Get-DirectoryEntryCount -LiteralPath $WorkspaceRoot) -ne 0) {
        throw 'Warm-up did not release its lease, child, and workspace.'
    }

    $env:TESTPLAY_UNITY_FIXTURE_MARKER = "vhdx-diff-forced-termination-crash-$Stamp"
    $ClientStdout = Join-Path $ArtifactRoot 'crash-client-stdout.txt'
    $ClientStderr = Join-Path $ArtifactRoot 'crash-client-stderr.txt'
    $Arguments = @('--config', $ConfigPath, 'run', '--filter', $ExpectedTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge')
    $StartedClient = Start-Process -FilePath $ExecutablePath -ArgumentList $Arguments -WorkingDirectory $ArtifactRoot -NoNewWindow -PassThru -RedirectStandardOutput $ClientStdout -RedirectStandardError $ClientStderr

    $ReadyDeadline = (Get-Date).AddMinutes(3)
    $JournalPath = $null
    while ((Get-Date) -lt $ReadyDeadline) {
        if ($StartedClient.HasExited) { throw "Crash client exited before the termination gate: exit=$($StartedClient.ExitCode)" }
        $Candidates = @(Get-ChildItem -LiteralPath $LeaseRoot -Filter '*.json' -File -ErrorAction SilentlyContinue)
        if ($Candidates.Count -eq 1) {
            try {
                $Candidate = [IO.File]::ReadAllText($Candidates[0].FullName) | ConvertFrom-Json
                if ($Candidate.state -eq 'ready' -and [int]$Candidate.clientPid -eq $StartedClient.Id -and [int]$Candidate.unityPid -gt 0) {
                    $JournalPath = $Candidates[0].FullName
                    $CrashJournal = $Candidate
                    break
                }
            }
            catch { }
        }
        Start-Sleep -Milliseconds 50
    }
    if ($null -eq $CrashJournal) { throw 'Timed out waiting for one exact ready lease with client and Unity PIDs.' }
    if (-not (Test-SamePath -Left $CrashJournal.workspacePath -Right (Join-Path $WorkspaceRoot $CrashJournal.workspaceId))) { throw 'Journal workspace identity mismatch.' }
    if (-not (Test-SamePath -Left $CrashJournal.mountPath -Right (Join-Path $CrashJournal.workspacePath 'Library'))) { throw 'Journal mount identity mismatch.' }
    if (-not (Test-Path -LiteralPath $CrashJournal.childPath -PathType Leaf)) { throw 'Journal child VHDX is missing before termination.' }
    $MarkerPath = Join-Path $CrashJournal.workspacePath $WorkspaceOwnerFile
    if (-not (Test-Path -LiteralPath $MarkerPath -PathType Leaf)) { throw 'Broker workspace ownership marker is missing before termination.' }
    $CrashMarker = [IO.File]::ReadAllText($MarkerPath) | ConvertFrom-Json
    foreach ($Property in @('leaseId', 'runId', 'workspaceId', 'ownershipToken')) {
        if ([string]$CrashMarker.$Property -ne [string]$CrashJournal.$Property) { throw "Workspace marker mismatch: $Property" }
    }

    Copy-Item -LiteralPath $JournalPath -Destination (Join-Path $ArtifactRoot 'crash-lease-journal.json')
    Copy-Item -LiteralPath $MarkerPath -Destination (Join-Path $ArtifactRoot 'crash-workspace-owner.json')
    $JournalHash = (Get-FileHash -LiteralPath $JournalPath -Algorithm SHA256).Hash
    $MarkerHash = (Get-FileHash -LiteralPath $MarkerPath -Algorithm SHA256).Hash
    $ClientIdentity = Get-ProcessIdentity -ProcessID ([int]$CrashJournal.clientPid)
    $UnityIdentity = Get-ProcessIdentity -ProcessID ([int]$CrashJournal.unityPid)
    if ($null -eq $ClientIdentity -or -not (Test-SamePath -Left $ClientIdentity.executablePath -Right $ExecutablePath)) { throw 'Client process identity is not the exact harness executable.' }
    if ($null -eq $UnityIdentity -or -not (Test-SamePath -Left $UnityIdentity.executablePath -Right $UnityEditorPath)) { throw 'Unity process identity is not the configured Editor.' }
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'pre-termination.json') -Value ([ordered]@{
        journal = $CrashJournal
        workspaceOwner = $CrashMarker
        journalSha256 = $JournalHash
        workspaceOwnerSha256 = $MarkerHash
        client = $ClientIdentity
        unity = $UnityIdentity
        fileBackedDisks = @(Get-FileBackedDisks)
    })

    $CrashInitiated = $true
    $TerminationStarted = Get-Date
    Stop-Process -Id ([int]$CrashJournal.clientPid) -Force -ErrorAction Stop
    Stop-Process -Id ([int]$CrashJournal.unityPid) -Force -ErrorAction Stop
    $ClientStopped = Wait-ProcessAbsent -ProcessID ([int]$CrashJournal.clientPid)
    $UnityStopped = Wait-ProcessAbsent -ProcessID ([int]$CrashJournal.unityPid)
    if (-not $ClientStopped -or -not $UnityStopped) { throw 'Exact harness-owned processes did not terminate within the bound.' }
    $Termination = [ordered]@{
        approved = $true
        startedAt = $TerminationStarted.ToUniversalTime().ToString('o')
        finishedAt = (Get-Date).ToUniversalTime().ToString('o')
        clientPid = [int]$CrashJournal.clientPid
        unityPid = [int]$CrashJournal.unityPid
        clientStopped = $ClientStopped
        unityStopped = $UnityStopped
    }
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'termination.json') -Value $Termination

    $RecoveryDeadline = (Get-Date).AddMinutes(3)
    while ((Get-Date) -lt $RecoveryDeadline) {
        $LeaseGone = -not (Test-Path -LiteralPath $JournalPath)
        $ChildGone = -not (Test-Path -LiteralPath $CrashJournal.childPath)
        $WorkspaceGone = -not (Test-Path -LiteralPath $CrashJournal.workspacePath)
        $PreDiskIDs = @($PreDisks | ForEach-Object { $_.Number })
        $NewRecoveryDisks = @(Get-FileBackedDisks | Where-Object { $PreDiskIDs -notcontains $_.Number })
        if ($LeaseGone -and $ChildGone -and $WorkspaceGone -and $NewRecoveryDisks.Count -eq 0) {
            $RecoveryVerified = $true
            break
        }
        Start-Sleep -Seconds 1
    }
    if (-not $RecoveryVerified) { throw 'Broker did not release the exact orphan lease, child, workspace, and attached disk within the recovery bound.' }

    $Status = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'recovered-storage-status.json') -WorkingDirectory $ArtifactRoot
    if ($Status.ExitCode -ne 0) { throw "storage status after recovery failed: exit=$($Status.ExitCode)" }
    $RecoveredStatus = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'recovered-storage-status.json')
    foreach ($Property in @('activeChildCount', 'retainedChildCount', 'pendingCount', 'quarantineCount')) {
        if ([int]$RecoveredStatus.$Property -ne 0) { throw "Recovered storage status is nonzero: $Property=$($RecoveredStatus.$Property)" }
    }
    if ([bool]$RecoveredStatus.manualRecoveryRequired) { throw 'Recovered storage status requires manual recovery.' }
    $CleanupState = 'recovered'
}
catch {
    $Failure = $_.Exception.ToString()
    if ($CrashInitiated -and -not $RecoveryVerified) { $CleanupState = 'preserved' }
    else { $CleanupState = 'failed-before-crash' }
}
finally {
    if ($Installed -and (-not $CrashInitiated -or $RecoveryVerified)) {
        try {
            $Uninstall = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'uninstall') -OutputPath (Join-Path $ArtifactRoot 'storage-uninstall.txt') -WorkingDirectory $ArtifactRoot
            $Uninstalled = $Uninstall.ExitCode -eq 0
            if (-not $Uninstalled -and $null -eq $Failure) { $Failure = "storage uninstall failed: exit=$($Uninstall.ExitCode)" }
            if ($Uninstalled) { $CleanupState = 'released' }
        }
        catch { if ($null -eq $Failure) { $Failure = $_.Exception.ToString() } }
    }
    if ($PreviousMarkerWasSet) { $env:TESTPLAY_UNITY_FIXTURE_MARKER = $PreviousMarker }
    else { Remove-Item Env:TESTPLAY_UNITY_FIXTURE_MARKER -ErrorAction SilentlyContinue }
    Stop-Transcript | Out-Null
}

$PostDisks = @(Get-FileBackedDisks)
$PostLetters = @(Get-DriveLetters)
$PostProcesses = @(Get-RelatedProcesses)
$PreDiskIDs = @($PreDisks | ForEach-Object { $_.Number })
$PreLetterIDs = @($PreLetters | ForEach-Object { $_.UniqueId })
$PreProcessIDs = @($PreProcesses | ForEach-Object { $_.Id })
$NewDisks = @($PostDisks | Where-Object { $PreDiskIDs -notcontains $_.Number })
$NewLetters = @($PostLetters | Where-Object { $PreLetterIDs -notcontains $_.UniqueId })
$NewProcesses = @($PostProcesses | Where-Object { $PreProcessIDs -notcontains $_.Id })
$ResidualZero = (
    $NewDisks.Count -eq 0 -and
    $NewLetters.Count -eq 0 -and
    $NewProcesses.Count -eq 0 -and
    -not (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) -and
    -not (Test-Path -LiteralPath $ReceiptPath) -and
    -not (Test-Path -LiteralPath $StoreRoot) -and
    -not (Test-Path -LiteralPath $WorkspaceRoot)
)
if ($RecoveryVerified -and $Uninstalled -and -not $ResidualZero -and $null -eq $Failure) { $Failure = 'Final outer residual is nonzero.' }
$Passed = $null -eq $Failure -and $CrashInitiated -and $RecoveryVerified -and $Uninstalled -and $ResidualZero
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { 'VHDX_DIFF_FIXTURE_FORCED_TERMINATION_RECOVERY_PASS' } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    repository = $RepositoryRoot
    unityEditor = $UnityEditorPath
    fixtureSource = $FixtureSource
    selectedTest = $ExpectedTest
    storeRoot = $StoreRoot
    workspaceRoot = $WorkspaceRoot
    warmup = $Warmup
    crashJournal = $CrashJournal
    crashWorkspaceOwner = $CrashMarker
    clientProcess = $ClientIdentity
    unityProcess = $UnityIdentity
    termination = $Termination
    recoveryVerified = $RecoveryVerified
    recoveredStorageStatus = $RecoveredStatus
    installed = $Installed
    uninstalled = $Uninstalled
    cleanupState = $CleanupState
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
    notMeasured = @(
        'broker process forced termination recovery',
        'Windows reboot recovery',
        'quota/LRU native behavior',
        'eight-worker compatibility',
        'performance superiority',
        'production readiness',
        'release readiness'
    )
}
Write-JsonFile -LiteralPath $SummaryPath -Value $Summary
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash
Write-Output "VHDX_DIFF_FORCED_TERMINATION_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_FORCED_TERMINATION_VERDICT=$($Summary.verdict)"
Write-Output "VHDX_DIFF_FORCED_TERMINATION_CLEANUP=$CleanupState"
Write-Output "VHDX_DIFF_FORCED_TERMINATION_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_FORCED_TERMINATION_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
