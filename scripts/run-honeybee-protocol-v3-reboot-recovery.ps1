[CmdletBinding()]
param(
    [ValidateSet('Prepare', 'Verify')]
    [string]$Phase = 'Prepare',

    [string]$StatePath,
    [string]$StateSHA256,

    [string]$TestPlayExecutable,
    [string]$ExpectedTestPlaySHA256,
    [string]$ExpectedTestPlayCommit,
    [string]$HoneyBeeRepository,
    [string]$ExpectedHoneyBeeCommit,
    [string]$ExpectedHoneyBeeRuntimeSHA256,
    [string]$WorkspaceStorageExecutable,
    [string]$ExpectedWorkspaceStorageSHA256,
    [string]$WorkspaceStorageContractCommit,
    [string]$AgentExecutable,
    [string]$UnityEditorPath,
    [string]$FixtureSource,
    [string]$BridgePackagePath,
    [string]$ExpectedBridgePackageSHA256,

    [switch]$InstallApproved,
    [switch]$RebootApproved,
    [switch]$CleanupApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'honeybee-protocol-v3-recovery-common.ps1')

$ServiceName = 'TestPlayStorageBroker'
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$PointerPath = Join-Path $env:ProgramData 'TestPlay\honeybee-protocol3-vhdx-reboot-recovery-pointer.json'
$RecoveryHarness = Join-Path $PSScriptRoot 'run-honeybee-protocol-v3-recovery.ps1'

function Test-Administrator {
    $Principal = [Security.Principal.WindowsPrincipal]([Security.Principal.WindowsIdentity]::GetCurrent())
    return $Principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-JsonFile {
    param([string]$LiteralPath, [object]$Value)
    [IO.File]::WriteAllText($LiteralPath, ($Value | ConvertTo-Json -Depth 40) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
}

function Get-SHA256 {
    param([string]$LiteralPath)
    return (Get-FileHash -LiteralPath $LiteralPath -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Test-SamePath {
    param([string]$Left, [string]$Right)
    if ([string]::IsNullOrWhiteSpace($Left) -or [string]::IsNullOrWhiteSpace($Right)) { return $false }
    return [string]::Equals([IO.Path]::GetFullPath($Left).TrimEnd('\'), [IO.Path]::GetFullPath($Right).TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)
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
    [IO.File]::WriteAllLines($OutputPath, [string[]]@($Lines | ForEach-Object ToString), [Text.UTF8Encoding]::new($false))
    return [pscustomobject]@{ ExitCode = $ExitCode; Lines = @($Lines) }
}

function Read-NativeJson {
    param([string]$LiteralPath)
    $Raw = [IO.File]::ReadAllText($LiteralPath)
    $Candidates = @($Raw -split "`r?`n" | Where-Object { $_.TrimStart().StartsWith('{') })
    for ($Index = $Candidates.Count - 1; $Index -ge 0; $Index--) {
        try { return $Candidates[$Index] | ConvertFrom-Json } catch { }
    }
    throw "No complete JSON object in native output: $LiteralPath"
}

function Get-ServiceEvidence {
    $Service = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
    if ($null -eq $Service) { return $null }
    return [ordered]@{ name = $Service.Name; state = $Service.State; processId = [int]$Service.ProcessId; pathName = $Service.PathName; startMode = $Service.StartMode }
}

function Wait-ServiceRunning {
    param([int]$TimeoutSeconds = 90)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $Service = Get-ServiceEvidence
        if ($null -ne $Service -and $Service.state -eq 'Running' -and [int]$Service.processId -gt 0) { return $Service }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $Deadline)
    throw 'Broker service did not become running.'
}

function Wait-ServiceAbsent {
    param([int]$TimeoutSeconds = 30)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        if ($null -eq (Get-ServiceEvidence)) { return }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $Deadline)
    throw 'Broker service did not become absent.'
}

function Get-ProcessIdentity {
    param([int]$ProcessID)
    $Process = Get-CimInstance Win32_Process -Filter "ProcessId=$ProcessID" -ErrorAction SilentlyContinue
    if ($null -eq $Process) { return $null }
    $Created = ([DateTime]$Process.CreationDate).ToUniversalTime()
    return [ordered]@{ processId = [int]$Process.ProcessId; executablePath = [string]$Process.ExecutablePath; processIdentity = 'win32:' + $Created.Ticks; creationTimeUtc = $Created.ToString('o') }
}

function Get-BootEvidence {
    $OS = Get-CimInstance Win32_OperatingSystem -ErrorAction Stop
    return [ordered]@{ computerName = $env:COMPUTERNAME; lastBootUpTime = ([DateTime]$OS.LastBootUpTime).ToUniversalTime().ToString('o') }
}

function Get-FileBackedDisks {
    return @(Get-Disk -ErrorAction SilentlyContinue | Where-Object BusType -eq 'File Backed Virtual' | Select-Object Number, FriendlyName, BusType, PartitionStyle)
}

function Get-DriveLetters {
    return @(Get-Volume -ErrorAction SilentlyContinue | Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_.DriveLetter) } | ForEach-Object { ([string]$_.DriveLetter).ToUpperInvariant() } | Sort-Object -Unique)
}

function Get-RelatedProcesses {
    $Service = Get-ServiceEvidence
    $ServicePID = if ($null -eq $Service) { 0 } else { [int]$Service.processId }
    return @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object {
        $_.ProcessId -ne $PID -and $_.ProcessId -ne $ServicePID -and
        ($_.Name -match '^(Unity|UnityPackageManager|UnityCrashHandler64|UnityShaderCompiler|AssetImportWorker|testplay|opencode)(\.exe)?$' -or
         ($_.Name -match '^(node|powershell)(\.exe)?$' -and ([string]$_.CommandLine -match 'honeybee-protocol3|honeybee.*unity')))
    } | Select-Object ProcessId, Name, ExecutablePath, CommandLine)
}

function Get-TreeDigest {
    param([string]$Root)
    $Builder = [Text.StringBuilder]::new()
    $Files = @(Get-ChildItem -LiteralPath $Root -Recurse -File -Force | Sort-Object FullName)
    [long]$Bytes = 0
    foreach ($File in $Files) {
        $Relative = $File.FullName.Substring($Root.TrimEnd('\').Length).TrimStart('\').Replace('\', '/')
        [void]$Builder.Append($Relative).Append([char]0).Append((Get-SHA256 $File.FullName)).Append([char]0)
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
        assets = Get-TreeDigest (Join-Path $Root 'Assets')
        packages = Get-TreeDigest (Join-Path $Root 'Packages')
        projectSettings = Get-TreeDigest (Join-Path $Root 'ProjectSettings')
        packagesLockSHA256 = Get-SHA256 (Join-Path $Root 'Packages\packages-lock.json')
    }
}

function Assert-SourceEqual {
    param([object]$Before, [object]$After)
    if ($Before.packagesLockSHA256 -ne $After.packagesLockSHA256) { throw 'Source packages lock changed across reboot.' }
    foreach ($Name in @('assets', 'packages', 'projectSettings')) {
        if ($Before.$Name.digest -ne $After.$Name.digest -or [int]$Before.$Name.fileCount -ne [int]$After.$Name.fileCount -or [long]$Before.$Name.logicalBytes -ne [long]$After.$Name.logicalBytes) {
            throw "Source tree changed across reboot: $Name"
        }
    }
}

function Assert-ZeroStatus {
    param([object]$Status, [string]$Label)
    $Value = if ($null -ne $Status.PSObject.Properties['status'] -and $Status.status -isnot [string]) { $Status.status } else { $Status }
    foreach ($Name in @('activeChildCount', 'retainedChildCount', 'pendingCount', 'quarantineCount')) {
        if ([int]$Value.$Name -ne 0) { throw "$Label protected count is nonzero: $Name=$($Value.$Name)" }
    }
    if ([bool]$Value.manualRecoveryRequired) { throw "$Label requires manual recovery." }
    return $Value
}

function Assert-ParentEvidence {
    param([object[]]$Before, [string]$Store)
    $Files = @(Get-ChildItem -LiteralPath $Store -Filter parent.vhdx -File -Recurse -Force -ErrorAction Stop)
    if ($Files.Count -ne $Before.Count) { throw 'Restored parent count changed.' }
    foreach ($Expected in $Before) {
        $Match = @($Files | Where-Object { Test-SamePath $_.FullName ([string]$Expected.path) })
        if ($Match.Count -ne 1 -or (Get-SHA256 $Match[0].FullName) -ne [string]$Expected.sha256 -or [long]$Match[0].Length -ne [long]$Expected.length) {
            throw "Restored parent identity changed: $($Expected.path)"
        }
    }
}

if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }

if ($Phase -eq 'Prepare') {
    if (-not $InstallApproved -or -not $RebootApproved) { throw 'Prepare requires -InstallApproved and -RebootApproved.' }
    $Required = @($TestPlayExecutable, $ExpectedTestPlaySHA256, $ExpectedTestPlayCommit, $HoneyBeeRepository, $ExpectedHoneyBeeCommit,
        $ExpectedHoneyBeeRuntimeSHA256, $WorkspaceStorageExecutable, $ExpectedWorkspaceStorageSHA256, $WorkspaceStorageContractCommit,
        $AgentExecutable, $UnityEditorPath, $FixtureSource, $BridgePackagePath, $ExpectedBridgePackageSHA256)
    if (@($Required | Where-Object { [string]::IsNullOrWhiteSpace([string]$_) }).Count -ne 0) { throw 'Prepare is missing a required pin or path.' }
    if (Test-Path -LiteralPath $PointerPath) { throw "Reboot pointer already exists: $PointerPath" }
    $HarnessSHA256 = Get-SHA256 $PSCommandPath
    $Arguments = @{
        Scenario = 'RebootPrepare'; TestPlayExecutable = $TestPlayExecutable; ExpectedTestPlaySHA256 = $ExpectedTestPlaySHA256
        ExpectedTestPlayCommit = $ExpectedTestPlayCommit; HoneyBeeRepository = $HoneyBeeRepository; ExpectedHoneyBeeCommit = $ExpectedHoneyBeeCommit
        ExpectedHoneyBeeRuntimeSHA256 = $ExpectedHoneyBeeRuntimeSHA256; WorkspaceStorageExecutable = $WorkspaceStorageExecutable
        ExpectedWorkspaceStorageSHA256 = $ExpectedWorkspaceStorageSHA256; WorkspaceStorageContractCommit = $WorkspaceStorageContractCommit
        AgentExecutable = $AgentExecutable; UnityEditorPath = $UnityEditorPath; FixtureSource = $FixtureSource
        BridgePackagePath = $BridgePackagePath; ExpectedBridgePackageSHA256 = $ExpectedBridgePackageSHA256
        InstallApproved = $true; FaultInjectionApproved = $true; RebootPointerPath = $PointerPath
        RebootHarnessPath = $PSCommandPath; ExpectedRebootHarnessSHA256 = $HarnessSHA256
    }
    $Output = @(& $RecoveryHarness @Arguments)
    $Output | Write-Output
    $StateLine = @($Output | Where-Object { $_ -like 'HONEYBEE_PROTOCOL3_REBOOT_STATE=*' })
    $HashLine = @($Output | Where-Object { $_ -like 'HONEYBEE_PROTOCOL3_REBOOT_STATE_SHA256=*' })
    if ($StateLine.Count -ne 1 -or $HashLine.Count -ne 1) { throw 'Reboot prepare did not emit one durable state contract.' }
    $PreparedState = $StateLine[0].Substring($StateLine[0].IndexOf('=') + 1)
    $PreparedHash = $HashLine[0].Substring($HashLine[0].IndexOf('=') + 1)
    Write-Output "HONEYBEE_PROTOCOL3_REBOOT_VERIFY_COMMAND=powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`" -Phase Verify -StatePath `"$PreparedState`" -StateSHA256 $PreparedHash -CleanupApproved"
    exit 0
}

if (-not $CleanupApproved) { throw 'Verify requires -CleanupApproved for exact ownership-safe cleanup and old broker restoration.' }
if (-not (Test-Path -LiteralPath $StatePath -PathType Leaf)) { throw "Reboot state is absent: $StatePath" }
$ActualStateSHA256 = Get-SHA256 $StatePath
if ([string]::IsNullOrWhiteSpace($StateSHA256) -or $ActualStateSHA256 -ne $StateSHA256.ToLowerInvariant()) { throw 'Reboot state SHA-256 mismatch.' }
$Contract = [IO.File]::ReadAllText($StatePath) | ConvertFrom-Json
if ([int]$Contract.schemaVersion -ne 1 -or [string]$Contract.phase -ne 'prepared') { throw 'Unsupported reboot state contract.' }
$ArtifactRoot = [string]$Contract.artifactRoot
if (-not (Test-SamePath (Split-Path -Parent $StatePath) $ArtifactRoot) -or -not (Test-SamePath $Contract.pointerPath $PointerPath)) { throw 'State path or pointer is outside the recorded contract.' }
if (-not (Test-Path -LiteralPath $PointerPath -PathType Leaf)) { throw 'Durable reboot pointer is absent.' }
$Pointer = [IO.File]::ReadAllText($PointerPath) | ConvertFrom-Json
$HarnessSHA256 = Get-SHA256 $PSCommandPath
if (-not (Test-Protocol3RebootPointer $Pointer $StatePath $ActualStateSHA256 $PSCommandPath $HarnessSHA256)) { throw 'Durable reboot pointer identity mismatch.' }
if ([string]$Contract.rebootHarnessSHA256 -ne $HarnessSHA256 -or -not (Test-SamePath $Contract.rebootHarnessPath $PSCommandPath)) { throw 'Recorded reboot harness identity mismatch.' }

$Failure = $null
$CleanupState = 'preserved'
$RecoveryVerified = $false
$OldRestored = $false
$NewRemoved = $false
$StatusEvidence = $null
$OldStatusAfter = $null
$Started = Get-Date
$BootAfter = Get-BootEvidence
Start-Transcript -Path (Join-Path $ArtifactRoot 'verify-terminal-transcript.txt') -Force | Out-Null
try {
    if ($BootAfter.computerName -ne $Contract.bootBefore.computerName -or $BootAfter.lastBootUpTime -eq $Contract.bootBefore.lastBootUpTime) { throw 'Required Windows reboot was not observed on the same computer.' }
    foreach ($Identity in @($Contract.fault.honeyBee, $Contract.fault.capability, $Contract.fault.unity)) {
        $Current = Get-ProcessIdentity ([int]$Identity.processId)
        if ($null -ne $Current -and $Current.processIdentity -eq [string]$Identity.processIdentity) { throw "Pre-reboot owned process survived: $($Identity.processId)" }
    }
    $Service = Wait-ServiceRunning
    if ($Service.startMode -ne 'Auto') { throw 'Broker did not auto-start after reboot.' }
    if (-not (Test-Path -LiteralPath $Contract.newReceipt.executable -PathType Leaf) -or (Get-SHA256 $Contract.newReceipt.executable) -ne (Get-SHA256 $Contract.testplayExecutable)) { throw 'Installed broker identity changed across reboot.' }
    $Broker = Get-ProcessIdentity ([int]$Service.processId)
    if ($null -eq $Broker -or -not (Test-SamePath $Broker.executablePath $Contract.newReceipt.executable)) { throw 'Post-reboot broker process identity mismatch.' }

    $Deadline = (Get-Date).AddMinutes(3)
    do {
        $JournalGone = -not (Test-Path -LiteralPath $Contract.leaseJournalPath)
        $ChildGone = -not (Test-Path -LiteralPath $Contract.lease.childPath)
        $WorkspaceGone = -not (Test-Path -LiteralPath $Contract.lease.workspacePath)
        $DisksGone = @(Get-FileBackedDisks).Count -eq 0
        if ($JournalGone -and $ChildGone -and $WorkspaceGone -and $DisksGone) { $RecoveryVerified = $true; break }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $Deadline)
    if (-not $RecoveryVerified) { throw 'Broker did not reconcile the exact Protocol 3 reboot orphan within the bound.' }

    $StatusCapture = Invoke-NativeCapture $Contract.testplayExecutable @('storage', 'status', '--json') (Join-Path $ArtifactRoot 'post-reboot-storage-status.json') $ArtifactRoot
    if ($StatusCapture.ExitCode -ne 0) { throw 'Post-reboot storage status failed.' }
    $StatusEvidence = Read-NativeJson (Join-Path $ArtifactRoot 'post-reboot-storage-status.json')
    $StatusValue = Assert-ZeroStatus $StatusEvidence 'Post-reboot broker'
    if ([int]$StatusValue.parentCount -ne 1 -or [string]$StatusValue.bootSessionId -eq [string]$Contract.lease.bootSessionId) { throw 'Post-reboot parent count or boot-session identity is invalid.' }
    if ((Get-SHA256 $Contract.parentBefore.path) -ne [string]$Contract.parentBefore.sha256 -or [long](Get-Item $Contract.parentBefore.path).Length -ne [long]$Contract.parentBefore.length) { throw 'Immutable parent changed across reboot.' }
    $SourceAfter = Get-SourceEvidence $Contract.sourceProject
    Assert-SourceEqual $Contract.sourceBefore $SourceAfter
    Write-JsonFile (Join-Path $ArtifactRoot 'source-after-reboot.json') $SourceAfter

    $Uninstall = Invoke-NativeCapture $Contract.testplayExecutable @('storage', 'uninstall') (Join-Path $ArtifactRoot 'new-storage-uninstall.txt') $ArtifactRoot
    if ($Uninstall.ExitCode -ne 0) { throw 'Temporary broker uninstall failed.' }
    Wait-ServiceAbsent
    $NewRemoved = -not (Test-Path -LiteralPath $Contract.storeRoot) -and -not (Test-Path -LiteralPath $ReceiptPath)
    if (-not $NewRemoved) { throw 'Temporary broker store or receipt remained after uninstall.' }

    if (-not (Test-Path -LiteralPath $Contract.preservedReceiptPath -PathType Leaf) -or (Get-SHA256 $Contract.preservedReceiptPath) -ne [string]$Contract.oldReceiptSHA256) { throw 'Preserved old receipt identity changed.' }
    if ((Get-SHA256 $Contract.oldReceipt.executable) -ne [string]$Contract.oldExecutableSHA256) { throw 'Old broker executable identity changed.' }
    Assert-ParentEvidence @($Contract.oldParentBefore) $Contract.oldReceipt.storeRoot
    $OldWorkspace = [string]$Contract.oldReceipt.workspaceRoot
    $OldWorkspaceParent = Split-Path -Parent $OldWorkspace
    if (-not (Test-Path -LiteralPath $OldWorkspaceParent -PathType Container) -or ((Get-Item $OldWorkspaceParent -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Old workspace parent is unsafe.' }
    if (-not (Test-Path -LiteralPath $OldWorkspace)) { New-Item -ItemType Directory -Path $OldWorkspace | Out-Null }
    if (@(Get-ChildItem -LiteralPath $OldWorkspace -Force).Count -ne 0 -or ((Get-Item $OldWorkspace -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Old workspace root is not an empty real directory.' }
    $ACL = Invoke-NativeCapture 'icacls.exe' @($OldWorkspace, '/inheritance:r', '/grant:r', '*S-1-5-18:(OI)(CI)F', '*S-1-5-32-544:(OI)(CI)F', "*$($Contract.oldReceipt.userSid):(OI)(CI)M") (Join-Path $ArtifactRoot 'old-workspace-restore-acl.txt') $ArtifactRoot
    if ($ACL.ExitCode -ne 0) { throw 'Old workspace ACL restore failed.' }
    Move-Item -LiteralPath $Contract.preservedReceiptPath -Destination $ReceiptPath
    if ((Get-SHA256 $ReceiptPath) -ne [string]$Contract.oldReceiptSHA256) { throw 'Authoritative old receipt changed during restore.' }
    $Restore = Invoke-NativeCapture $Contract.oldReceipt.executable @('storage', 'install', '--root', [string]$Contract.oldReceipt.storeRoot) (Join-Path $ArtifactRoot 'old-storage-restore.txt') $ArtifactRoot
    if ($Restore.ExitCode -ne 0) { throw 'Old broker restore failed.' }
    $OldService = Wait-ServiceRunning
    $OldBroker = Get-ProcessIdentity ([int]$OldService.processId)
    if ($null -eq $OldBroker -or -not (Test-SamePath $OldBroker.executablePath $Contract.oldReceipt.executable)) { throw 'Restored old broker process identity mismatch.' }
    $OldStatusCapture = Invoke-NativeCapture $Contract.oldReceipt.executable @('storage', 'status', '--json') (Join-Path $ArtifactRoot 'old-storage-status-after.json') $ArtifactRoot
    if ($OldStatusCapture.ExitCode -ne 0) { throw 'Restored old broker status failed.' }
    $OldStatusAfter = Read-NativeJson (Join-Path $ArtifactRoot 'old-storage-status-after.json')
    $OldStatusValue = Assert-ZeroStatus $OldStatusAfter 'Restored old broker'
    if ([int]$OldStatusValue.parentCount -ne @($Contract.oldParentBefore).Count) { throw 'Restored old broker parent count changed.' }
    $OldRestored = $true
    $CleanupState = 'released'
}
catch {
    $Failure = $_.Exception.ToString()
    $CleanupState = Get-Protocol3CleanupState $false $true (@(Get-FileBackedDisks).Count -eq 0) ((Test-Path -LiteralPath $Contract.storeRoot) -or (Test-Path -LiteralPath $Contract.oldReceipt.storeRoot))
}
finally { Stop-Transcript | Out-Null }

$PostDisks = @(Get-FileBackedDisks)
$PostLetters = @(Get-DriveLetters)
$PostProcesses = @(Get-RelatedProcesses)
$DriveLettersEqual = (@($Contract.preState.driveLetters) -join ',') -eq ($PostLetters -join ',')
$ResidualZero = $OldRestored -and $NewRemoved -and $PostDisks.Count -eq 0 -and $PostProcesses.Count -eq 0 -and $DriveLettersEqual -and
    -not (Test-Path -LiteralPath $Contract.storeRoot) -and -not (Test-Path -LiteralPath $Contract.preservedReceiptPath)
$Passed = $null -eq $Failure -and $RecoveryVerified -and $ResidualZero
if ($Passed) {
    $CurrentPointer = [IO.File]::ReadAllText($PointerPath) | ConvertFrom-Json
    if (-not (Test-Protocol3RebootPointer $CurrentPointer $StatePath $ActualStateSHA256 $PSCommandPath $HarnessSHA256)) { throw 'Pointer changed before exact removal.' }
    Remove-Item -LiteralPath $PointerPath -Force
    $ResidualZero = $ResidualZero -and -not (Test-Path -LiteralPath $PointerPath)
    $Passed = $ResidualZero
}
if (-not $Passed -and $CleanupState -eq 'released') { $CleanupState = 'preserved' }
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { 'HONEYBEE_PROTOCOL3_REBOOT_RECOVERY_PASS' } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    statePath = $StatePath
    stateSHA256 = $ActualStateSHA256
    bootBefore = $Contract.bootBefore
    bootAfter = $BootAfter
    recoveryVerified = $RecoveryVerified
    sourceUnchanged = $null -eq $Failure -or $Failure -notmatch 'Source tree|packages lock'
    parentUnchanged = $null -eq $Failure -or $Failure -notmatch 'Immutable parent'
    newBrokerRemoved = $NewRemoved
    oldBrokerRestored = $OldRestored
    recoveredStorageStatus = $StatusEvidence
    oldBrokerStatusAfter = $OldStatusAfter
    cleanupState = $CleanupState
    manualRecoveryRequired = $CleanupState -eq 'uncertain'
    residualZero = $ResidualZero
    fileBackedDisks = @($PostDisks)
    driveLetters = @($PostLetters)
    relatedProcesses = @($PostProcesses)
    failure = $Failure
}
Write-JsonFile (Join-Path $ArtifactRoot 'summary.json') $Summary
$ZipPath = "$ArtifactRoot.zip"
if (Test-Path -LiteralPath $ZipPath) { throw "Refusing to overwrite artifact ZIP: $ZipPath" }
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath
$ZipSHA256 = Get-SHA256 $ZipPath
Write-Output "HONEYBEE_PROTOCOL3_REBOOT_STATUS=$($Summary.status)"
Write-Output "HONEYBEE_PROTOCOL3_REBOOT_VERDICT=$($Summary.verdict)"
Write-Output "HONEYBEE_PROTOCOL3_REBOOT_CLEANUP=$CleanupState"
Write-Output "HONEYBEE_PROTOCOL3_REBOOT_ARTIFACT_ZIP=$ZipPath"
Write-Output "HONEYBEE_PROTOCOL3_REBOOT_ARTIFACT_SHA256=$ZipSHA256"
if (-not $Passed) { Write-Error $Failure; exit 1 }
