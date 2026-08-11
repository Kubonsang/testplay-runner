[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$FailedArtifactZip,

    [Parameter(Mandatory = $true)]
    [string]$ExpectedArtifactSHA256,

    [switch]$RecoveryApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ServiceName = 'TestPlayStorageBroker'
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
        if ($null -eq $Entry) { throw "ZIP entry is missing: $EntryName" }
        $Reader = [IO.StreamReader]::new($Entry.Open(), [Text.UTF8Encoding]::new($false), $true)
        try { return $Reader.ReadToEnd() | ConvertFrom-Json }
        finally { $Reader.Dispose() }
    }
    finally { $Archive.Dispose() }
}

function Test-SamePath {
    param([string]$Left, [string]$Right)
    if ([string]::IsNullOrWhiteSpace($Left) -or [string]::IsNullOrWhiteSpace($Right)) { return $false }
    return [string]::Equals([IO.Path]::GetFullPath($Left).TrimEnd('\'), [IO.Path]::GetFullPath($Right).TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)
}

function Get-FileBackedDisks {
    return @(Get-Disk -ErrorAction SilentlyContinue | Where-Object { $_.BusType -eq 'File Backed Virtual' } | Select-Object Number, FriendlyName, BusType, PartitionStyle)
}

function Get-RelatedWorkloadProcesses {
    return @(Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -match '^(Unity|testplay-vhdx-diff-broker-restart)$' } | Select-Object Id, ProcessName, StartTime)
}

function Get-ServiceEvidence {
    $Service = Get-CimInstance Win32_Service -Filter "Name = '$ServiceName'" -ErrorAction SilentlyContinue
    if ($null -eq $Service) { return $null }
    return [ordered]@{ name = [string]$Service.Name; state = [string]$Service.State; processId = [int]$Service.ProcessId; startMode = [string]$Service.StartMode; pathName = [string]$Service.PathName }
}

function Assert-NoUnexpectedWorkspaceEntry {
    param([string]$WorkspacePath, [string]$MountPath)
    $AllowedRoot = @('Assets', 'Packages', 'ProjectSettings', 'Library', 'Logs', 'Temp', 'UserSettings', '.testplay', $WorkspaceOwnerFile)
    $Unexpected = @(Get-ChildItem -LiteralPath $WorkspacePath -Force | Where-Object { $AllowedRoot -notcontains $_.Name })
    if ($Unexpected.Count -ne 0) { throw "Unexpected workspace root entries: $($Unexpected.Name -join ', ')" }
    foreach ($Entry in @(Get-ChildItem -LiteralPath $WorkspacePath -Force)) {
        if (Test-SamePath $Entry.FullName $MountPath) { continue }
        if (($Entry.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw "Unexpected workspace reparse entry: $($Entry.FullName)" }
        if ($Entry.PSIsContainer) {
            $Nested = @(Get-ChildItem -LiteralPath $Entry.FullName -Recurse -Force -ErrorAction Stop | Where-Object { ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 })
            if ($Nested.Count -ne 0) { throw "Unexpected nested workspace reparse entry: $($Nested[0].FullName)" }
        }
    }
}

if (-not $RecoveryApproved) { throw 'Pass -RecoveryApproved after reviewing the exact failed artifact and retained ownership identities.' }
if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }
if (-not (Test-Path -LiteralPath $FailedArtifactZip -PathType Leaf)) { throw "Failed artifact ZIP was not found: $FailedArtifactZip" }

$ActualArtifactHash = (Get-FileHash -LiteralPath $FailedArtifactZip -Algorithm SHA256).Hash
if (-not [string]::Equals($ActualArtifactHash, $ExpectedArtifactSHA256, [StringComparison]::OrdinalIgnoreCase)) { throw "Failed artifact SHA-256 mismatch: actual=$ActualArtifactHash expected=$ExpectedArtifactSHA256" }
$FailedSummary = Read-ZipJson -ZipPath $FailedArtifactZip -EntryName 'summary.json'
if ($FailedSummary.status -ne 'FAILED' -or $FailedSummary.cleanupState -ne 'preserved' -or -not $FailedSummary.brokerKilled -or -not $FailedSummary.brokerRestarted -or $FailedSummary.recoveryVerified) { throw 'Failed artifact does not describe the exact preserved broker-restart residual.' }
if ([string]$FailedSummary.failure -notlike '*did not reconcile the exact orphan lease*') { throw 'Failed artifact has an unexpected first failure.' }

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-broker-restart-recovery-$Stamp"
$ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-broker-restart-recovery.exe'
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$ZipPath = "$ArtifactRoot.zip"
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$StoreRoot = [string]$FailedSummary.storeRoot
$WorkspaceRoot = [string]$FailedSummary.workspaceRoot
$CrashJournal = $FailedSummary.crashJournal
$LeasePath = Join-Path (Join-Path (Join-Path $StoreRoot $CrashJournal.userSid) 'leases') "$($CrashJournal.leaseId).json"
$ChildPath = [string]$CrashJournal.childPath
$WorkspacePath = [string]$CrashJournal.workspacePath
$MountPath = [string]$CrashJournal.mountPath
$MarkerPath = Join-Path $WorkspacePath $WorkspaceOwnerFile

if (Test-Path -LiteralPath $ArtifactRoot) { throw "Unique recovery artifact root already exists: $ArtifactRoot" }
New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
$Started = Get-Date
$Failure = $null
$UpgradeSucceeded = $false
$RecoveryVerified = $false
$Uninstalled = $false
$CleanupState = 'preserved'
$PreState = $null
$PostRecoveryStatus = $null
$FinalJournal = $null
$RecoveryShape = $null

Start-Transcript -Path (Join-Path $ArtifactRoot 'terminal-transcript.txt') -Force | Out-Null
try {
    if (-not (Test-Path -LiteralPath $ReceiptPath -PathType Leaf)) { throw 'Authoritative install receipt is missing.' }
    $Receipt = [IO.File]::ReadAllText($ReceiptPath) | ConvertFrom-Json
    if (-not (Test-SamePath $Receipt.storeRoot $StoreRoot) -or -not (Test-SamePath $Receipt.workspaceRoot $WorkspaceRoot) -or $Receipt.userSid -ne $CrashJournal.userSid) { throw 'Install receipt does not match the failed artifact.' }
    $Service = Get-ServiceEvidence
    if ($null -eq $Service -or $Service.state -ne 'Running' -or [int]$Service.processId -le 0) { throw 'Preserved broker service is not running.' }
    $BrokerProcess = Get-CimInstance Win32_Process -Filter "ProcessId = $($Service.processId)" -ErrorAction Stop
    if (-not (Test-SamePath $BrokerProcess.ExecutablePath $Receipt.executable)) { throw 'Running broker executable does not match the receipt.' }
    if (@(Get-RelatedWorkloadProcesses).Count -ne 0) { throw 'A related Unity or harness workload process is still running.' }
    if (@(Get-FileBackedDisks).Count -ne 0) { throw 'A file-backed disk is attached before recovery.' }
    foreach ($Path in @($LeasePath, $WorkspacePath, $MountPath, $MarkerPath)) {
        if (-not (Test-Path -LiteralPath $Path)) { throw "Expected retained path is missing: $Path" }
    }
    $Journal = [IO.File]::ReadAllText($LeasePath) | ConvertFrom-Json
    $Marker = [IO.File]::ReadAllText($MarkerPath) | ConvertFrom-Json
    foreach ($Property in @('leaseId', 'runId', 'workspaceId', 'ownershipToken')) {
        if ([string]$Journal.$Property -ne [string]$CrashJournal.$Property -or [string]$Marker.$Property -ne [string]$CrashJournal.$Property) { throw "Retained ownership mismatch: $Property" }
    }
    foreach ($Property in @('parentPath', 'childPath', 'workspacePath', 'mountPath', 'volumeGuid')) {
        if ([string]$Journal.$Property -ne [string]$CrashJournal.$Property) { throw "Retained journal mismatch: $Property" }
    }
    if ($Journal.state -ne 'quarantined' -or $Journal.retained) { throw "Unexpected retained journal state: $($Journal.state)" }
    $MountItem = Get-Item -LiteralPath $MountPath -Force
    $Images = @()
    if (Test-Path -LiteralPath $ChildPath -PathType Leaf) {
        $RecoveryShape = 'DETACHED_CHILD_WITH_STALE_MOUNT'
        $Images = @(Get-DiskImage -ImagePath $ChildPath -ErrorAction Stop)
        if ($Images.Count -ne 1 -or $Images[0].Attached -or ($MountItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) { throw 'Retained stale mount is not detached or is not a reparse point.' }
        if (@($MountItem.Target).Count -ne 1 -or ([string]$MountItem.Target[0]).IndexOf(([string]$Journal.volumeGuid).TrimStart('\?').TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase) -lt 0) { throw 'Retained stale mount target does not match the journal volume GUID.' }
    }
    else {
        $RecoveryShape = 'PARTIAL_RELEASE_CHILD_ABSENT_EMPTY_MOUNT'
        if (($MountItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or -not $MountItem.PSIsContainer) { throw 'Partial-release mount is not a real directory.' }
        if (@(Get-ChildItem -LiteralPath $MountPath -Force).Count -ne 0) { throw 'Partial-release mount directory is not empty.' }
        if ([string]$Journal.recoveryError -notlike '*Library mount path already exists*') { throw 'Child absence is not explained by the recorded partial-release recovery error.' }
    }
    Assert-NoUnexpectedWorkspaceEntry -WorkspacePath $WorkspacePath -MountPath $MountPath

    Copy-Item -LiteralPath $LeasePath -Destination (Join-Path $ArtifactRoot 'retained-lease-before-upgrade.json')
    Copy-Item -LiteralPath $MarkerPath -Destination (Join-Path $ArtifactRoot 'retained-workspace-owner-before-upgrade.json')
    $PreState = [ordered]@{
        failedArtifactZip = $FailedArtifactZip
        failedArtifactSha256 = $ActualArtifactHash
        service = $Service
        receipt = $Receipt
        journal = $Journal
        marker = $Marker
        journalSha256 = (Get-FileHash $LeasePath -Algorithm SHA256).Hash
        markerSha256 = (Get-FileHash $MarkerPath -Algorithm SHA256).Hash
        recoveryShape = $RecoveryShape
        childSha256 = if (Test-Path -LiteralPath $ChildPath -PathType Leaf) { (Get-FileHash $ChildPath -Algorithm SHA256).Hash } else { $null }
        mountAttributes = [string]$MountItem.Attributes
        mountTarget = @($MountItem.Target)
        childAttached = if ($Images.Count -eq 1) { [bool]$Images[0].Attached } else { $false }
        fileBackedDisks = @(Get-FileBackedDisks)
        relatedWorkloadProcesses = @(Get-RelatedWorkloadProcesses)
    }
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'pre-state.json') -Value $PreState

    Push-Location $RepositoryRoot
    try {
        & go build -o $ExecutablePath .\cmd\testplay
        if ($LASTEXITCODE -ne 0) { throw "go build failed: exit=$LASTEXITCODE" }
    }
    finally { Pop-Location }
    $Upgrade = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'upgrade') -OutputPath (Join-Path $ArtifactRoot 'storage-upgrade.txt') -WorkingDirectory $ArtifactRoot
    if ($Upgrade.ExitCode -ne 0) { throw "storage upgrade failed: exit=$($Upgrade.ExitCode)" }
    $UpgradeSucceeded = $true

    $Deadline = (Get-Date).AddMinutes(3)
    while ((Get-Date) -lt $Deadline) {
        if (-not (Test-Path -LiteralPath $LeasePath) -and -not (Test-Path -LiteralPath $ChildPath) -and -not (Test-Path -LiteralPath $WorkspacePath) -and @(Get-FileBackedDisks).Count -eq 0) {
            $RecoveryVerified = $true
            break
        }
        Start-Sleep -Seconds 1
    }
    if (-not $RecoveryVerified) {
        if (Test-Path -LiteralPath $LeasePath) {
            try { $FinalJournal = [IO.File]::ReadAllText($LeasePath) | ConvertFrom-Json }
            catch { $FinalJournal = [ordered]@{ decodeError = $_.Exception.Message } }
            Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'retained-lease-after-upgrade.json') -Value $FinalJournal
        }
        throw 'Upgraded broker did not reconcile the exact retained residual within the bound.'
    }

    $Status = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'post-recovery-status.json') -WorkingDirectory $ArtifactRoot
    if ($Status.ExitCode -ne 0) { throw "post-recovery storage status failed: exit=$($Status.ExitCode)" }
    $PostRecoveryStatus = Read-NativeJson (Join-Path $ArtifactRoot 'post-recovery-status.json')
    foreach ($Property in @('activeChildCount', 'retainedChildCount', 'pendingCount', 'quarantineCount')) {
        if ([int]$PostRecoveryStatus.$Property -ne 0) { throw "Post-recovery status is nonzero: $Property=$($PostRecoveryStatus.$Property)" }
    }
    if ($PostRecoveryStatus.manualRecoveryRequired) { throw 'Post-recovery status requires manual recovery.' }

    $Uninstall = Invoke-NativeCapture -LiteralPath $ExecutablePath -ArgumentList @('storage', 'uninstall') -OutputPath (Join-Path $ArtifactRoot 'storage-uninstall.txt') -WorkingDirectory $ArtifactRoot
    if ($Uninstall.ExitCode -ne 0) { throw "storage uninstall failed: exit=$($Uninstall.ExitCode)" }
    $Uninstalled = $true
    $CleanupState = 'released'
}
catch {
    $Failure = $_.Exception.ToString()
}
finally {
    Stop-Transcript | Out-Null
}

$ResidualZero = -not (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) -and -not (Test-Path $ReceiptPath) -and -not (Test-Path $StoreRoot) -and -not (Test-Path $WorkspaceRoot) -and @(Get-FileBackedDisks).Count -eq 0 -and @(Get-RelatedWorkloadProcesses).Count -eq 0
if ($Uninstalled -and -not $ResidualZero -and $null -eq $Failure) { $Failure = 'Final residual is nonzero.' }
$Passed = $null -eq $Failure -and $UpgradeSucceeded -and $RecoveryVerified -and $Uninstalled -and $ResidualZero
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { 'VHDX_DIFF_BROKER_RESTART_RESIDUAL_RECOVERED' } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    failedArtifactZip = $FailedArtifactZip
    failedArtifactSha256 = $ActualArtifactHash
    storeRoot = $StoreRoot
    workspaceRoot = $WorkspaceRoot
    leaseId = [string]$CrashJournal.leaseId
    recoveryShape = $RecoveryShape
    preState = $PreState
    upgradeSucceeded = $UpgradeSucceeded
    recoveryVerified = $RecoveryVerified
    finalJournal = $FinalJournal
    postRecoveryStatus = $PostRecoveryStatus
    uninstalled = $Uninstalled
    cleanupState = $CleanupState
    residualZero = $ResidualZero
    failure = $Failure
}
Write-JsonFile -LiteralPath $SummaryPath -Value $Summary
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash
Write-Output "VHDX_DIFF_BROKER_RESTART_RECOVERY_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_BROKER_RESTART_RECOVERY_VERDICT=$($Summary.verdict)"
Write-Output "VHDX_DIFF_BROKER_RESTART_RECOVERY_CLEANUP=$CleanupState"
Write-Output "VHDX_DIFF_BROKER_RESTART_RECOVERY_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_BROKER_RESTART_RECOVERY_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
