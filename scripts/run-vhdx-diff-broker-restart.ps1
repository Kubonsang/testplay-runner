[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$UnityEditorPath,

    [switch]$InstallApproved,

    [switch]$BrokerTerminationApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ServiceName = 'TestPlayStorageBroker'
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
    return [ordered]@{
        processId = [int]$Process.ProcessId
        parentProcessId = [int]$Process.ParentProcessId
        name = [string]$Process.Name
        executablePath = [string]$Process.ExecutablePath
        commandLine = [string]$Process.CommandLine
        creationDate = [string]$Process.CreationDate
    }
}

function Get-ServiceEvidence {
    $Service = Get-CimInstance Win32_Service -Filter "Name = '$ServiceName'" -ErrorAction SilentlyContinue
    if ($null -eq $Service) { return $null }
    return [ordered]@{
        name = [string]$Service.Name
        state = [string]$Service.State
        status = [string]$Service.Status
        processId = [int]$Service.ProcessId
        startMode = [string]$Service.StartMode
        pathName = [string]$Service.PathName
    }
}

function Wait-ServiceState {
    param([string]$State, [int]$TimeoutSeconds = 30)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $Deadline) {
        $Evidence = Get-ServiceEvidence
        if ($null -ne $Evidence -and $Evidence.state -eq $State) { return $Evidence }
        Start-Sleep -Milliseconds 200
    }
    return $null
}

function Wait-ProcessAbsent {
    param([int]$ProcessID, [int]$TimeoutSeconds = 90)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $Deadline) {
        if ($null -eq (Get-Process -Id $ProcessID -ErrorAction SilentlyContinue)) { return $true }
        Start-Sleep -Milliseconds 200
    }
    return $false
}

function Get-DirectoryEntryCount {
    param([string]$LiteralPath)
    if (-not (Test-Path -LiteralPath $LiteralPath -PathType Container)) { return 0 }
    return @(Get-ChildItem -LiteralPath $LiteralPath -Force -ErrorAction Stop).Count
}

function Get-StaleMountEvidence {
    param([object]$Journal)
    $MountItem = Get-Item -LiteralPath $Journal.mountPath -Force -ErrorAction Stop
    $Images = @(Get-DiskImage -ImagePath $Journal.childPath -ErrorAction Stop)
    if ($Images.Count -ne 1) { throw "Expected one exact child disk image; found $($Images.Count)" }
    return [ordered]@{
        mountPath = [string]$Journal.mountPath
        expectedVolumeGuid = [string]$Journal.volumeGuid
        attributes = [string]$MountItem.Attributes
        reparsePoint = (($MountItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)
        linkType = [string]$MountItem.LinkType
        target = @($MountItem.Target)
        childPath = [string]$Journal.childPath
        childImagePath = [string]$Images[0].ImagePath
        childAttached = [bool]$Images[0].Attached
        fileBackedDisks = @(Get-FileBackedDisks)
    }
}

if (-not $InstallApproved) { throw 'Pass -InstallApproved after reviewing the unique store contract.' }
if (-not $BrokerTerminationApproved) { throw 'Pass -BrokerTerminationApproved to terminate only the exact installed broker service PID.' }
if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }
if (-not (Test-Path -LiteralPath $UnityEditorPath -PathType Leaf)) { throw "Unity Editor was not found: $UnityEditorPath" }

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$FixtureTemplate = Join-Path $RepositoryRoot 'testdata\unity-vhdx-fixture'
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-broker-restart-$Stamp"
$FixtureSource = Join-Path $ArtifactRoot 'fixture-source'
$StoreRoot = Join-Path $env:ProgramData "TestPlay\VHDXDiffBrokerRestart-$Stamp"
$WorkspaceRoot = Join-Path $env:LOCALAPPDATA 'TestPlay\Workspaces'
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-broker-restart.exe'
$InstalledBrokerPath = Join-Path $StoreRoot 'broker\testplay.exe'
$ConfigPath = Join-Path $ArtifactRoot 'testplay.json'
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$ZipPath = "$ArtifactRoot.zip"
$UserSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$UserStoreRoot = Join-Path $StoreRoot $UserSID
$LeaseRoot = Join-Path $UserStoreRoot 'leases'
$ChildRoot = Join-Path $UserStoreRoot 'children'

if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) { throw "$ServiceName already exists; refusing to replace it." }
foreach ($Path in @($ReceiptPath, $StoreRoot, $ArtifactRoot, $WorkspaceRoot)) {
    if (Test-Path -LiteralPath $Path) { throw "Pre-existing state is outside this harness ownership: $Path" }
}
$PreProcesses = @(Get-RelatedProcesses)
if ($PreProcesses.Count -ne 0) { throw 'Related Unity/testplay processes already exist; refusing an ambiguous broker termination test.' }

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
$BrokerKilled = $false
$BrokerRestarted = $false
$RecoveryVerified = $false
$CleanupState = 'not-started'
$Failure = $null
$Warmup = $null
$CrashJournal = $null
$FinalJournal = $null
$CrashMarker = $null
$OriginalBroker = $null
$RestartedBroker = $null
$ClientIdentity = $null
$UnityIdentity = $null
$CrashClientExitCode = $null
$RecoveredStatus = $null
$StaleMountEvidence = $null
$StartedClient = $null
$HarnessProcessCleanup = [ordered]@{ attempted = $false; clientStopped = $null; unityStopped = $null }
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

    $env:TESTPLAY_UNITY_FIXTURE_MARKER = "vhdx-diff-broker-restart-warmup-$Stamp"
    $WarmupRun = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('--config', $ConfigPath, 'run', '--filter', $ExpectedTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge') -OutputPath (Join-Path $ArtifactRoot 'warmup-run.txt') -WorkingDirectory $ArtifactRoot
    if ($WarmupRun.ExitCode -ne 0) { throw "warm-up fixture run failed: exit=$($WarmupRun.ExitCode)" }
    $Warmup = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'warmup-run.txt')
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'warmup-result.json') -Value $Warmup
    if ((Get-DirectoryEntryCount $LeaseRoot) -ne 0 -or (Get-DirectoryEntryCount $ChildRoot) -ne 0 -or (Get-DirectoryEntryCount $WorkspaceRoot) -ne 0) { throw 'Warm-up residual is nonzero.' }

    $env:TESTPLAY_UNITY_FIXTURE_MARKER = "vhdx-diff-broker-restart-crash-$Stamp"
    $ClientStdout = Join-Path $ArtifactRoot 'crash-client-stdout.txt'
    $ClientStderr = Join-Path $ArtifactRoot 'crash-client-stderr.txt'
    $Arguments = @('--config', $ConfigPath, 'run', '--filter', $ExpectedTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge')
    $StartedClient = Start-Process -FilePath $ExecutablePath -ArgumentList $Arguments -WorkingDirectory $ArtifactRoot -NoNewWindow -PassThru -RedirectStandardOutput $ClientStdout -RedirectStandardError $ClientStderr

    $ReadyDeadline = (Get-Date).AddMinutes(3)
    $JournalPath = $null
    while ((Get-Date) -lt $ReadyDeadline) {
        if ($StartedClient.HasExited) { throw "Crash client exited before broker termination: exit=$($StartedClient.ExitCode)" }
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
    if ($null -eq $CrashJournal) { throw 'Timed out waiting for one exact ready lease.' }
    if (-not (Test-SamePath $CrashJournal.workspacePath (Join-Path $WorkspaceRoot $CrashJournal.workspaceId))) { throw 'Journal workspace identity mismatch.' }
    if (-not (Test-SamePath $CrashJournal.mountPath (Join-Path $CrashJournal.workspacePath 'Library'))) { throw 'Journal mount identity mismatch.' }
    if (-not (Test-Path -LiteralPath $CrashJournal.childPath -PathType Leaf)) { throw 'Journal child VHDX is missing.' }
    $MarkerPath = Join-Path $CrashJournal.workspacePath $WorkspaceOwnerFile
    $CrashMarker = [IO.File]::ReadAllText($MarkerPath) | ConvertFrom-Json
    foreach ($Property in @('leaseId', 'runId', 'workspaceId', 'ownershipToken')) {
        if ([string]$CrashMarker.$Property -ne [string]$CrashJournal.$Property) { throw "Workspace marker mismatch: $Property" }
    }
    $ClientIdentity = Get-ProcessIdentity ([int]$CrashJournal.clientPid)
    $UnityIdentity = Get-ProcessIdentity ([int]$CrashJournal.unityPid)
    if ($null -eq $ClientIdentity -or -not (Test-SamePath $ClientIdentity.executablePath $ExecutablePath)) { throw 'Client process identity mismatch.' }
    if ($null -eq $UnityIdentity -or -not (Test-SamePath $UnityIdentity.executablePath $UnityEditorPath)) { throw 'Unity process identity mismatch.' }

    $OriginalService = Get-ServiceEvidence
    if ($null -eq $OriginalService -or $OriginalService.state -ne 'Running' -or [int]$OriginalService.processId -le 0) { throw 'Broker service is not running.' }
    $OriginalBroker = Get-ProcessIdentity ([int]$OriginalService.processId)
    if ($null -eq $OriginalBroker -or -not (Test-SamePath $OriginalBroker.executablePath $InstalledBrokerPath)) { throw 'Broker process is not the exact installed executable.' }
    Copy-Item -LiteralPath $JournalPath -Destination (Join-Path $ArtifactRoot 'broker-crash-lease-journal.json')
    Copy-Item -LiteralPath $MarkerPath -Destination (Join-Path $ArtifactRoot 'broker-crash-workspace-owner.json')
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'pre-broker-termination.json') -Value ([ordered]@{
        service = $OriginalService
        broker = $OriginalBroker
        client = $ClientIdentity
        unity = $UnityIdentity
        journal = $CrashJournal
        workspaceOwner = $CrashMarker
        journalSha256 = (Get-FileHash $JournalPath -Algorithm SHA256).Hash
        workspaceOwnerSha256 = (Get-FileHash $MarkerPath -Algorithm SHA256).Hash
        fileBackedDisks = @(Get-FileBackedDisks)
    })

    $BrokerKilled = $true
    Stop-Process -Id ([int]$OriginalService.processId) -Force -ErrorAction Stop
    if (-not (Wait-ProcessAbsent ([int]$OriginalService.processId) 15)) { throw 'Exact broker process did not terminate.' }
    $StoppedService = Wait-ServiceState 'Stopped' 30
    if ($null -eq $StoppedService) { throw 'Service Control Manager did not report the broker stopped.' }

    $StaleMountEvidence = Get-StaleMountEvidence $CrashJournal
    if (-not $StaleMountEvidence.reparsePoint -or $StaleMountEvidence.childAttached -or @($StaleMountEvidence.fileBackedDisks).Count -ne 0) {
        throw 'Post-crash stale mount did not have the expected detached ownership shape.'
    }
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'stale-mount-after-broker-exit.json') -Value $StaleMountEvidence

    Start-Service -Name $ServiceName -ErrorAction Stop
    $RunningService = Wait-ServiceState 'Running' 30
    if ($null -eq $RunningService -or [int]$RunningService.processId -le 0 -or [int]$RunningService.processId -eq [int]$OriginalService.processId) { throw 'Broker service did not restart with a new PID.' }
    $RestartedBroker = Get-ProcessIdentity ([int]$RunningService.processId)
    if ($null -eq $RestartedBroker -or -not (Test-SamePath $RestartedBroker.executablePath $InstalledBrokerPath)) { throw 'Restarted broker executable identity mismatch.' }
    $BrokerRestarted = $true
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'broker-restart.json') -Value ([ordered]@{
        terminatedService = $OriginalService
        terminatedBroker = $OriginalBroker
        stoppedService = $StoppedService
        runningService = $RunningService
        restartedBroker = $RestartedBroker
    })

    if (-not $StartedClient.WaitForExit(90000)) { throw 'Crash client did not exit after broker loss and restart.' }
    # WaitForExit(timeout) only waits for the process handle. Complete the
    # redirected-stream drain and refresh the Process snapshot before reading
    # ExitCode, otherwise Windows PowerShell can serialize this field as null.
    $StartedClient.WaitForExit()
    $StartedClient.Refresh()
    $CrashClientExitCode = [int]$StartedClient.ExitCode
    if (-not (Wait-ProcessAbsent ([int]$CrashJournal.unityPid) 30)) { throw 'Harness-owned Unity process remained after client cancellation.' }

    $RecoveryDeadline = (Get-Date).AddMinutes(3)
    while ((Get-Date) -lt $RecoveryDeadline) {
        $LeaseGone = -not (Test-Path -LiteralPath $JournalPath)
        $ChildGone = -not (Test-Path -LiteralPath $CrashJournal.childPath)
        $WorkspaceGone = -not (Test-Path -LiteralPath $CrashJournal.workspacePath)
        $PreDiskIDs = @($PreDisks | ForEach-Object { $_.Number })
        $NewRecoveryDisks = @(Get-FileBackedDisks | Where-Object { $PreDiskIDs -notcontains $_.Number })
        if ($LeaseGone -and $ChildGone -and $WorkspaceGone -and $NewRecoveryDisks.Count -eq 0) { $RecoveryVerified = $true; break }
        Start-Sleep -Seconds 1
    }
    if (-not $RecoveryVerified) { throw 'Restarted broker did not reconcile the exact orphan lease within the bound.' }

    $Status = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'recovered-storage-status.json') -WorkingDirectory $ArtifactRoot
    if ($Status.ExitCode -ne 0) { throw "storage status after broker restart failed: exit=$($Status.ExitCode)" }
    $RecoveredStatus = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'recovered-storage-status.json')
    foreach ($Property in @('activeChildCount', 'retainedChildCount', 'pendingCount', 'quarantineCount')) {
        if ([int]$RecoveredStatus.$Property -ne 0) { throw "Recovered storage status is nonzero: $Property=$($RecoveredStatus.$Property)" }
    }
    if ([bool]$RecoveredStatus.manualRecoveryRequired) { throw 'Recovered storage status requires manual recovery.' }
    $CleanupState = 'recovered'
}
catch {
    $Failure = $_.Exception.ToString()
    if ($BrokerKilled -and -not $RecoveryVerified) { $CleanupState = 'preserved' }
    else { $CleanupState = 'failed-before-broker-crash' }
}
finally {
    if ($null -ne $StartedClient -and -not $StartedClient.HasExited) {
        $CurrentClient = Get-ProcessIdentity $StartedClient.Id
        if ($null -ne $CurrentClient -and (Test-SamePath $CurrentClient.executablePath $ExecutablePath)) {
            $HarnessProcessCleanup.attempted = $true
            Stop-Process -Id $StartedClient.Id -Force -ErrorAction SilentlyContinue
            $HarnessProcessCleanup.clientStopped = Wait-ProcessAbsent $StartedClient.Id 15
        }
    }
    if ($null -ne $CrashJournal -and [int]$CrashJournal.unityPid -gt 0) {
        $CurrentUnity = Get-ProcessIdentity ([int]$CrashJournal.unityPid)
        if ($null -ne $CurrentUnity -and (Test-SamePath $CurrentUnity.executablePath $UnityEditorPath)) {
            $HarnessProcessCleanup.attempted = $true
            Stop-Process -Id ([int]$CrashJournal.unityPid) -Force -ErrorAction SilentlyContinue
            $HarnessProcessCleanup.unityStopped = Wait-ProcessAbsent ([int]$CrashJournal.unityPid) 15
        }
    }
    if ($BrokerKilled -and -not $BrokerRestarted) {
        try {
            Start-Service -Name $ServiceName -ErrorAction Stop
            $BrokerRestarted = $null -ne (Wait-ServiceState 'Running' 30)
        }
        catch { }
    }
    if ($Installed -and (-not $BrokerKilled -or $RecoveryVerified)) {
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
if ($null -ne $CrashJournal) {
    $FinalJournalPath = Join-Path $LeaseRoot "$($CrashJournal.leaseId).json"
    if (Test-Path -LiteralPath $FinalJournalPath) {
        try { $FinalJournal = [IO.File]::ReadAllText($FinalJournalPath) | ConvertFrom-Json }
        catch { $FinalJournal = [ordered]@{ decodeError = $_.Exception.Message; path = $FinalJournalPath } }
    }
}
$ResidualZero = $NewDisks.Count -eq 0 -and $NewLetters.Count -eq 0 -and $NewProcesses.Count -eq 0 -and -not (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) -and -not (Test-Path $ReceiptPath) -and -not (Test-Path $StoreRoot) -and -not (Test-Path $WorkspaceRoot)
if ($RecoveryVerified -and $Uninstalled -and -not $ResidualZero -and $null -eq $Failure) { $Failure = 'Final outer residual is nonzero.' }
$Passed = $null -eq $Failure -and $BrokerKilled -and $BrokerRestarted -and $RecoveryVerified -and $Uninstalled -and $ResidualZero
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { 'VHDX_DIFF_FIXTURE_BROKER_RESTART_RECOVERY_PASS' } else { 'FAILED' }
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
    finalJournal = $FinalJournal
    crashWorkspaceOwner = $CrashMarker
    originalBroker = $OriginalBroker
    restartedBroker = $RestartedBroker
    staleMountAfterBrokerExit = $StaleMountEvidence
    clientProcess = $ClientIdentity
    unityProcess = $UnityIdentity
    crashClientExitCode = $CrashClientExitCode
    harnessProcessCleanup = $HarnessProcessCleanup
    brokerKilled = $BrokerKilled
    brokerRestarted = $BrokerRestarted
    recoveryVerified = $RecoveryVerified
    recoveredStorageStatus = $RecoveredStatus
    installed = $Installed
    uninstalled = $Uninstalled
    cleanupState = $CleanupState
    residualZero = $ResidualZero
    newFileBackedDisks = @($NewDisks)
    newDriveLetters = @($NewLetters)
    newProcesses = @($NewProcesses)
    failure = $Failure
    notMeasured = @('GNF forced termination', 'Windows reboot recovery', 'quota/LRU native behavior', 'eight-worker compatibility', 'performance superiority', 'production readiness', 'release readiness')
}
Write-JsonFile -LiteralPath $SummaryPath -Value $Summary
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash
Write-Output "VHDX_DIFF_BROKER_RESTART_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_BROKER_RESTART_VERDICT=$($Summary.verdict)"
Write-Output "VHDX_DIFF_BROKER_RESTART_CLEANUP=$CleanupState"
Write-Output "VHDX_DIFF_BROKER_RESTART_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_BROKER_RESTART_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
