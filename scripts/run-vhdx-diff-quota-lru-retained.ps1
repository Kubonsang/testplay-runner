[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$UnityEditorPath,

    [switch]$InstallApproved,

    [switch]$QuotaMutationApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ServiceName = 'TestPlayStorageBroker'
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

function Write-JsonReplaceDurable {
    param([string]$LiteralPath, [object]$Value)
    $Directory = Split-Path -Parent $LiteralPath
    $TempPath = Join-Path $Directory ('.quota-config-' + [Guid]::NewGuid().ToString('N') + '.tmp')
    $Json = ($Value | ConvertTo-Json -Depth 24) + [Environment]::NewLine
    $Bytes = [Text.UTF8Encoding]::new($false).GetBytes($Json)
    $Stream = [IO.FileStream]::new($TempPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
    try {
        $Stream.Write($Bytes, 0, $Bytes.Length)
        $Stream.Flush($true)
    }
    finally { $Stream.Dispose() }
    try {
        [IO.File]::Replace($TempPath, $LiteralPath, $null, $true)
    }
    finally {
        if (Test-Path -LiteralPath $TempPath) { Remove-Item -LiteralPath $TempPath -Force }
    }
    $ReadBack = [IO.File]::ReadAllBytes($LiteralPath)
    if ([Convert]::ToBase64String($Bytes) -ne [Convert]::ToBase64String($ReadBack)) {
        throw "Config read-back mismatch: $LiteralPath"
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

function Get-ServiceEvidence {
    $Service = Get-CimInstance Win32_Service -Filter "Name = '$ServiceName'" -ErrorAction SilentlyContinue
    if ($null -eq $Service) { return $null }
    return [ordered]@{ state = [string]$Service.State; startMode = [string]$Service.StartMode; processId = [int]$Service.ProcessId; pathName = [string]$Service.PathName }
}

function Wait-ServiceState {
    param([string]$State, [int]$TimeoutSeconds = 60)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $Deadline) {
        $Evidence = Get-ServiceEvidence
        if ($null -ne $Evidence -and $Evidence.state -eq $State) { return $Evidence }
        if ($State -eq 'Absent' -and $null -eq $Evidence) { return $true }
        Start-Sleep -Milliseconds 250
    }
    return $null
}

function Set-ExactBrokerPolicy {
    param([string]$ConfigPath, [string]$StoreRoot, [string]$WorkspaceRoot, [string]$UserSID, [long]$QuotaBytes, [long]$ParentTTLSeconds, [string]$ArtifactPath)
    Stop-Service -Name $ServiceName -ErrorAction Stop
    if ($null -eq (Wait-ServiceState 'Stopped' 60)) { throw 'Broker service did not stop for exact policy update.' }
    $Config = [IO.File]::ReadAllText($ConfigPath) | ConvertFrom-Json
    if ([string]$Config.storeRoot -ne $StoreRoot -or [string]$Config.workspaceRoot -ne $WorkspaceRoot -or [string]$Config.userSid -ne $UserSID) {
        throw 'Service config identity mismatch.'
    }
    $Before = $Config | ConvertTo-Json -Depth 12 | ConvertFrom-Json
    $Config.quotaBytes = $QuotaBytes
    $Config | Add-Member -NotePropertyName parentTTLSeconds -NotePropertyValue $ParentTTLSeconds -Force
    Write-JsonReplaceDurable -LiteralPath $ConfigPath -Value $Config
    Write-JsonFile -LiteralPath $ArtifactPath -Value ([ordered]@{ before = $Before; after = $Config; configSHA256 = (Get-FileHash $ConfigPath -Algorithm SHA256).Hash })
    Start-Service -Name $ServiceName -ErrorAction Stop
    $Running = Wait-ServiceState 'Running' 60
    if ($null -eq $Running) { throw 'Broker service did not restart after exact policy update.' }
    return $Running
}

function New-FixtureCopy {
    param([string]$Template, [string]$Destination)
    New-Item -ItemType Directory -Path $Destination | Out-Null
    foreach ($Name in @('Assets', 'Packages', 'ProjectSettings')) {
        Copy-Item -LiteralPath (Join-Path $Template $Name) -Destination $Destination -Recurse
    }
}

function New-Config {
    param([string]$Path, [string]$Project, [string]$ResultRoot, [string]$Editor, [string]$Store)
    Write-JsonFile -LiteralPath $Path -Value ([ordered]@{
        schema_version = '1'
        unity_path = $Editor
        project_path = $Project
        test_platform = 'edit_mode'
        timeout = [ordered]@{ total_ms = 900000 }
        result_dir = $ResultRoot
        workspace = [ordered]@{ backend = 'vhdx-diff'; store_root = $Store; store_max_allocated_bytes = 34359738368; minimum_host_free_bytes = 21474836480 }
    })
}

if (-not $InstallApproved) { throw 'Pass -InstallApproved after reviewing the unique store and cleanup contract.' }
if (-not $QuotaMutationApproved) { throw 'Pass -QuotaMutationApproved to update only this harness-owned broker config while its service is stopped.' }
if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }
if (-not (Test-Path -LiteralPath $UnityEditorPath -PathType Leaf)) { throw "Unity Editor was not found: $UnityEditorPath" }

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$FixtureTemplate = Join-Path $RepositoryRoot 'testdata\unity-vhdx-fixture'
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-quota-lru-retained-$Stamp"
$StoreRoot = Join-Path $env:ProgramData "TestPlay\VHDXDiffQuotaLRU-$Stamp"
$WorkspaceRoot = Join-Path $env:LOCALAPPDATA 'TestPlay\Workspaces'
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-quota-lru.exe'
$FixtureA = Join-Path $ArtifactRoot 'fixture-a'
$FixtureB = Join-Path $ArtifactRoot 'fixture-b'
$ConfigA = Join-Path $ArtifactRoot 'testplay-a.json'
$ConfigB = Join-Path $ArtifactRoot 'testplay-b.json'
$UserSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$UserStoreRoot = Join-Path $StoreRoot $UserSID
$ParentRoot = Join-Path $UserStoreRoot 'parents'
$RetainedRoot = Join-Path $UserStoreRoot 'retained'
$LeaseRoot = Join-Path $UserStoreRoot 'leases'
$Started = Get-Date
$Installed = $false
$Uninstalled = $false
$CleanupState = 'not-started'
$Failure = $null
$RunA = $null
$RunB = $null
$RunAID = $null
$ParentKeyA = $null
$ParentKeyB = $null
$Timeline = [ordered]@{}

if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) { throw "$ServiceName already exists; refusing replacement." }
foreach ($Path in @($ReceiptPath, $StoreRoot, $ArtifactRoot, $WorkspaceRoot)) {
    if (Test-Path -LiteralPath $Path) { throw "Pre-existing state is outside this harness ownership: $Path" }
}
$PreDisks = @(Get-FileBackedDisks)
$PreLetters = @(Get-DriveLetters)
$PreProcesses = @(Get-RelatedProcesses)
if ($PreDisks.Count -ne 0 -or $PreProcesses.Count -ne 0) { throw 'Pre-state contains related disks or processes.' }

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
New-FixtureCopy $FixtureTemplate $FixtureA
New-FixtureCopy $FixtureTemplate $FixtureB
New-Config $ConfigA $FixtureA (Join-Path $ArtifactRoot 'results-a') $UnityEditorPath $StoreRoot
New-Config $ConfigB $FixtureB (Join-Path $ArtifactRoot 'results-b') $UnityEditorPath $StoreRoot

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
    $Receipt = [IO.File]::ReadAllText($ReceiptPath) | ConvertFrom-Json
    if ([string]$Receipt.storeRoot -ne $StoreRoot -or [string]$Receipt.workspaceRoot -ne $WorkspaceRoot -or [string]$Receipt.userSid -ne $UserSID) { throw 'Install receipt identity mismatch.' }

    $env:TESTPLAY_UNITY_FIXTURE_MARKER = "quota-retained-a-$Stamp"
    $KeepA = Invoke-NativeCapture $ExecutablePath @('--config', $ConfigA, 'run', '--filter', $ExpectedTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--keep-workspace', '--no-bridge') (Join-Path $ArtifactRoot 'run-a-retained.txt') $ArtifactRoot
    if ($KeepA.ExitCode -ne 0) { throw "retained fixture A failed: exit=$($KeepA.ExitCode)" }
    $RunA = Read-NativeJson (Join-Path $ArtifactRoot 'run-a-retained.txt')
    $RunAID = [string]$RunA.run_id
    $ParentKeyA = [string]$RunA.workspace_metrics.parentKey
    if ([string]::IsNullOrWhiteSpace($RunAID) -or [string]::IsNullOrWhiteSpace($ParentKeyA) -or [bool]$RunA.workspace_metrics.fallbackUsed) { throw 'Retained run A evidence is incomplete.' }

    $StatusRetained = Invoke-NativeCapture $ExecutablePath @('storage', 'status', '--json') (Join-Path $ArtifactRoot 'status-retained-default.json') $ArtifactRoot
    if ($StatusRetained.ExitCode -ne 0) { throw 'Default retained status failed.' }
    $RetainedDefault = Read-NativeJson (Join-Path $ArtifactRoot 'status-retained-default.json')
    if ([int]$RetainedDefault.parentCount -ne 1 -or [int]$RetainedDefault.retainedChildCount -ne 1) { throw 'Retained default counts are incorrect.' }

    $LowPolicy = Set-ExactBrokerPolicy ([string]$Receipt.configPath) $StoreRoot $WorkspaceRoot $UserSID 1 1 (Join-Path $ArtifactRoot 'policy-low-retained.json')
    Start-Sleep -Seconds 2
    $DryProtected = Invoke-NativeCapture $ExecutablePath @('storage', 'gc', '--dry-run') (Join-Path $ArtifactRoot 'gc-retained-dry-run.json') $ArtifactRoot
    if ($DryProtected.ExitCode -ne 0) { throw 'Retained dry-run GC failed.' }
    $DryProtectedJSON = Read-NativeJson (Join-Path $ArtifactRoot 'gc-retained-dry-run.json')
    if ([long]$DryProtectedJSON.metrics.capacity.reclaimableBytes -ne 0) { throw 'Retained parent was reported reclaimable.' }
    $GCProtected = Invoke-NativeCapture $ExecutablePath @('storage', 'gc') (Join-Path $ArtifactRoot 'gc-retained-refused.txt') $ArtifactRoot
    if ($GCProtected.ExitCode -eq 0 -or ([IO.File]::ReadAllText((Join-Path $ArtifactRoot 'gc-retained-refused.txt')) -notmatch 'storage capacity unavailable')) { throw 'Retained parent GC was not refused by quota pressure.' }

    $RejectedRun = Invoke-NativeCapture $ExecutablePath @('--config', $ConfigA, 'run', '--filter', $ExpectedTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge') (Join-Path $ArtifactRoot 'quota-admission-refused.txt') $ArtifactRoot
    $RejectedText = [IO.File]::ReadAllText((Join-Path $ArtifactRoot 'quota-admission-refused.txt'))
    if ($RejectedRun.ExitCode -eq 0 -or $RejectedText -notmatch 'storage-capacity-unavailable') { throw 'Quota admission did not fail explicitly.' }
    $StatusProtected = Invoke-NativeCapture $ExecutablePath @('storage', 'status', '--json') (Join-Path $ArtifactRoot 'status-retained-protected.json') $ArtifactRoot
    $RetainedProtected = Read-NativeJson (Join-Path $ArtifactRoot 'status-retained-protected.json')
    if ([int]$RetainedProtected.parentCount -ne 1 -or [int]$RetainedProtected.retainedChildCount -ne 1 -or [int]$RetainedProtected.quarantineCount -ne 0) { throw 'Retained state changed after refused admission/GC.' }

    $HighPolicy = Set-ExactBrokerPolicy ([string]$Receipt.configPath) $StoreRoot $WorkspaceRoot $UserSID 34359738368 1 (Join-Path $ArtifactRoot 'policy-high-retained.json')
    $Attach = Invoke-NativeCapture $ExecutablePath @('workspace', 'attach', $RunAID) (Join-Path $ArtifactRoot 'workspace-attach.json') $ArtifactRoot
    if ($Attach.ExitCode -ne 0) { throw "retained attach failed: exit=$($Attach.ExitCode)" }
    $MarkerPath = Join-Path $WorkspaceRoot "$RunAID\Library\TestPlayVHDX\marker.txt"
    if (-not (Test-Path -LiteralPath $MarkerPath -PathType Leaf) -or [IO.File]::ReadAllText($MarkerPath) -ne $env:TESTPLAY_UNITY_FIXTURE_MARKER) { throw 'Retained child data did not survive detach/attach.' }
    Write-JsonFile (Join-Path $ArtifactRoot 'retained-marker-evidence.json') ([ordered]@{ path = $MarkerPath; value = [IO.File]::ReadAllText($MarkerPath); sha256 = (Get-FileHash $MarkerPath -Algorithm SHA256).Hash })
    $Remove = Invoke-NativeCapture $ExecutablePath @('workspace', 'remove', $RunAID) (Join-Path $ArtifactRoot 'workspace-remove.json') $ArtifactRoot
    if ($Remove.ExitCode -ne 0) { throw "retained remove failed: exit=$($Remove.ExitCode)" }
    if (Test-Path -LiteralPath (Join-Path $WorkspaceRoot $RunAID)) { throw 'Retained workspace remained after exact remove.' }

    Start-Sleep -Seconds 2
    $env:TESTPLAY_UNITY_FIXTURE_MARKER = "quota-lru-b-$Stamp"
    $BuildB = Invoke-NativeCapture $ExecutablePath @('--config', $ConfigB, 'run', '--filter', $ExpectedTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge') (Join-Path $ArtifactRoot 'run-b.txt') $ArtifactRoot
    if ($BuildB.ExitCode -ne 0) { throw "fixture B failed: exit=$($BuildB.ExitCode)" }
    $RunB = Read-NativeJson (Join-Path $ArtifactRoot 'run-b.txt')
    $ParentKeyB = [string]$RunB.workspace_metrics.parentKey
    if ([string]::IsNullOrWhiteSpace($ParentKeyB) -or $ParentKeyB -eq $ParentKeyA -or [bool]$RunB.workspace_metrics.fallbackUsed) { throw 'Fixture B did not create a distinct non-fallback parent.' }
    Start-Sleep -Seconds 2

    $BeforeLRU = Invoke-NativeCapture $ExecutablePath @('storage', 'status', '--json') (Join-Path $ArtifactRoot 'status-before-lru.json') $ArtifactRoot
    $BeforeLRUJSON = Read-NativeJson (Join-Path $ArtifactRoot 'status-before-lru.json')
    if ([int]$BeforeLRUJSON.parentCount -ne 2 -or [int]$BeforeLRUJSON.retainedChildCount -ne 0 -or [int]$BeforeLRUJSON.activeChildCount -ne 0) { throw 'Pre-LRU counts are incorrect.' }
    $DryLRU = Invoke-NativeCapture $ExecutablePath @('storage', 'gc', '--dry-run') (Join-Path $ArtifactRoot 'gc-two-parent-dry-run.json') $ArtifactRoot
    $DryLRUJSON = Read-NativeJson (Join-Path $ArtifactRoot 'gc-two-parent-dry-run.json')
    if ($DryLRU.ExitCode -ne 0 -or [long]$DryLRUJSON.metrics.capacity.reclaimableBytes -le 0) { throw 'Expired parents were not reported reclaimable.' }

    $OneRemovalQuota = [long]$BeforeLRUJSON.parentAllocatedBytes - 1
    if ($OneRemovalQuota -le 0) { throw 'Invalid measured LRU quota.' }
    $LRUPolicy = Set-ExactBrokerPolicy ([string]$Receipt.configPath) $StoreRoot $WorkspaceRoot $UserSID $OneRemovalQuota 1 (Join-Path $ArtifactRoot 'policy-one-removal.json')
    $FirstGC = Invoke-NativeCapture $ExecutablePath @('storage', 'gc') (Join-Path $ArtifactRoot 'gc-oldest-parent.json') $ArtifactRoot
    if ($FirstGC.ExitCode -ne 0) { throw "oldest-parent GC failed: exit=$($FirstGC.ExitCode)" }
    $ParentAPath = Join-Path $ParentRoot $ParentKeyA
    $ParentBPath = Join-Path $ParentRoot $ParentKeyB
    if (Test-Path -LiteralPath $ParentAPath) { throw 'Oldest parent A survived first LRU pass.' }
    if (-not (Test-Path -LiteralPath $ParentBPath -PathType Container)) { throw 'Newer parent B was removed before parent A.' }
    $AfterFirstGC = Invoke-NativeCapture $ExecutablePath @('storage', 'status', '--json') (Join-Path $ArtifactRoot 'status-after-first-gc.json') $ArtifactRoot
    $AfterFirstGCJSON = Read-NativeJson (Join-Path $ArtifactRoot 'status-after-first-gc.json')
    if ([int]$AfterFirstGCJSON.parentCount -ne 1) { throw 'First LRU pass did not leave exactly one parent.' }

    $FinalPolicy = Set-ExactBrokerPolicy ([string]$Receipt.configPath) $StoreRoot $WorkspaceRoot $UserSID 1 1 (Join-Path $ArtifactRoot 'policy-final-gc.json')
    $FinalGC = Invoke-NativeCapture $ExecutablePath @('storage', 'gc') (Join-Path $ArtifactRoot 'gc-final-parent.json') $ArtifactRoot
    if ($FinalGC.ExitCode -ne 0) { throw "final-parent GC failed: exit=$($FinalGC.ExitCode)" }
    $FinalStatus = Invoke-NativeCapture $ExecutablePath @('storage', 'status', '--json') (Join-Path $ArtifactRoot 'status-final-empty.json') $ArtifactRoot
    $FinalStatusJSON = Read-NativeJson (Join-Path $ArtifactRoot 'status-final-empty.json')
    foreach ($Property in @('parentCount', 'activeChildCount', 'retainedChildCount', 'pendingCount', 'quarantineCount')) {
        if ([int]$FinalStatusJSON.$Property -ne 0) { throw "Final status is nonzero: $Property=$($FinalStatusJSON.$Property)" }
    }
    if ([bool]$FinalStatusJSON.manualRecoveryRequired) { throw 'Final status requires manual recovery.' }
    $CleanupState = 'ready-to-uninstall'
}
catch {
    $Failure = $_.Exception.ToString()
    $CleanupState = 'preserved'
}
finally {
    Remove-Item Env:TESTPLAY_UNITY_FIXTURE_MARKER -ErrorAction SilentlyContinue
    if ($Installed) {
        try {
            if ($null -ne $RunAID) {
                $RetainedRecord = Join-Path $RetainedRoot "$RunAID.json"
                if (Test-Path -LiteralPath $RetainedRecord -PathType Leaf) {
                    [void](Invoke-NativeCapture $ExecutablePath @('workspace', 'remove', $RunAID) (Join-Path $ArtifactRoot 'cleanup-workspace-remove.txt') $ArtifactRoot)
                }
            }
            $CleanupStatus = Invoke-NativeCapture $ExecutablePath @('storage', 'status', '--json') (Join-Path $ArtifactRoot 'cleanup-storage-status.json') $ArtifactRoot
            if ($CleanupStatus.ExitCode -eq 0) {
                $CleanupStatusJSON = Read-NativeJson (Join-Path $ArtifactRoot 'cleanup-storage-status.json')
                $ProtectedCount = [int]$CleanupStatusJSON.activeChildCount + [int]$CleanupStatusJSON.retainedChildCount + [int]$CleanupStatusJSON.pendingCount + [int]$CleanupStatusJSON.quarantineCount
                if ($ProtectedCount -eq 0 -and -not [bool]$CleanupStatusJSON.manualRecoveryRequired) {
                    $Uninstall = Invoke-NativeCapture $ExecutablePath @('storage', 'uninstall') (Join-Path $ArtifactRoot 'storage-uninstall.txt') $ArtifactRoot
                    $Uninstalled = $Uninstall.ExitCode -eq 0
                    if ($Uninstalled) { $CleanupState = 'released' }
                }
            }
        }
        catch {
            if ($null -eq $Failure) { $Failure = $_.Exception.ToString() }
        }
    }
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
$ResidualZero = $NewDisks.Count -eq 0 -and $NewLetters.Count -eq 0 -and $NewProcesses.Count -eq 0 -and -not (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) -and -not (Test-Path $ReceiptPath) -and -not (Test-Path $StoreRoot) -and -not (Test-Path $WorkspaceRoot)
if ($Uninstalled -and -not $ResidualZero -and $null -eq $Failure) { $Failure = 'Final outer residual is nonzero.'; $CleanupState = 'uncertain' }
$Passed = $null -eq $Failure -and $Uninstalled -and $ResidualZero
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { 'VHDX_DIFF_QUOTA_LRU_RETAINED_PASS' } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    repository = $RepositoryRoot
    storeRoot = $StoreRoot
    retainedRunId = $RunAID
    parentKeyA = $ParentKeyA
    parentKeyB = $ParentKeyB
    runA = $RunA
    runB = $RunB
    uninstalled = $Uninstalled
    cleanupState = $CleanupState
    residualZero = $ResidualZero
    newFileBackedDisks = @($NewDisks)
    newDriveLetters = @($NewLetters)
    newProcesses = @($NewProcesses)
    failure = $Failure
    notMeasured = @('GNF forced termination', 'eight-worker compatibility', 'performance superiority', 'production readiness', 'release readiness')
}
Write-JsonFile (Join-Path $ArtifactRoot 'summary.json') $Summary
$ZipPath = "$ArtifactRoot.zip"
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash $ZipPath -Algorithm SHA256).Hash
Write-Output "VHDX_DIFF_QUOTA_LRU_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_QUOTA_LRU_VERDICT=$($Summary.verdict)"
Write-Output "VHDX_DIFF_QUOTA_LRU_CLEANUP=$CleanupState"
Write-Output "VHDX_DIFF_QUOTA_LRU_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_QUOTA_LRU_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
