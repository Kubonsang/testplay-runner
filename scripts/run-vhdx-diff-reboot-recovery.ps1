[CmdletBinding()]
param(
    [ValidateSet('Prepare', 'Verify')]
    [string]$Phase = 'Prepare',

    [string]$UnityEditorPath,

    [switch]$InstallApproved,

    [switch]$RebootApproved,

    [string]$StatePath,

    [string]$StateSHA256,

    [switch]$CleanupApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ServiceName = 'TestPlayStorageBroker'
$WarmupTest = 'TestPlayFixture.Tests.LibraryMountTests.LibraryMountWriteReadTest'
$RebootTest = 'TestPlayFixture.Tests.LibraryMountTests.RebootRecoveryHoldTest'
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

function Write-DurableJsonExclusive {
    param([string]$LiteralPath, [object]$Value)
    $Json = ($Value | ConvertTo-Json -Depth 24) + [Environment]::NewLine
    $Bytes = [Text.UTF8Encoding]::new($false).GetBytes($Json)
    $Stream = [IO.FileStream]::new($LiteralPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
    try {
        $Stream.Write($Bytes, 0, $Bytes.Length)
        $Stream.Flush($true)
    }
    finally {
        $Stream.Dispose()
    }
    $ReadBack = [IO.File]::ReadAllBytes($LiteralPath)
    if ([Convert]::ToBase64String($Bytes) -ne [Convert]::ToBase64String($ReadBack)) {
        throw "Durable state read-back mismatch: $LiteralPath"
    }
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

function Get-BootEvidence {
    $OS = Get-CimInstance Win32_OperatingSystem -ErrorAction Stop
    return [ordered]@{
        computerName = [Environment]::MachineName
        lastBootUpTime = $OS.LastBootUpTime.ToUniversalTime().ToString('o')
        localTime = (Get-Date).ToUniversalTime().ToString('o')
    }
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

function Wait-ServiceRunning {
    param([int]$TimeoutSeconds = 60)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $Deadline) {
        $Service = Get-ServiceEvidence
        if ($null -ne $Service -and $Service.state -eq 'Running' -and $Service.processId -gt 0) { return $Service }
        Start-Sleep -Milliseconds 250
    }
    return $null
}

function Get-DirectoryEntryCount {
    param([string]$LiteralPath)
    if (-not (Test-Path -LiteralPath $LiteralPath -PathType Container)) { return 0 }
    return @(Get-ChildItem -LiteralPath $LiteralPath -Force -ErrorAction Stop).Count
}

function Stop-ExactProcess {
    param([int]$ProcessID, [string]$ExpectedPath)
    $Identity = Get-ProcessIdentity $ProcessID
    if ($null -eq $Identity) { return $true }
    if (-not (Test-SamePath $Identity.executablePath $ExpectedPath)) { return $false }
    Stop-Process -Id $ProcessID -Force -ErrorAction SilentlyContinue
    $Deadline = (Get-Date).AddSeconds(15)
    while ((Get-Date) -lt $Deadline -and $null -ne (Get-Process -Id $ProcessID -ErrorAction SilentlyContinue)) {
        Start-Sleep -Milliseconds 200
    }
    return $null -eq (Get-Process -Id $ProcessID -ErrorAction SilentlyContinue)
}

if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$PointerPath = Join-Path $env:ProgramData 'TestPlay\vhdx-diff-reboot-recovery-pointer.json'

if ($Phase -eq 'Prepare') {
    if (-not $InstallApproved) { throw 'Pass -InstallApproved after reviewing the unique store contract.' }
    if (-not $RebootApproved) { throw 'Pass -RebootApproved to intentionally leave one exact ephemeral child for the reboot.' }
    if (-not (Test-Path -LiteralPath $UnityEditorPath -PathType Leaf)) { throw "Unity Editor was not found: $UnityEditorPath" }

    $FixtureTemplate = Join-Path $RepositoryRoot 'testdata\unity-vhdx-fixture'
    $Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
    $ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-reboot-recovery-$Stamp"
    $FixtureSource = Join-Path $ArtifactRoot 'fixture-source'
    $StoreRoot = Join-Path $env:ProgramData "TestPlay\VHDXDiffRebootRecovery-$Stamp"
    $WorkspaceRoot = Join-Path $env:LOCALAPPDATA 'TestPlay\Workspaces'
    $ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-reboot-recovery.exe'
    $InstalledBrokerPath = Join-Path $StoreRoot 'broker\testplay.exe'
    $ConfigPath = Join-Path $ArtifactRoot 'testplay.json'
    $StatePath = Join-Path $ArtifactRoot 'reboot-state.json'
    $UserSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $UserStoreRoot = Join-Path $StoreRoot $UserSID
    $LeaseRoot = Join-Path $UserStoreRoot 'leases'
    $ChildRoot = Join-Path $UserStoreRoot 'children'
    $Prepared = $false
    $Installed = $false
    $StartedClient = $null
    $Journal = $null
    $Failure = $null

    if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) { throw "$ServiceName already exists; refusing to replace it." }
    foreach ($Path in @($ReceiptPath, $PointerPath, $StoreRoot, $ArtifactRoot, $WorkspaceRoot)) {
        if (Test-Path -LiteralPath $Path) { throw "Pre-existing state is outside this harness ownership: $Path" }
    }
    $PreDisks = @(Get-FileBackedDisks)
    $PreProcesses = @(Get-RelatedProcesses)
    if ($PreDisks.Count -ne 0) { throw 'File-backed virtual disks already exist; refusing an ambiguous reboot test.' }
    if ($PreProcesses.Count -ne 0) { throw 'Related Unity/testplay processes already exist; refusing an ambiguous reboot test.' }
    $PreLetters = @(Get-DriveLetters)

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
        timeout = [ordered]@{ total_ms = 2700000 }
        result_dir = (Join-Path $ArtifactRoot 'results')
        workspace = [ordered]@{
            backend = 'vhdx-diff'
            store_root = $StoreRoot
            store_max_allocated_bytes = 34359738368
            minimum_host_free_bytes = 21474836480
        }
    })

    Start-Transcript -Path (Join-Path $ArtifactRoot 'prepare-terminal-transcript.txt') -Force | Out-Null
    try {
        Push-Location $RepositoryRoot
        try {
            & go build -buildvcs=false -o $ExecutablePath .\cmd\testplay
            if ($LASTEXITCODE -ne 0) { throw "go build failed: exit=$LASTEXITCODE" }
        }
        finally { Pop-Location }

        $Install = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'install', '--root', $StoreRoot) -OutputPath (Join-Path $ArtifactRoot 'storage-install.txt') -WorkingDirectory $ArtifactRoot
        if ($Install.ExitCode -ne 0) { throw "storage install failed: exit=$($Install.ExitCode)" }
        $Installed = $true

        $env:TESTPLAY_UNITY_FIXTURE_MARKER = "vhdx-diff-reboot-warmup-$Stamp"
        $WarmupRun = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('--config', $ConfigPath, 'run', '--filter', $WarmupTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge') -OutputPath (Join-Path $ArtifactRoot 'warmup-run.txt') -WorkingDirectory $ArtifactRoot
        if ($WarmupRun.ExitCode -ne 0) { throw "warm-up fixture run failed: exit=$($WarmupRun.ExitCode)" }
        $Warmup = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'warmup-run.txt')
        Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'warmup-result.json') -Value $Warmup
        if ((Get-DirectoryEntryCount $LeaseRoot) -ne 0 -or (Get-DirectoryEntryCount $ChildRoot) -ne 0 -or (Get-DirectoryEntryCount $WorkspaceRoot) -ne 0) { throw 'Warm-up residual is nonzero.' }

        $env:TESTPLAY_UNITY_FIXTURE_MARKER = "vhdx-diff-reboot-orphan-$Stamp"
        $ReadySignalPath = Join-Path $ArtifactRoot 'unity-reboot-hold-ready.txt'
        $ReleaseSignalPath = Join-Path $ArtifactRoot 'unity-reboot-hold-release.txt'
        $env:TESTPLAY_UNITY_FIXTURE_REBOOT_READY_FILE = $ReadySignalPath
        $env:TESTPLAY_UNITY_FIXTURE_REBOOT_RELEASE_FILE = $ReleaseSignalPath
        $ClientStdout = Join-Path $ArtifactRoot 'reboot-client-stdout.txt'
        $ClientStderr = Join-Path $ArtifactRoot 'reboot-client-stderr.txt'
        $Arguments = @('--config', $ConfigPath, 'run', '--filter', $RebootTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge')
        $StartedClient = Start-Process -FilePath $ExecutablePath -ArgumentList $Arguments -WorkingDirectory $ArtifactRoot -NoNewWindow -PassThru -RedirectStandardOutput $ClientStdout -RedirectStandardError $ClientStderr

        $ReadyDeadline = (Get-Date).AddMinutes(3)
        $JournalPath = $null
        while ((Get-Date) -lt $ReadyDeadline) {
            if ($StartedClient.HasExited) { throw "Reboot client exited before the ready gate: exit=$($StartedClient.ExitCode)" }
            $Candidates = @(Get-ChildItem -LiteralPath $LeaseRoot -Filter '*.json' -File -ErrorAction SilentlyContinue)
            if ($Candidates.Count -eq 1) {
                try {
                    $Candidate = [IO.File]::ReadAllText($Candidates[0].FullName) | ConvertFrom-Json
                    if ($Candidate.state -eq 'ready' -and [int]$Candidate.clientPid -eq $StartedClient.Id -and [int]$Candidate.unityPid -gt 0 -and (Test-Path -LiteralPath $ReadySignalPath -PathType Leaf)) {
                        $JournalPath = $Candidates[0].FullName
                        $Journal = $Candidate
                        break
                    }
                }
                catch { }
            }
            Start-Sleep -Milliseconds 50
        }
        if ($null -eq $Journal) { throw 'Timed out waiting for one exact ready lease.' }
        if ([string]::IsNullOrWhiteSpace([string]$Journal.bootSessionId)) { throw 'Broker did not measure a boot-session identity.' }
        if (-not (Test-SamePath $Journal.workspacePath (Join-Path $WorkspaceRoot $Journal.workspaceId))) { throw 'Journal workspace identity mismatch.' }
        if (-not (Test-SamePath $Journal.mountPath (Join-Path $Journal.workspacePath 'Library'))) { throw 'Journal mount identity mismatch.' }
        if (-not (Test-Path -LiteralPath $Journal.childPath -PathType Leaf)) { throw 'Journal child VHDX is missing.' }
        $MarkerPath = Join-Path $Journal.workspacePath $WorkspaceOwnerFile
        $Marker = [IO.File]::ReadAllText($MarkerPath) | ConvertFrom-Json
        foreach ($Property in @('leaseId', 'runId', 'workspaceId', 'ownershipToken')) {
            if ([string]$Marker.$Property -ne [string]$Journal.$Property) { throw "Workspace marker mismatch: $Property" }
        }
        $ClientIdentity = Get-ProcessIdentity ([int]$Journal.clientPid)
        $UnityIdentity = Get-ProcessIdentity ([int]$Journal.unityPid)
        if ($null -eq $ClientIdentity -or -not (Test-SamePath $ClientIdentity.executablePath $ExecutablePath)) { throw 'Client process identity mismatch.' }
        if ($null -eq $UnityIdentity -or -not (Test-SamePath $UnityIdentity.executablePath $UnityEditorPath)) { throw 'Unity process identity mismatch.' }
        $Service = Get-ServiceEvidence
        if ($null -eq $Service -or $Service.state -ne 'Running' -or $Service.startMode -ne 'Auto') { throw 'Broker service is not running with automatic start.' }
        $BrokerIdentity = Get-ProcessIdentity ([int]$Service.processId)
        if ($null -eq $BrokerIdentity -or -not (Test-SamePath $BrokerIdentity.executablePath $InstalledBrokerPath)) { throw 'Broker executable identity mismatch.' }
        $Images = @(Get-DiskImage -ImagePath $Journal.childPath -ErrorAction Stop)
        if ($Images.Count -ne 1 -or -not [bool]$Images[0].Attached) { throw 'Exact reboot child is not attached.' }

        Copy-Item -LiteralPath $JournalPath -Destination (Join-Path $ArtifactRoot 'pre-reboot-lease-journal.json')
        Copy-Item -LiteralPath $MarkerPath -Destination (Join-Path $ArtifactRoot 'pre-reboot-workspace-owner.json')
        $BootBefore = Get-BootEvidence
        $Contract = [ordered]@{
            schemaVersion = 1
            phase = 'prepared'
            preparedAt = (Get-Date).ToUniversalTime().ToString('o')
            repositoryRoot = $RepositoryRoot
            artifactRoot = $ArtifactRoot
            executablePath = $ExecutablePath
            installedBrokerPath = $InstalledBrokerPath
            configPath = $ConfigPath
            storeRoot = $StoreRoot
            workspaceRoot = $WorkspaceRoot
            receiptPath = $ReceiptPath
            pointerPath = $PointerPath
            userSid = $UserSID
            unityEditorPath = $UnityEditorPath
            selectedTest = $RebootTest
            readySignalPath = $ReadySignalPath
            readySignalSha256 = (Get-FileHash $ReadySignalPath -Algorithm SHA256).Hash
            releaseSignalPath = $ReleaseSignalPath
            journalPath = $JournalPath
            markerPath = $MarkerPath
            leaseId = [string]$Journal.leaseId
            ownershipToken = [string]$Journal.ownershipToken
            childPath = [string]$Journal.childPath
            mountPath = [string]$Journal.mountPath
            volumeGuid = [string]$Journal.volumeGuid
            bootSessionIdBefore = [string]$Journal.bootSessionId
            bootBefore = $BootBefore
            journalSha256 = (Get-FileHash $JournalPath -Algorithm SHA256).Hash
            markerSha256 = (Get-FileHash $MarkerPath -Algorithm SHA256).Hash
            serviceBefore = $Service
            brokerBefore = $BrokerIdentity
            clientBefore = $ClientIdentity
            unityBefore = $UnityIdentity
            executableSha256 = (Get-FileHash $ExecutablePath -Algorithm SHA256).Hash
            installedBrokerSha256 = (Get-FileHash $InstalledBrokerPath -Algorithm SHA256).Hash
            workspacePath = [string]$Journal.workspacePath
            diskImageBefore = [ordered]@{
                attached = [bool]$Images[0].Attached
                imagePath = [string]$Images[0].ImagePath
                devicePath = [string]$Images[0].DevicePath
            }
            preState = [ordered]@{
                fileBackedDisks = @($PreDisks)
                driveLetters = @($PreLetters)
                relatedProcesses = @($PreProcesses)
            }
        }
        Write-DurableJsonExclusive -LiteralPath $StatePath -Value $Contract
        $StateSHA256 = (Get-FileHash -LiteralPath $StatePath -Algorithm SHA256).Hash
        $Pointer = [ordered]@{
            schemaVersion = 1
            statePath = $StatePath
            stateSHA256 = $StateSHA256
            harnessPath = $PSCommandPath
            harnessSHA256 = (Get-FileHash $PSCommandPath -Algorithm SHA256).Hash
            createdAt = (Get-Date).ToUniversalTime().ToString('o')
        }
        Write-DurableJsonExclusive -LiteralPath $PointerPath -Value $Pointer
        Copy-Item -LiteralPath $PointerPath -Destination (Join-Path $ArtifactRoot 'reboot-pointer.json')
        $PointerSHA256 = (Get-FileHash -LiteralPath $PointerPath -Algorithm SHA256).Hash
        Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'prepare-summary.json') -Value ([ordered]@{
            schemaVersion = 1
            status = 'REBOOT_REQUIRED'
            verdict = 'PENDING_REBOOT'
            statePath = $StatePath
            stateSHA256 = $StateSHA256
            pointerPath = $PointerPath
            pointerSHA256 = $PointerSHA256
            bootSessionIdBefore = $Journal.bootSessionId
            leaseId = $Journal.leaseId
            cleanupState = 'preserved-for-reboot'
            failure = $null
        })
        $Prepared = $true
    }
    catch {
        $Failure = $_.Exception.ToString()
    }
    finally {
        if (-not $Prepared) {
            if ($null -ne $Journal) {
                [void](Stop-ExactProcess ([int]$Journal.clientPid) $ExecutablePath)
                [void](Stop-ExactProcess ([int]$Journal.unityPid) $UnityEditorPath)
                Start-Sleep -Seconds 35
            }
            if ($Installed) {
                try { [void](Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'uninstall') -OutputPath (Join-Path $ArtifactRoot 'failed-prepare-uninstall.txt') -WorkingDirectory $ArtifactRoot) } catch { }
            }
            if ((Test-Path -LiteralPath $PointerPath -PathType Leaf) -and (Test-Path -LiteralPath $StatePath -PathType Leaf)) {
                try {
                    $FailedPointer = [IO.File]::ReadAllText($PointerPath) | ConvertFrom-Json
                    if ([int]$FailedPointer.schemaVersion -eq 1 -and (Test-SamePath ([string]$FailedPointer.statePath) $StatePath) -and [string]$FailedPointer.stateSHA256 -eq (Get-FileHash $StatePath -Algorithm SHA256).Hash) {
                        Remove-Item -LiteralPath $PointerPath -Force
                    }
                }
                catch { }
            }
        }
        Remove-Item Env:TESTPLAY_UNITY_FIXTURE_MARKER -ErrorAction SilentlyContinue
        Remove-Item Env:TESTPLAY_UNITY_FIXTURE_REBOOT_READY_FILE -ErrorAction SilentlyContinue
        Remove-Item Env:TESTPLAY_UNITY_FIXTURE_REBOOT_RELEASE_FILE -ErrorAction SilentlyContinue
        Stop-Transcript | Out-Null
    }
    if (-not $Prepared) {
        Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'prepare-summary.json') -Value ([ordered]@{ schemaVersion = 1; status = 'FAILED'; verdict = 'FAILED'; cleanupState = 'preserved'; failure = $Failure })
        $ZipPath = "$ArtifactRoot.zip"
        Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
        Write-Output 'VHDX_DIFF_REBOOT_PREPARE_STATUS=FAILED'
        Write-Output "VHDX_DIFF_REBOOT_PREPARE_ARTIFACT_ZIP=$ZipPath"
        Write-Output "VHDX_DIFF_REBOOT_PREPARE_ARTIFACT_SHA256=$((Get-FileHash $ZipPath -Algorithm SHA256).Hash)"
        exit 1
    }
    Write-Output 'VHDX_DIFF_REBOOT_PREPARE_STATUS=REBOOT_REQUIRED'
    Write-Output "VHDX_DIFF_REBOOT_STATE=$StatePath"
    Write-Output "VHDX_DIFF_REBOOT_STATE_SHA256=$StateSHA256"
    Write-Output "VHDX_DIFF_REBOOT_POINTER=$PointerPath"
    Write-Output "VHDX_DIFF_REBOOT_POINTER_SHA256=$PointerSHA256"
    Write-Output "VHDX_DIFF_REBOOT_VERIFY_COMMAND=powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`" -Phase Verify -StatePath `"$StatePath`" -StateSHA256 $StateSHA256 -CleanupApproved"
    exit 0
}

if (-not $CleanupApproved) { throw 'Pass -CleanupApproved to allow normal ownership-safe uninstall after verified recovery.' }
if (-not (Test-Path -LiteralPath $StatePath -PathType Leaf)) { throw "Reboot state was not found: $StatePath" }
$ActualStateSHA256 = (Get-FileHash -LiteralPath $StatePath -Algorithm SHA256).Hash
if ([string]::IsNullOrWhiteSpace($StateSHA256) -or $ActualStateSHA256 -ne $StateSHA256) { throw 'Reboot state SHA-256 mismatch.' }
$Contract = [IO.File]::ReadAllText($StatePath) | ConvertFrom-Json
if ([int]$Contract.schemaVersion -ne 1 -or [string]$Contract.phase -ne 'prepared') { throw 'Unsupported reboot state contract.' }
$ArtifactRoot = [string]$Contract.artifactRoot
if (-not (Test-SamePath (Split-Path -Parent $StatePath) $ArtifactRoot)) { throw 'Reboot state is outside its recorded artifact root.' }
$ExecutablePath = [string]$Contract.executablePath
$StoreRoot = [string]$Contract.storeRoot
$WorkspaceRoot = [string]$Contract.workspaceRoot
$ReceiptPath = [string]$Contract.receiptPath
$CurrentSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$ExpectedStoreParent = Join-Path $env:ProgramData 'TestPlay'
if ([string]$Contract.userSid -ne $CurrentSID) { throw 'Reboot state belongs to a different Windows user SID.' }
if (-not (Test-SamePath $WorkspaceRoot (Join-Path $env:LOCALAPPDATA 'TestPlay\Workspaces'))) { throw 'Recorded workspace root is outside the reboot harness contract.' }
if (-not (Test-SamePath $ReceiptPath (Join-Path $env:ProgramData 'TestPlay\storage-install.json'))) { throw 'Recorded receipt path is outside the reboot harness contract.' }
if (-not (Test-SamePath ([string]$Contract.pointerPath) $PointerPath)) { throw 'Recorded reboot pointer path is outside the harness contract.' }
if (-not (Test-Path -LiteralPath $PointerPath -PathType Leaf)) { throw 'Durable reboot pointer is missing.' }
$PointerEvidence = [IO.File]::ReadAllText($PointerPath) | ConvertFrom-Json
if ([int]$PointerEvidence.schemaVersion -ne 1 -or -not (Test-SamePath ([string]$PointerEvidence.statePath) $StatePath) -or [string]$PointerEvidence.stateSHA256 -ne $ActualStateSHA256 -or -not (Test-SamePath ([string]$PointerEvidence.harnessPath) $PSCommandPath) -or [string]$PointerEvidence.harnessSHA256 -ne (Get-FileHash $PSCommandPath -Algorithm SHA256).Hash) { throw 'Durable reboot pointer identity mismatch.' }
if (-not (Test-SamePath (Split-Path -Parent $StoreRoot) $ExpectedStoreParent) -or (Split-Path -Leaf $StoreRoot) -notlike 'VHDXDiffRebootRecovery-*') { throw 'Recorded store root is outside the reboot harness contract.' }
if (-not (Test-SamePath (Split-Path -Parent $ExecutablePath) $ArtifactRoot)) { throw 'Recorded executable is outside the artifact root.' }
if (-not (Test-Path -LiteralPath $ExecutablePath -PathType Leaf) -or (Get-FileHash $ExecutablePath -Algorithm SHA256).Hash -ne [string]$Contract.executableSha256) { throw 'Recorded executable hash mismatch.' }
if (-not (Test-Path -LiteralPath ([string]$Contract.readySignalPath) -PathType Leaf) -or (Get-FileHash ([string]$Contract.readySignalPath) -Algorithm SHA256).Hash -ne [string]$Contract.readySignalSha256) { throw 'Unity pre-reboot ready signal hash mismatch.' }
if (Test-Path -LiteralPath ([string]$Contract.releaseSignalPath)) { throw 'Unexpected Unity release signal exists; the hold was not reserved for reboot.' }
$Failure = $null
$CleanupState = 'preserved'
$RecoveryVerified = $false
$Uninstalled = $false
$StatusEvidence = $null
$BootAfter = Get-BootEvidence
$StartedAt = Get-Date

Start-Transcript -Path (Join-Path $ArtifactRoot 'verify-terminal-transcript.txt') -Force | Out-Null
try {
    if ([string]$BootAfter.computerName -ne [string]$Contract.bootBefore.computerName) { throw 'Computer identity changed.' }
    if ([string]$BootAfter.lastBootUpTime -eq [string]$Contract.bootBefore.lastBootUpTime) { throw 'Windows reboot was not observed.' }
    $Service = Wait-ServiceRunning 90
    if ($null -eq $Service -or $Service.startMode -ne 'Auto') { throw 'Broker service did not automatically start after reboot.' }
    if (-not (Test-Path -LiteralPath ([string]$Contract.installedBrokerPath) -PathType Leaf) -or (Get-FileHash ([string]$Contract.installedBrokerPath) -Algorithm SHA256).Hash -ne [string]$Contract.installedBrokerSha256) { throw 'Installed broker hash changed across reboot.' }
    $BrokerIdentity = Get-ProcessIdentity ([int]$Service.processId)
    if ($null -eq $BrokerIdentity -or -not (Test-SamePath $BrokerIdentity.executablePath ([string]$Contract.installedBrokerPath))) { throw 'Post-reboot broker identity mismatch.' }

    $Deadline = (Get-Date).AddMinutes(3)
    while ((Get-Date) -lt $Deadline) {
        $JournalGone = -not (Test-Path -LiteralPath ([string]$Contract.journalPath))
        $ChildGone = -not (Test-Path -LiteralPath ([string]$Contract.childPath))
        $WorkspaceGone = -not (Test-Path -LiteralPath ([string]$Contract.workspacePath))
        $DisksGone = @(Get-FileBackedDisks).Count -eq 0
        if ($JournalGone -and $ChildGone -and $WorkspaceGone -and $DisksGone) { $RecoveryVerified = $true; break }
        Start-Sleep -Seconds 1
    }
    if (-not $RecoveryVerified) { throw 'Broker did not reconcile the exact reboot orphan within the bound.' }

    $Status = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'post-reboot-storage-status.json') -WorkingDirectory $ArtifactRoot
    if ($Status.ExitCode -ne 0) { throw "Post-reboot storage status failed: exit=$($Status.ExitCode)" }
    $StatusEvidence = Read-NativeJson (Join-Path $ArtifactRoot 'post-reboot-storage-status.json')
    if ([string]::IsNullOrWhiteSpace([string]$StatusEvidence.bootSessionId) -or [string]$StatusEvidence.bootSessionId -eq [string]$Contract.bootSessionIdBefore) { throw 'Broker boot-session identity did not change.' }
    foreach ($Property in @('activeChildCount', 'retainedChildCount', 'pendingCount', 'quarantineCount')) {
        if ([int]$StatusEvidence.$Property -ne 0) { throw "Post-reboot storage status is nonzero: $Property=$($StatusEvidence.$Property)" }
    }
    if ([int]$StatusEvidence.parentCount -ne 1 -or [bool]$StatusEvidence.manualRecoveryRequired) { throw 'Post-reboot parent/manual-recovery status is unexpected.' }

    $Uninstall = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'uninstall') -OutputPath (Join-Path $ArtifactRoot 'storage-uninstall.txt') -WorkingDirectory $ArtifactRoot
    if ($Uninstall.ExitCode -ne 0) { throw "Storage uninstall failed: exit=$($Uninstall.ExitCode)" }
    $Uninstalled = $true
    $CleanupState = 'released'
}
catch {
    $Failure = $_.Exception.ToString()
    if (@(Get-FileBackedDisks).Count -ne 0) { $CleanupState = 'uncertain' }
}
finally {
    Stop-Transcript | Out-Null
}

$PostDisks = @(Get-FileBackedDisks)
$PostProcesses = @(Get-RelatedProcesses)
$PostLetters = @(Get-DriveLetters)
$PreLetterIDs = @($Contract.preState.driveLetters | ForEach-Object { $_.UniqueId })
$NewLetters = @($PostLetters | Where-Object { $PreLetterIDs -notcontains $_.UniqueId })
$ResidualZero = $PostDisks.Count -eq 0 -and $NewLetters.Count -eq 0 -and $PostProcesses.Count -eq 0 -and -not (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) -and -not (Test-Path -LiteralPath $ReceiptPath) -and -not (Test-Path -LiteralPath $StoreRoot) -and -not (Test-Path -LiteralPath $WorkspaceRoot)
if ($Uninstalled -and -not $ResidualZero -and $null -eq $Failure) { $Failure = 'Final outer residual is nonzero.'; $CleanupState = 'uncertain' }
$Passed = $null -eq $Failure -and $RecoveryVerified -and $Uninstalled -and $ResidualZero
if ($Passed -and (Test-Path -LiteralPath $PointerPath -PathType Leaf)) {
    $Pointer = [IO.File]::ReadAllText($PointerPath) | ConvertFrom-Json
    if ([int]$Pointer.schemaVersion -ne 1 -or -not (Test-SamePath ([string]$Pointer.statePath) $StatePath) -or [string]$Pointer.stateSHA256 -ne $ActualStateSHA256) {
        $Passed = $false
        $Failure = 'Refusing to remove mismatched reboot pointer.'
        $CleanupState = 'preserved'
    }
    else {
        Remove-Item -LiteralPath $PointerPath -Force
    }
}
$ResidualZero = $ResidualZero -and -not (Test-Path -LiteralPath $PointerPath)
$Passed = $Passed -and $ResidualZero
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { 'VHDX_DIFF_FIXTURE_REBOOT_RECOVERY_PASS' } else { 'FAILED' }
    startedAt = $StartedAt.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    statePath = $StatePath
    stateSHA256 = $ActualStateSHA256
    bootBefore = $Contract.bootBefore
    bootAfter = $BootAfter
    bootSessionIdBefore = $Contract.bootSessionIdBefore
    bootSessionIdAfter = if ($null -ne $StatusEvidence) { $StatusEvidence.bootSessionId } else { $null }
    leaseId = $Contract.leaseId
    journalSha256 = $Contract.journalSha256
    markerSha256 = $Contract.markerSha256
    recoveryVerified = $RecoveryVerified
    recoveredStorageStatus = $StatusEvidence
    uninstalled = $Uninstalled
    cleanupState = $CleanupState
    residualZero = $ResidualZero
    fileBackedDisks = @($PostDisks)
    newDriveLetters = @($NewLetters)
    relatedProcesses = @($PostProcesses)
    failure = $Failure
    notMeasured = @('GNF forced termination', 'quota/LRU native behavior', 'eight-worker compatibility', 'performance superiority', 'production readiness', 'release readiness')
}
Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'summary.json') -Value $Summary
$ZipPath = "$ArtifactRoot.zip"
if (Test-Path -LiteralPath $ZipPath) { throw "Refusing to overwrite artifact ZIP: $ZipPath" }
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash
Write-Output "VHDX_DIFF_REBOOT_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_REBOOT_VERDICT=$($Summary.verdict)"
Write-Output "VHDX_DIFF_REBOOT_CLEANUP=$CleanupState"
Write-Output "VHDX_DIFF_REBOOT_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_REBOOT_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
