[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('ClientTermination', 'UnityTermination', 'BrokerRestart', 'RebootPrepare')]
    [string]$Scenario,

    [Parameter(Mandatory = $true)]
    [string]$TestPlayExecutable,

    [Parameter(Mandatory = $true)]
    [string]$ExpectedTestPlaySHA256,

    [Parameter(Mandatory = $true)]
    [string]$ExpectedTestPlayCommit,

    [Parameter(Mandatory = $true)]
    [string]$HoneyBeeRepository,

    [Parameter(Mandatory = $true)]
    [string]$ExpectedHoneyBeeCommit,

    [Parameter(Mandatory = $true)]
    [string]$ExpectedHoneyBeeRuntimeSHA256,

    [Parameter(Mandatory = $true)]
    [string]$WorkspaceStorageExecutable,

    [Parameter(Mandatory = $true)]
    [string]$ExpectedWorkspaceStorageSHA256,

    [Parameter(Mandatory = $true)]
    [string]$WorkspaceStorageContractCommit,

    [Parameter(Mandatory = $true)]
    [string]$AgentExecutable,

    [Parameter(Mandatory = $true)]
    [string]$UnityEditorPath,

    [Parameter(Mandatory = $true)]
    [string]$FixtureSource,

    [Parameter(Mandatory = $true)]
    [string]$BridgePackagePath,

    [Parameter(Mandatory = $true)]
    [string]$ExpectedBridgePackageSHA256,

    [switch]$InstallApproved,

    [switch]$FaultInjectionApproved,

    [string]$RebootPointerPath,

    [string]$RebootHarnessPath,

    [string]$ExpectedRebootHarnessSHA256
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'honeybee-protocol-v3-recovery-common.ps1')

$ExpectedUnityVersion = '6000.3.8f1'
$PackageName = 'com.testplay.bridge'
$ProvisionTest = 'TestPlayFixture.Tests.DeterministicPlayModeTests.DeterministicPlayModeSmokeTest'
$WarmTest = 'TestPlayFixture.Tests.LibraryMountTests.RebootRecoveryHoldTest'
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$ServiceName = 'TestPlayStorageBroker'
$Timestamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ScenarioSlug = ($Scenario -creplace '([a-z0-9])([A-Z])', '$1-$2').ToLowerInvariant()
$ArtifactRoot = Join-Path $env:TEMP "testplay-honeybee-protocol3-$ScenarioSlug-$Timestamp"
$SessionRoot = Join-Path $env:LOCALAPPDATA "TestPlay\HoneyBeeProtocol3Recovery-$Scenario-$Timestamp"
$StoreRoot = Join-Path $SessionRoot 'store'
$SourceProject = Join-Path $ArtifactRoot 'source-project'
$HoneyBeeRunRoot = Join-Path $ArtifactRoot 'honeybee-run'
$PreservedReceiptPath = Join-Path (Split-Path -Parent $ReceiptPath) "storage-install.protocol3-recovery-preserved-$Timestamp.json"
$ZipPath = "$ArtifactRoot.zip"
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$CompletionPath = Join-Path $env:TEMP "testplay-honeybee-protocol3-$ScenarioSlug-$Timestamp-complete.json"
$GateReadyPath = Join-Path $ArtifactRoot 'warm-test-ready.txt'
$GateReleasePath = Join-Path $ArtifactRoot 'warm-test-release.txt'
$WorkspaceOwnerFile = '.testplay-vhdx-workspace-owner.json'

function Test-Administrator {
    $Principal = [Security.Principal.WindowsPrincipal]([Security.Principal.WindowsIdentity]::GetCurrent())
    return $Principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-JsonFile {
    param([string]$LiteralPath, [object]$Value)
    $Json = $Value | ConvertTo-Json -Depth 30
    [IO.File]::WriteAllText($LiteralPath, $Json + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
}

function Write-TextFile {
    param([string]$LiteralPath, [string]$Value)
    [IO.File]::WriteAllText($LiteralPath, $Value, [Text.UTF8Encoding]::new($false))
}

function Write-DurableJsonExclusive {
    param([string]$LiteralPath, [object]$Value)
    $Parent = Split-Path -Parent $LiteralPath
    if (-not (Test-Path -LiteralPath $Parent -PathType Container)) { throw "Durable JSON parent is absent: $Parent" }
    $Bytes = [Text.UTF8Encoding]::new($false).GetBytes(($Value | ConvertTo-Json -Depth 40) + [Environment]::NewLine)
    $Stream = [IO.FileStream]::new($LiteralPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
    try {
        $Stream.Write($Bytes, 0, $Bytes.Length)
        $Stream.Flush($true)
    }
    finally { $Stream.Dispose() }
    $ReadBack = [IO.File]::ReadAllBytes($LiteralPath)
    if ($ReadBack.Length -ne $Bytes.Length) { throw "Durable JSON read-back length mismatch: $LiteralPath" }
    for ($Index = 0; $Index -lt $Bytes.Length; $Index++) {
        if ($ReadBack[$Index] -ne $Bytes[$Index]) { throw "Durable JSON read-back mismatch: $LiteralPath" }
    }
}

function Get-NormalizedSHA256 {
    param([string]$LiteralPath)
    return (Get-FileHash -LiteralPath $LiteralPath -Algorithm SHA256).Hash.ToLowerInvariant()
}

function New-ArtifactArchive {
    param(
        [string]$ArtifactDirectory,
        [string]$DestinationPath,
        [int]$Attempts = 60,
        [int]$DelayMilliseconds = 500
    )
    $LastError = $null
    for ($Attempt = 1; $Attempt -le $Attempts; $Attempt++) {
        try {
            if (Test-Path -LiteralPath $DestinationPath) {
                Remove-Item -LiteralPath $DestinationPath -Force
            }
            Compress-Archive `
                -Path (Join-Path $ArtifactDirectory '*') `
                -DestinationPath $DestinationPath `
                -Force `
                -ErrorAction Stop
            return [pscustomobject]@{ Success = $true; Attempts = $Attempt; Error = $null }
        }
        catch {
            $LastError = $_.Exception.ToString()
            if ($Attempt -lt $Attempts) { Start-Sleep -Milliseconds $DelayMilliseconds }
        }
    }
    return [pscustomobject]@{ Success = $false; Attempts = $Attempts; Error = $LastError }
}

function Assert-FileSHA256 {
    param([string]$LiteralPath, [string]$Expected, [string]$Label)
    if (-not (Test-Path -LiteralPath $LiteralPath -PathType Leaf)) {
        throw "$Label was not found: $LiteralPath"
    }
    $Actual = Get-NormalizedSHA256 -LiteralPath $LiteralPath
    if ($Actual -ne $Expected.ToLowerInvariant()) {
        throw "$Label SHA-256 mismatch: expected=$Expected actual=$Actual path=$LiteralPath"
    }
    return $Actual
}

function Invoke-NativeCapture {
    param(
        [string]$LiteralPath,
        [string[]]$ArgumentList,
        [string]$OutputPath,
        [string]$WorkingDirectory
    )
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
    $Candidates = @($Raw -split "`r?`n" | Where-Object { $_.TrimStart().StartsWith('{') })
    for ($Index = $Candidates.Count - 1; $Index -ge 0; $Index--) {
        try { return $Candidates[$Index] | ConvertFrom-Json }
        catch { }
    }
    $Start = $Raw.IndexOf('{')
    if ($Start -ge 0) {
        try { return $Raw.Substring($Start) | ConvertFrom-Json }
        catch { }
    }
    throw "No complete JSON object in native output: $LiteralPath"
}

function Get-GitText {
    param([string]$Repository, [string[]]$Arguments)
    $Lines = @(& git -C $Repository @Arguments 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "git failed in ${Repository}: $($Lines -join [Environment]::NewLine)"
    }
    return ($Lines -join [Environment]::NewLine).Trim()
}

function Get-TreeDigest {
    param([string]$Root)
    if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
        throw "Tree root is missing: $Root"
    }
    $Builder = [Text.StringBuilder]::new()
    $Files = @(Get-ChildItem -LiteralPath $Root -Recurse -File -Force | Sort-Object FullName)
    [long]$LogicalBytes = 0
    foreach ($File in $Files) {
        $Relative = $File.FullName.Substring($Root.TrimEnd('\').Length).TrimStart('\').Replace('\', '/')
        $Hash = Get-NormalizedSHA256 -LiteralPath $File.FullName
        [void]$Builder.Append($Relative).Append([char]0).Append($Hash).Append([char]0)
        $LogicalBytes += $File.Length
    }
    $SHA = [Security.Cryptography.SHA256]::Create()
    try {
        $Digest = ([BitConverter]::ToString(
            $SHA.ComputeHash([Text.Encoding]::UTF8.GetBytes($Builder.ToString()))
        )).Replace('-', '').ToLowerInvariant()
    }
    finally { $SHA.Dispose() }
    return [ordered]@{ digest = $Digest; fileCount = $Files.Count; logicalBytes = $LogicalBytes }
}

function Get-HoneyBeeRuntimeEvidence {
    param([string]$Repository)
    $Roots = @((Join-Path $Repository 'apps\cli\dist'))
    $Roots += @(Get-ChildItem -LiteralPath (Join-Path $Repository 'packages') -Directory -Force |
        ForEach-Object { Join-Path $_.FullName 'dist' } |
        Where-Object { Test-Path -LiteralPath $_ -PathType Container })
    foreach ($Root in $Roots) {
        if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
            throw "HoneyBee runtime root is missing: $Root"
        }
    }
    $Files = @($Roots | ForEach-Object { Get-ChildItem -LiteralPath $_ -Recurse -File -Force })
    if ($Files.Count -eq 0) { throw 'HoneyBee built runtime is empty.' }
    $Builder = [Text.StringBuilder]::new()
    $Records = [Collections.Generic.List[string]]::new()
    [long]$LogicalBytes = 0
    foreach ($File in $Files) {
        $Relative = $File.FullName.Substring($Repository.TrimEnd('\').Length).TrimStart('\').Replace('\', '/')
        $Hash = Get-NormalizedSHA256 -LiteralPath $File.FullName
        $Records.Add($Relative + [char]0 + $Hash + [char]0)
        $LogicalBytes += $File.Length
    }
    $Records.Sort([StringComparer]::Ordinal)
    foreach ($Record in $Records) { [void]$Builder.Append($Record) }
    $SHA = [Security.Cryptography.SHA256]::Create()
    try {
        $Digest = ([BitConverter]::ToString(
            $SHA.ComputeHash([Text.Encoding]::UTF8.GetBytes($Builder.ToString()))
        )).Replace('-', '').ToLowerInvariant()
    }
    finally { $SHA.Dispose() }
    return [ordered]@{ digest = $Digest; fileCount = $Records.Count; logicalBytes = $LogicalBytes }
}

function Get-SourceEvidence {
    param([string]$Root)
    return [ordered]@{
        assets = Get-TreeDigest -Root (Join-Path $Root 'Assets')
        packages = Get-TreeDigest -Root (Join-Path $Root 'Packages')
        projectSettings = Get-TreeDigest -Root (Join-Path $Root 'ProjectSettings')
        packagesLockSHA256 = Get-NormalizedSHA256 -LiteralPath (Join-Path $Root 'Packages\packages-lock.json')
    }
}

function Assert-SourceEvidenceEqual {
    param([object]$Before, [object]$After)
    if ($Before.packagesLockSHA256 -ne $After.packagesLockSHA256) {
        throw 'Source packages-lock.json changed during HoneyBee E2E.'
    }
    foreach ($Name in @('assets', 'packages', 'projectSettings')) {
        if ($Before.$Name.digest -ne $After.$Name.digest -or
            $Before.$Name.fileCount -ne $After.$Name.fileCount -or
            $Before.$Name.logicalBytes -ne $After.$Name.logicalBytes) {
            throw "Source tree changed during HoneyBee E2E: $Name"
        }
    }
}

function Get-ServiceEvidence {
    $Service = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
    if ($null -eq $Service) { return $null }
    return [ordered]@{
        name = $Service.Name
        state = $Service.State
        processId = [int]$Service.ProcessId
        pathName = $Service.PathName
        startMode = $Service.StartMode
    }
}

function Get-BootEvidence {
    $OS = Get-CimInstance Win32_OperatingSystem -ErrorAction Stop
    return [ordered]@{
        computerName = $env:COMPUTERNAME
        lastBootUpTime = ([DateTime]$OS.LastBootUpTime).ToUniversalTime().ToString('o')
    }
}

function Wait-ServiceAbsent {
    param([int]$TimeoutSeconds = 30)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        if ($null -eq (Get-ServiceEvidence)) { return }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $Deadline)
    throw "Service did not become absent: $ServiceName"
}

function Wait-ServiceRunning {
    param([int]$TimeoutSeconds = 30)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $Service = Get-ServiceEvidence
        if ($null -ne $Service -and $Service.state -eq 'Running' -and $Service.processId -gt 0) { return $Service }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $Deadline)
    throw "Service did not become running: $ServiceName"
}

function Test-SamePath {
    param([string]$Left, [string]$Right)
    if ([string]::IsNullOrWhiteSpace($Left) -or [string]::IsNullOrWhiteSpace($Right)) { return $false }
    return [string]::Equals(
        [IO.Path]::GetFullPath($Left).TrimEnd('\'),
        [IO.Path]::GetFullPath($Right).TrimEnd('\'),
        [StringComparison]::OrdinalIgnoreCase)
}

function Get-ProcessIdentity {
    param([int]$ProcessID)
    $Process = Get-CimInstance Win32_Process -Filter "ProcessId=$ProcessID" -ErrorAction SilentlyContinue
    if ($null -eq $Process) { return $null }
    $Created = ([DateTime]$Process.CreationDate).ToUniversalTime()
    return [ordered]@{
        processId = [int]$Process.ProcessId
        parentProcessId = [int]$Process.ParentProcessId
        name = [string]$Process.Name
        executablePath = [string]$Process.ExecutablePath
        commandLine = [string]$Process.CommandLine
        creationTimeUtc = $Created.ToString('o')
        processIdentity = 'win32:' + $Created.Ticks
    }
}

function Stop-ExactProcess {
    param([int]$ProcessID, [string]$ExpectedPath, [string]$ExpectedIdentity)
    $Identity = Get-ProcessIdentity -ProcessID $ProcessID
    if ($null -eq $Identity) { throw "Expected process is absent before fault injection: $ProcessID" }
    if (-not (Test-SamePath -Left $Identity.executablePath -Right $ExpectedPath) -or
        (-not [string]::IsNullOrWhiteSpace($ExpectedIdentity) -and $Identity.processIdentity -ne $ExpectedIdentity)) {
        throw "Process identity changed before fault injection: pid=$ProcessID"
    }
    Stop-Process -Id $ProcessID -Force -ErrorAction Stop
    $Deadline = (Get-Date).AddSeconds(30)
    while ((Get-Date) -lt $Deadline -and $null -ne (Get-Process -Id $ProcessID -ErrorAction SilentlyContinue)) {
        Start-Sleep -Milliseconds 100
    }
    if ($null -ne (Get-Process -Id $ProcessID -ErrorAction SilentlyContinue)) {
        throw "Fault target did not exit: pid=$ProcessID"
    }
    return $Identity
}

function Read-HoneyBeeEvents {
    param([string]$JournalPath)
    $Events = [Collections.Generic.List[object]]::new()
    if (-not (Test-Path -LiteralPath $JournalPath -PathType Leaf)) { return @() }
    foreach ($Line in @(Get-Content -LiteralPath $JournalPath -ErrorAction Stop)) {
        if ([string]::IsNullOrWhiteSpace($Line)) { continue }
        try { $Events.Add(($Line | ConvertFrom-Json)) }
        catch { break }
    }
    return @($Events)
}

function Wait-HoneyBeeFaultPoint {
    param([string]$RunRoot, [string]$ReadyPath, [int]$TimeoutSeconds = 300)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $Deadline) {
        $Journals = @(Get-ChildItem -LiteralPath (Join-Path $RunRoot '.honeybee\runs') -Filter events.jsonl -File -Recurse -ErrorAction SilentlyContinue)
        if ($Journals.Count -gt 1) { throw 'Multiple HoneyBee journals appeared during one qualification run.' }
        if ($Journals.Count -eq 1) {
            $Events = @(Read-HoneyBeeEvents -JournalPath $Journals[0].FullName)
            $Selection = Select-Protocol3FaultEvents -Events $Events -ReadySignalPresent (Test-Path -LiteralPath $ReadyPath -PathType Leaf)
            if ($Selection.ready) {
                return [ordered]@{
                    journalPath = $Journals[0].FullName
                    events = $Events
                    workspace = $Selection.workspace
                    ownership = $Selection.ownership
                    binding = $Selection.binding
                    warmStarted = $Selection.warmStarted
                    warmProcess = $Selection.warmProcess
                }
            }
        }
        Start-Sleep -Milliseconds 100
    }
    throw 'Timed out waiting for the exact warm-test fault point.'
}

function Wait-ProcessExit {
    param([Diagnostics.Process]$Process, [int]$TimeoutSeconds = 300)
    if (-not $Process.WaitForExit($TimeoutSeconds * 1000)) { return $false }
    # Drain redirected streams before reading ExitCode on Windows PowerShell 5.1.
    $Process.WaitForExit()
    $Process.Refresh()
    return $true
}

function Wait-ServiceStopped {
    param([int]$TimeoutSeconds = 30)
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $Service = Get-ServiceEvidence
        if ($null -ne $Service -and $Service.state -eq 'Stopped') { return $Service }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $Deadline)
    throw "Service did not become stopped: $ServiceName"
}

function Get-ExactLeaseEvidence {
    param([string]$LeaseRoot, [object]$FaultPoint, [string]$WorkspaceRoot)
    $WorkspaceID = [string]$FaultPoint.workspace.payload.workspaceId
    $LeaseID = [string]$FaultPoint.workspace.payload.leaseId
    if ([string]::IsNullOrWhiteSpace($WorkspaceID) -or [string]::IsNullOrWhiteSpace($LeaseID)) {
        throw 'HoneyBee workspace event has incomplete workspace or lease identity.'
    }
    $Candidates = @(Get-ChildItem -LiteralPath $LeaseRoot -Filter '*.json' -File -Force -ErrorAction Stop)
    if ($Candidates.Count -ne 1) { throw "Expected one exact lease journal, found $($Candidates.Count)." }
    $Journal = [IO.File]::ReadAllText($Candidates[0].FullName) | ConvertFrom-Json
    if ([string]$Journal.leaseId -ne $LeaseID -or [string]$Journal.workspaceId -ne $WorkspaceID -or
        -not (Test-SamePath $Journal.workspacePath (Join-Path $WorkspaceRoot $WorkspaceID)) -or
        -not (Test-SamePath $Journal.mountPath (Join-Path $Journal.workspacePath 'Library'))) {
        throw 'Lease journal does not match the exact HoneyBee workspace identity.'
    }
    if (-not (Test-Path -LiteralPath $Journal.childPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $Journal.parentPath -PathType Leaf)) {
        throw 'Exact child or parent VHDX is absent at the fault point.'
    }
    $MarkerPath = Join-Path $Journal.workspacePath $WorkspaceOwnerFile
    if (-not (Test-Path -LiteralPath $MarkerPath -PathType Leaf)) { throw 'Workspace ownership marker is absent.' }
    $Marker = [IO.File]::ReadAllText($MarkerPath) | ConvertFrom-Json
    if (-not (Test-Protocol3LeaseIdentity $FaultPoint.workspace $Journal $Marker $WorkspaceRoot)) {
        throw 'Workspace ownership marker or lease identity mismatch.'
    }
    return [ordered]@{
        journalPath = $Candidates[0].FullName
        journal = $Journal
        journalSHA256 = Get-NormalizedSHA256 -LiteralPath $Candidates[0].FullName
        markerPath = $MarkerPath
        marker = $Marker
        markerSHA256 = Get-NormalizedSHA256 -LiteralPath $MarkerPath
    }
}

function Get-FileBackedDisks {
    return @(Get-Disk -ErrorAction SilentlyContinue |
        Where-Object { $_.BusType -eq 'File Backed Virtual' } |
        Select-Object Number, FriendlyName, BusType, PartitionStyle)
}

function Get-DriveLetters {
    return @(Get-Volume -ErrorAction SilentlyContinue |
        Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_.DriveLetter) } |
        ForEach-Object { ([string]$_.DriveLetter).ToUpperInvariant() } |
        Sort-Object -Unique)
}

function Get-RelatedProcesses {
    $Service = Get-ServiceEvidence
    $ServicePID = if ($null -eq $Service) { 0 } else { [int]$Service.processId }
    return @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
        Where-Object {
            $_.ProcessId -ne $PID -and $_.ProcessId -ne $ServicePID -and
            ($_.Name -match '^(Unity|UnityPackageManager|UnityCrashHandler64|UnityShaderCompiler|AssetImportWorker|testplay|opencode)(\.exe)?$' -or
             ($_.Name -match '^(node|powershell)(\.exe)?$' -and
              ([string]$_.CommandLine -match 'honeybee-protocol3|honeybee.*unity')))
        } |
        Select-Object ProcessId, Name, ExecutablePath, CommandLine)
}

function Get-HostState {
    return [ordered]@{
        service = Get-ServiceEvidence
        fileBackedDisks = @(Get-FileBackedDisks)
        driveLetters = @(Get-DriveLetters)
        relatedProcesses = @(Get-RelatedProcesses)
        receiptExists = Test-Path -LiteralPath $ReceiptPath -PathType Leaf
    }
}

function Assert-ZeroProtectedStatus {
    param([object]$Status, [string]$Label)
    $Value = $Status
    if ($null -ne $Status.PSObject.Properties['status'] -and $Status.status -isnot [string]) {
        $Value = $Status.status
    }
    foreach ($Name in @('activeChildCount', 'retainedChildCount', 'pendingCount', 'quarantineCount')) {
        if ([int]$Value.$Name -ne 0) {
            throw "$Label has protected state: $Name=$($Value.$Name)"
        }
    }
    if ([bool]$Value.manualRecoveryRequired) {
        throw "$Label requires manual recovery."
    }
    return $Value
}

function Get-ParentEvidence {
    param([string]$Store)
    $Parents = @()
    $Files = @(Get-ChildItem -LiteralPath $Store -Filter parent.vhdx -File -Recurse -Force -ErrorAction Stop)
    foreach ($File in $Files) {
        $MetadataPath = Join-Path $File.DirectoryName 'metadata.json'
        $CompletePath = Join-Path $File.DirectoryName 'COMPLETE'
        if (-not (Test-Path -LiteralPath $MetadataPath -PathType Leaf) -or
            -not (Test-Path -LiteralPath $CompletePath -PathType Leaf)) {
            throw "Committed parent layout is incomplete: $($File.DirectoryName)"
        }
        $Parents += [ordered]@{
            path = $File.FullName
            length = [long]$File.Length
            sha256 = Get-NormalizedSHA256 -LiteralPath $File.FullName
            metadataPath = $MetadataPath
            metadataSHA256 = Get-NormalizedSHA256 -LiteralPath $MetadataPath
            completeSHA256 = Get-NormalizedSHA256 -LiteralPath $CompletePath
        }
    }
    return @($Parents)
}

function Assert-ParentEvidenceEqual {
    param([object[]]$Before, [object[]]$After, [string]$Label)
    if ($Before.Count -ne $After.Count) { throw "$Label parent count changed." }
    for ($Index = 0; $Index -lt $Before.Count; $Index++) {
        $Expected = $Before[$Index]
        $Actual = @($After | Where-Object { $_.path -eq $Expected.path })
        if ($Actual.Count -ne 1 -or $Actual[0].sha256 -ne $Expected.sha256 -or $Actual[0].length -ne $Expected.length) {
            throw "$Label immutable parent changed: $($Expected.path)"
        }
    }
}

function Resolve-HoneyBeeArtifact {
    param([string]$JournalPath, [object]$Artifact)
    $Digest = [string]$Artifact.contentDigest
    if ($Digest -notmatch '^sha256:([0-9a-f]{64})$') {
        throw "Invalid HoneyBee artifact digest: $Digest"
    }
    $Hex = $Matches[1]
    $RunDirectory = Split-Path -Parent $JournalPath
    $Blob = Join-Path $RunDirectory ("blobs\sha256\{0}\{1}" -f $Hex.Substring(0, 2), $Hex.Substring(2))
    if (-not (Test-Path -LiteralPath $Blob -PathType Leaf)) {
        throw "HoneyBee artifact blob is missing: $Blob"
    }
    if ((Get-NormalizedSHA256 -LiteralPath $Blob) -ne $Hex) {
        throw "HoneyBee artifact blob hash mismatch: $Blob"
    }
    return $Blob
}

function Read-HoneyBeeArtifactJson {
    param([string]$JournalPath, [object]$Artifact)
    $Blob = Resolve-HoneyBeeArtifact -JournalPath $JournalPath -Artifact $Artifact
    return Get-Content -Raw -LiteralPath $Blob | ConvertFrom-Json
}

function Assert-CapabilityEvidence {
    param(
        [string]$JournalPath,
        [object]$Event,
        [string]$Kind,
        [string]$ExpectedWorkspaceID,
        [string]$ExpectedSessionID,
        [int]$ExpectedEditorPID
    )
    if ($Event.payload.kind -ne $Kind) { throw "Unexpected capability kind: $($Event.payload.kind)" }
    $Evidence = Read-HoneyBeeArtifactJson -JournalPath $JournalPath -Artifact $Event.payload.evidence
    if ($Evidence.capability.kind -ne $Kind -or
        $Evidence.bridge.workspaceId -ne $ExpectedWorkspaceID -or
        $Evidence.bridge.bridgeSessionId -ne $ExpectedSessionID -or
        [int]$Evidence.bridge.editorPid -ne $ExpectedEditorPID) {
        throw "HoneyBee capability evidence identity mismatch: $Kind"
    }
    $SummaryEntry = @($Evidence.files | Where-Object { $_.name -eq 'summary.json' })
    if ($SummaryEntry.Count -ne 1) { throw "TestPlay summary.json evidence is missing: $Kind" }
    $Summary = Read-HoneyBeeArtifactJson -JournalPath $JournalPath -Artifact $SummaryEntry[0].artifact
    if ($Summary.capability -ne $Kind -or [int]$Summary.exit_code -ne 0 -or
        [int]$Summary.bridge.protocol_version -ne 3 -or
        $Summary.bridge.workspace_id -ne $ExpectedWorkspaceID -or
        $Summary.bridge.bridge_session_id -ne $ExpectedSessionID -or
        [int]$Summary.bridge.editor_pid -ne $ExpectedEditorPID -or
        [bool]$Summary.fallback_used -or $Summary.cleanup_state -ne 'released') {
        throw "TestPlay capability result contract failed: $Kind"
    }
    if ($Kind -eq 'compile') {
        if ([int]$Summary.total -ne 0 -or [int]$Summary.compile_errors -ne 0) {
            throw 'Compile capability executed tests or reported compile errors.'
        }
    }
    else {
        if ([int]$Summary.total -lt 1 -or [int]$Summary.passed -ne [int]$Summary.total -or
            [int]$Summary.failed -ne 0 -or [int]$Summary.compile_errors -ne 0) {
            throw 'Warm-test capability did not execute and pass at least one test.'
        }
    }
    return [ordered]@{ honeyBee = $Evidence; testplay = $Summary }
}

function Prepare-FixtureSource {
    foreach ($Name in @('Assets', 'Packages', 'ProjectSettings')) {
        Copy-Item -LiteralPath (Join-Path $FixtureSource $Name) -Destination (Join-Path $SourceProject $Name) -Recurse
    }
    $RuntimePath = Join-Path $SourceProject 'Assets\Runtime\DeterministicProbe.cs'
    $Runtime = [IO.File]::ReadAllText($RuntimePath)
    $ExpectedLine = 'return left * 10 + right;'
    if (($Runtime.Split([string[]]@($ExpectedLine), [StringSplitOptions]::None).Count - 1) -ne 1) {
        throw 'The deterministic fixture source no longer has the expected exact implementation.'
    }
    $Runtime = $Runtime.Replace($ExpectedLine, 'return left * 10 + right + 1;')
    Write-TextFile -LiteralPath $RuntimePath -Value $Runtime

    $PackageDestination = Join-Path $SourceProject "Packages\$PackageName"
    if (Test-Path -LiteralPath $PackageDestination) {
        throw "Embedded bridge destination already exists: $PackageDestination"
    }
    Copy-Item -LiteralPath $BridgePackagePath -Destination $PackageDestination -Recurse

    $ManifestPath = Join-Path $SourceProject 'Packages\manifest.json'
    $Manifest = Get-Content -Raw -LiteralPath $ManifestPath | ConvertFrom-Json
    $Manifest.dependencies.PSObject.Properties.Remove($PackageName)
    Write-JsonFile -LiteralPath $ManifestPath -Value $Manifest

    $LockPath = Join-Path $SourceProject 'Packages\packages-lock.json'
    $Lock = Get-Content -Raw -LiteralPath $LockPath | ConvertFrom-Json
    $Lock.dependencies.PSObject.Properties.Remove($PackageName)
    $Lock.dependencies | Add-Member -NotePropertyName $PackageName -NotePropertyValue ([pscustomobject][ordered]@{
        version = "file:$PackageName"
        depth = 0
        source = 'embedded'
        dependencies = [pscustomobject]@{}
    })
    Write-JsonFile -LiteralPath $LockPath -Value $Lock
}

function Restore-OldBroker {
    if (-not $script:OldReceiptMoved -or $script:OldRestored) { return }
    if ($null -ne (Get-ServiceEvidence)) {
        throw 'Refusing old broker restore while a broker service still exists.'
    }
    if (Test-Path -LiteralPath $ReceiptPath) {
        throw "Refusing old broker restore because the authoritative receipt path is occupied: $ReceiptPath"
    }
    if (-not (Test-Path -LiteralPath $PreservedReceiptPath -PathType Leaf)) {
        throw "Preserved old receipt is missing: $PreservedReceiptPath"
    }
    $WorkspaceRoot = [string]$script:OldReceipt.workspaceRoot
    $WorkspaceParent = Split-Path -Parent $WorkspaceRoot
    if (-not (Test-Path -LiteralPath $WorkspaceParent -PathType Container)) {
        throw "Refusing old broker restore because the workspace parent is missing: $WorkspaceParent"
    }
    $WorkspaceParentItem = Get-Item -LiteralPath $WorkspaceParent -Force
    if (($WorkspaceParentItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Refusing old broker restore because the workspace parent is a reparse point: $WorkspaceParent"
    }
    if (Test-Path -LiteralPath $WorkspaceRoot) {
        $WorkspaceItem = Get-Item -LiteralPath $WorkspaceRoot -Force
        if (-not $WorkspaceItem.PSIsContainer -or
            ($WorkspaceItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
            @(Get-ChildItem -LiteralPath $WorkspaceRoot -Force).Count -ne 0) {
            throw "Refusing old broker restore because the workspace root is not an empty real directory: $WorkspaceRoot"
        }
    }
    else {
        New-Item -ItemType Directory -Path $WorkspaceRoot | Out-Null
    }
    $ACLResult = Invoke-NativeCapture -LiteralPath 'icacls.exe' `
        -ArgumentList @(
            $WorkspaceRoot,
            '/inheritance:r',
            '/grant:r',
            '*S-1-5-18:(OI)(CI)F',
            '*S-1-5-32-544:(OI)(CI)F',
            "*$([string]$script:OldReceipt.userSid)`:(OI)(CI)M"
        ) `
        -OutputPath (Join-Path $ArtifactRoot 'old-workspace-restore-acl.txt') `
        -WorkingDirectory $ArtifactRoot
    if ($ACLResult.ExitCode -ne 0) {
        throw "Old broker workspace ACL restore failed: exit=$($ACLResult.ExitCode)"
    }
    Move-Item -LiteralPath $PreservedReceiptPath -Destination $ReceiptPath
    $Restore = Invoke-NativeCapture -LiteralPath $script:OldReceipt.executable `
        -ArgumentList @('storage', 'install', '--root', [string]$script:OldReceipt.storeRoot) `
        -OutputPath (Join-Path $ArtifactRoot 'old-storage-restore.txt') `
        -WorkingDirectory $ArtifactRoot
    if ($Restore.ExitCode -ne 0) { throw "Old broker restore failed: exit=$($Restore.ExitCode)" }
    [void](Wait-ServiceRunning)
    $script:OldRestored = $true
}

if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }
if (-not $InstallApproved) { throw 'Pass -InstallApproved after reviewing the unique temporary broker contract.' }
if (-not $FaultInjectionApproved) { throw "Pass -FaultInjectionApproved to authorize the exact $Scenario process fault." }
if ($Scenario -eq 'RebootPrepare') {
    if ([string]::IsNullOrWhiteSpace($RebootPointerPath) -or [string]::IsNullOrWhiteSpace($RebootHarnessPath) -or
        [string]::IsNullOrWhiteSpace($ExpectedRebootHarnessSHA256)) {
        throw 'RebootPrepare requires the durable pointer path and exact reboot harness identity.'
    }
    if (-not [IO.Path]::IsPathRooted($RebootPointerPath) -or -not [IO.Path]::IsPathRooted($RebootHarnessPath) -or
        -not (Test-Path -LiteralPath $RebootHarnessPath -PathType Leaf) -or
        (Get-NormalizedSHA256 -LiteralPath $RebootHarnessPath) -ne $ExpectedRebootHarnessSHA256.ToLowerInvariant()) {
        throw 'Reboot harness path or SHA-256 is invalid.'
    }
    if (Test-Path -LiteralPath $RebootPointerPath) { throw "Reboot pointer already exists: $RebootPointerPath" }
}
if ((Test-Path -LiteralPath $ArtifactRoot) -or
    (Test-Path -LiteralPath $SessionRoot) -or
    (Test-Path -LiteralPath $ZipPath) -or
    (Test-Path -LiteralPath $PreservedReceiptPath)) {
    throw 'A supposedly unique recovery path already exists.'
}

foreach ($Path in @($TestPlayExecutable, $WorkspaceStorageExecutable, $AgentExecutable, $UnityEditorPath)) {
    if (-not [IO.Path]::IsPathRooted($Path) -or -not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Required executable is not an existing absolute file: $Path"
    }
}
foreach ($Path in @($HoneyBeeRepository, $FixtureSource, $BridgePackagePath)) {
    if (-not [IO.Path]::IsPathRooted($Path) -or -not (Test-Path -LiteralPath $Path -PathType Container)) {
        throw "Required directory is not an existing absolute path: $Path"
    }
}
if (-not (Test-Path -LiteralPath $ReceiptPath -PathType Leaf)) {
    throw "Existing broker receipt is required: $ReceiptPath"
}

$TestPlaySHA256 = Assert-FileSHA256 -LiteralPath $TestPlayExecutable -Expected $ExpectedTestPlaySHA256 -Label 'TestPlay'
$WorkspaceStorageSHA256 = Assert-FileSHA256 -LiteralPath $WorkspaceStorageExecutable -Expected $ExpectedWorkspaceStorageSHA256 -Label 'workspace storage adapter'
$TestPlayVersionPath = Join-Path $env:TEMP "testplay-protocol3-version-$Timestamp.txt"
$Version = Invoke-NativeCapture -LiteralPath $TestPlayExecutable -ArgumentList @('version') -OutputPath $TestPlayVersionPath -WorkingDirectory $env:TEMP
if ($Version.ExitCode -ne 0) { throw 'TestPlay version command failed.' }
$VersionJSON = Read-NativeJson -LiteralPath $TestPlayVersionPath
if ($VersionJSON.commit -ne $ExpectedTestPlayCommit -or $VersionJSON.version -ne 'v0.14.0-dev') {
    throw "TestPlay build identity mismatch: version=$($VersionJSON.version) commit=$($VersionJSON.commit)"
}
$HoneyBeeCommit = Get-GitText -Repository $HoneyBeeRepository -Arguments @('rev-parse', 'HEAD')
$HoneyBeeStatus = Get-GitText -Repository $HoneyBeeRepository -Arguments @('status', '--porcelain=v1', '--untracked-files=all')
if ($HoneyBeeCommit -ne $ExpectedHoneyBeeCommit -or $HoneyBeeStatus -ne '') {
    throw "HoneyBee repository is not the exact clean pinned revision: revision=$HoneyBeeCommit status=$HoneyBeeStatus"
}
$HoneyBeeCLI = Join-Path $HoneyBeeRepository 'apps\cli\dist\cli.js'
if (-not (Test-Path -LiteralPath $HoneyBeeCLI -PathType Leaf)) { throw "Built HoneyBee CLI is missing: $HoneyBeeCLI" }
$HoneyBeeCLISHA256 = Get-NormalizedSHA256 -LiteralPath $HoneyBeeCLI
$HoneyBeeRuntime = Get-HoneyBeeRuntimeEvidence -Repository $HoneyBeeRepository
if ($HoneyBeeRuntime.digest -ne $ExpectedHoneyBeeRuntimeSHA256.ToLowerInvariant()) {
    throw "HoneyBee runtime tree SHA-256 mismatch: expected=$ExpectedHoneyBeeRuntimeSHA256 actual=$($HoneyBeeRuntime.digest)"
}
$BridgePackageSHA = (Get-TreeDigest -Root $BridgePackagePath).digest
if ($BridgePackageSHA -ne $ExpectedBridgePackageSHA256.ToLowerInvariant()) {
    throw "Bridge package tree SHA-256 mismatch: expected=$ExpectedBridgePackageSHA256 actual=$BridgePackageSHA"
}
$ProjectVersion = Get-Content -Raw -LiteralPath (Join-Path $FixtureSource 'ProjectSettings\ProjectVersion.txt')
if ($ProjectVersion -notmatch [regex]::Escape("m_EditorVersion: $ExpectedUnityVersion")) {
    throw "Fixture Unity version is not $ExpectedUnityVersion."
}

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
New-Item -ItemType Directory -Path $SourceProject | Out-Null
New-Item -ItemType Directory -Path $HoneyBeeRunRoot | Out-Null
Copy-Item -LiteralPath $TestPlayVersionPath -Destination (Join-Path $ArtifactRoot 'testplay-version.json')
Remove-Item -LiteralPath $TestPlayVersionPath
Prepare-FixtureSource

$script:OldReceipt = Get-Content -Raw -LiteralPath $ReceiptPath | ConvertFrom-Json
$OldReceiptBytes = [IO.File]::ReadAllBytes($ReceiptPath)
[IO.File]::WriteAllBytes((Join-Path $ArtifactRoot 'old-storage-install-receipt.json'), $OldReceiptBytes)
$OldReceiptSHA256 = Get-NormalizedSHA256 -LiteralPath $ReceiptPath
if ($script:OldReceipt.serviceName -ne $ServiceName -or
    -not [IO.Path]::IsPathRooted([string]$script:OldReceipt.storeRoot) -or
    -not [IO.Path]::IsPathRooted([string]$script:OldReceipt.workspaceRoot) -or
    -not [IO.Path]::IsPathRooted([string]$script:OldReceipt.executable)) {
    throw 'Existing broker receipt identity is invalid.'
}
if (-not (Test-Path -LiteralPath $script:OldReceipt.storeRoot -PathType Container) -or
    -not (Test-Path -LiteralPath $script:OldReceipt.executable -PathType Leaf)) {
    throw 'Existing broker store or executable is missing.'
}
$OldExecutableSHA256 = Get-NormalizedSHA256 -LiteralPath $script:OldReceipt.executable
$OldParentBefore = @(Get-ParentEvidence -Store $script:OldReceipt.storeRoot)
$PreState = Get-HostState
if ($null -eq $PreState.service -or $PreState.service.state -ne 'Running') { throw 'Existing broker service is not running.' }
if ($PreState.fileBackedDisks.Count -ne 0 -or $PreState.relatedProcesses.Count -ne 0) {
    throw 'Pre-state has a file-backed disk or related non-service process.'
}
if (@(Get-ChildItem -LiteralPath $script:OldReceipt.workspaceRoot -Force).Count -ne 0) {
    throw 'Existing broker workspace root is not empty.'
}

$OldStatusCapture = Invoke-NativeCapture -LiteralPath $script:OldReceipt.executable -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'old-storage-status-before.json') -WorkingDirectory $ArtifactRoot
if ($OldStatusCapture.ExitCode -ne 0) { throw 'Existing broker status failed.' }
$OldStatusBefore = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'old-storage-status-before.json')
$OldStatusBeforeValue = Assert-ZeroProtectedStatus -Status $OldStatusBefore -Label 'Existing broker'
Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'pre-state.json') -Value $PreState
Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'old-parent-before.json') -Value $OldParentBefore
Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'pins.json') -Value ([ordered]@{
    testplay = [ordered]@{ path = $TestPlayExecutable; commit = $ExpectedTestPlayCommit; sha256 = $TestPlaySHA256 }
    bridgePackage = [ordered]@{ path = $BridgePackagePath; treeSHA256 = $BridgePackageSHA }
    honeyBee = [ordered]@{ path = $HoneyBeeRepository; commit = $HoneyBeeCommit; cliSHA256 = $HoneyBeeCLISHA256; runtime = $HoneyBeeRuntime }
    workspaceStorage = [ordered]@{ path = $WorkspaceStorageExecutable; contractCommit = $WorkspaceStorageContractCommit; sha256 = $WorkspaceStorageSHA256 }
})

$Failure = $null
$OldReceiptMoved = $false
$OldRestored = $false
$NewBrokerInstalled = $false
$NewBrokerRemoved = $false
$CleanupState = 'preserved'
$E2EResult = $null
$CapabilityEvidence = @()
$FaultEvidence = $null
$TerminalEvidence = $null
$BrokerRestartEvidence = $null
$HoneyBeeProcess = $null
$HoneyBeeExitCode = $null
$LeaseEvidence = $null
$FaultInjected = $false
$LeaveActiveForReboot = $false
$NewParentBefore = $null
$NewParentAfter = $null
$SourceBefore = $null
$SourceAfter = $null
$NewStatus = $null
$NewReceipt = $null
$PreviousFixtureMarker = [Environment]::GetEnvironmentVariable('TESTPLAY_UNITY_FIXTURE_MARKER', 'Process')
$PreviousReadyFile = [Environment]::GetEnvironmentVariable('TESTPLAY_UNITY_FIXTURE_REBOOT_READY_FILE', 'Process')
$PreviousReleaseFile = [Environment]::GetEnvironmentVariable('TESTPLAY_UNITY_FIXTURE_REBOOT_RELEASE_FILE', 'Process')
$Started = Get-Date

Start-Transcript -Path (Join-Path $ArtifactRoot 'terminal-transcript.txt') -Force | Out-Null
try {
    $Preserve = Invoke-NativeCapture -LiteralPath $script:OldReceipt.executable `
        -ArgumentList @('storage', 'uninstall', '--preserve-data') `
        -OutputPath (Join-Path $ArtifactRoot 'old-storage-preserve.txt') `
        -WorkingDirectory $ArtifactRoot
    if ($Preserve.ExitCode -ne 0) { throw "Existing broker preserve uninstall failed: exit=$($Preserve.ExitCode)" }
    Wait-ServiceAbsent
    if (-not (Test-Path -LiteralPath $script:OldReceipt.storeRoot -PathType Container) -or
        (Get-NormalizedSHA256 -LiteralPath $ReceiptPath) -ne $OldReceiptSHA256) {
        throw 'Existing broker data or receipt changed during preserve uninstall.'
    }
    Move-Item -LiteralPath $ReceiptPath -Destination $PreservedReceiptPath
    $OldReceiptMoved = $true

    $Install = Invoke-NativeCapture -LiteralPath $TestPlayExecutable `
        -ArgumentList @('storage', 'install', '--root', $StoreRoot) `
        -OutputPath (Join-Path $ArtifactRoot 'new-storage-install.txt') `
        -WorkingDirectory $ArtifactRoot
    if ($Install.ExitCode -ne 0) { throw "Protocol 3 broker install failed: exit=$($Install.ExitCode)" }
    $NewBrokerInstalled = $true
    [void](Wait-ServiceRunning)
    $NewReceipt = Get-Content -Raw -LiteralPath $ReceiptPath | ConvertFrom-Json
    if ($NewReceipt.storeRoot -ne $StoreRoot -or $NewReceipt.userSid -ne $script:OldReceipt.userSid) {
        throw 'Protocol 3 broker receipt identity mismatch.'
    }

    $ProvisionConfigPath = Join-Path $ArtifactRoot 'testplay-provision.json'
    Write-JsonFile -LiteralPath $ProvisionConfigPath -Value ([ordered]@{
        schema_version = '1'
        unity_path = $UnityEditorPath
        project_path = $SourceProject
        test_platform = 'play_mode'
        timeout = [ordered]@{ total_ms = 900000 }
        result_dir = (Join-Path $ArtifactRoot 'provision-results')
        retention = [ordered]@{ max_runs = 0 }
        workspace = [ordered]@{
            backend = 'vhdx-diff'
            store_root = $StoreRoot
            store_max_allocated_bytes = 34359738368
            minimum_host_free_bytes = 21474836480
        }
    })
    $Provision = Invoke-NativeCapture -LiteralPath $TestPlayExecutable `
        -ArgumentList @('--config', $ProvisionConfigPath, 'run', '--filter', $ProvisionTest, '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge') `
        -OutputPath (Join-Path $ArtifactRoot 'testplay-provision-output.json') `
        -WorkingDirectory $ArtifactRoot
    if ($Provision.ExitCode -ne 0) { throw "Parent provisioning run failed: exit=$($Provision.ExitCode)" }
    $ProvisionResult = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'testplay-provision-output.json')
    if ([int]$ProvisionResult.exit_code -ne 0 -or [int]$ProvisionResult.total -ne 1 -or
        [int]$ProvisionResult.passed -ne 1 -or [bool]$ProvisionResult.workspace_metrics.fallbackUsed -or
        $ProvisionResult.workspace_metrics.cleanupState -ne 'released' -or
        -not [bool]$ProvisionResult.workspace_metrics.parentCreated) {
        throw 'Parent provisioning evidence did not satisfy the strict vhdx-diff contract.'
    }
    $ParentPath = [string]$ProvisionResult.workspace_metrics.parentPath
    $MetadataPath = Join-Path (Split-Path -Parent $ParentPath) 'metadata.json'
    $Metadata = Get-Content -Raw -LiteralPath $MetadataPath | ConvertFrom-Json
    if ($Metadata.compatibilityKey.digest -ne $ProvisionResult.workspace_metrics.parentKey -or
        $Metadata.provider -ne 'vhdx-differencing' -or -not [bool]$Metadata.immutable) {
        throw 'Committed parent metadata does not match provisioning evidence.'
    }
    $NewParentBefore = [ordered]@{
        path = $ParentPath
        sha256 = Get-NormalizedSHA256 -LiteralPath $ParentPath
        length = (Get-Item -LiteralPath $ParentPath -Force).Length
        metadata = $Metadata
    }
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'new-parent-before.json') -Value $NewParentBefore
    $SourceBefore = Get-SourceEvidence -Root $SourceProject
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'source-before.json') -Value $SourceBefore

    $HoneyBeeConfigPath = Join-Path $ArtifactRoot 'honeybee-unity-work-v2.json'
    Write-JsonFile -LiteralPath $HoneyBeeConfigPath -Value ([ordered]@{
        schemaVersion = 2
        sourceProjectPath = $SourceProject
        workspaceStorage = [ordered]@{
            command = [ordered]@{ command = $WorkspaceStorageExecutable }
            contractCommit = $WorkspaceStorageContractCommit
            binarySha256 = $WorkspaceStorageSHA256
            workspaceRoot = [string]$NewReceipt.workspaceRoot
            parentKey = $Metadata.compatibilityKey
        }
        agent = [ordered]@{
            command = [ordered]@{ command = $AgentExecutable; args = @('run', '--pure') }
            harness = 'stdio-framed-v2'
            timeoutMs = 900000
            maxOutputBytes = 4194304
        }
        testplay = [ordered]@{
            command = [ordered]@{ command = $TestPlayExecutable }
            unityPath = $UnityEditorPath
            platform = 'edit_mode'
            timeoutMs = 900000
            bridgeProtocolVersion = 3
        }
        editorPool = [ordered]@{
            id = "unity-editors-protocol3-$Timestamp"
            capacity = 1
            registrationTimeoutMs = 30000
            activationTimeoutMs = 180000
            bridgeReadyTimeoutMs = 180000
            capabilityTimeoutMs = 900000
            shutdownTimeoutMs = 120000
        }
        priority = 'validation'
        capabilities = @(
            [ordered]@{ id = 'compile'; kind = 'compile' },
            [ordered]@{ id = 'warm-test'; kind = 'warm-test'; filter = $WarmTest }
        )
    })

    $Task = 'In Assets/Runtime/DeterministicProbe.cs, fix DeterministicState.Combine so Combine(4, 2) returns 42. Make the smallest source change. Do not launch Unity and do not edit Packages, ProjectSettings, or Library.'
    $Node = (Get-Command node.exe -ErrorAction Stop).Source
    $HoneyBeeStdout = Join-Path $ArtifactRoot 'honeybee-recovery-stdout.json'
    $HoneyBeeStderr = Join-Path $ArtifactRoot 'honeybee-recovery-stderr.txt'
    $env:TESTPLAY_UNITY_FIXTURE_MARKER = "honeybee-protocol3-recovery-$ScenarioSlug-$Timestamp"
    $env:TESTPLAY_UNITY_FIXTURE_REBOOT_READY_FILE = $GateReadyPath
    $env:TESTPLAY_UNITY_FIXTURE_REBOOT_RELEASE_FILE = $GateReleasePath
    $HoneyBeeProcess = Start-Process -FilePath $Node `
        -ArgumentList @($HoneyBeeCLI, 'unity', 'run', '--config', $HoneyBeeConfigPath, '--task', $Task, '--json') `
        -WorkingDirectory $HoneyBeeRunRoot `
        -NoNewWindow -PassThru `
        -RedirectStandardOutput $HoneyBeeStdout `
        -RedirectStandardError $HoneyBeeStderr

    $FaultPoint = Wait-HoneyBeeFaultPoint -RunRoot $HoneyBeeRunRoot -ReadyPath $GateReadyPath
    if ($HoneyBeeProcess.HasExited) { throw 'HoneyBee exited before the exact fault point was verified.' }
    $WorkspaceID = [string]$FaultPoint.workspace.payload.workspaceId
    $EditorPID = [int]$FaultPoint.ownership.payload.pid
    $EditorProcessIdentity = [string]$FaultPoint.ownership.payload.processIdentity
    $BridgeSessionID = [string]$FaultPoint.binding.payload.bridgeSessionId
    $CapabilityPID = [int]$FaultPoint.warmProcess.payload.pid
    $CapabilityProcessIdentity = [string]$FaultPoint.warmProcess.payload.processIdentity
    if ($EditorPID -le 0 -or $CapabilityPID -le 0 -or [string]::IsNullOrWhiteSpace($BridgeSessionID) -or
        [string]::IsNullOrWhiteSpace($EditorProcessIdentity) -or [string]::IsNullOrWhiteSpace($CapabilityProcessIdentity)) {
        throw 'Fault-point process or bridge identity is incomplete.'
    }
    $EditorIdentity = Get-ProcessIdentity -ProcessID $EditorPID
    $CapabilityIdentity = Get-ProcessIdentity -ProcessID $CapabilityPID
    if ($null -eq $EditorIdentity -or -not (Test-SamePath $EditorIdentity.executablePath $UnityEditorPath) -or
        $EditorIdentity.processIdentity -ne $EditorProcessIdentity) {
        throw 'Unity process identity does not match the signed HoneyBee journal event.'
    }
    if ($null -eq $CapabilityIdentity -or -not (Test-SamePath $CapabilityIdentity.executablePath $TestPlayExecutable) -or
        $CapabilityIdentity.processIdentity -ne $CapabilityProcessIdentity) {
        throw 'Capability TestPlay process identity does not match the signed HoneyBee journal event.'
    }
    $LeaseRoot = Join-Path (Join-Path $StoreRoot ([string]$NewReceipt.userSid)) 'leases'
    $LeaseEvidence = Get-ExactLeaseEvidence -LeaseRoot $LeaseRoot -FaultPoint $FaultPoint -WorkspaceRoot ([string]$NewReceipt.workspaceRoot)
    if ([int]$LeaseEvidence.journal.unityPid -ne $EditorPID) {
        throw 'Lease Unity process identity does not match the warm-test fault target.'
    }
    Copy-Item -LiteralPath $FaultPoint.journalPath -Destination (Join-Path $ArtifactRoot 'fault-honeybee-events.jsonl')
    Copy-Item -LiteralPath $LeaseEvidence.journalPath -Destination (Join-Path $ArtifactRoot 'fault-lease-journal.json')
    Copy-Item -LiteralPath $LeaseEvidence.markerPath -Destination (Join-Path $ArtifactRoot 'fault-workspace-owner.json')
    $FaultEvidence = [ordered]@{
        scenario = $Scenario
        journalPath = $FaultPoint.journalPath
        journalSHA256 = Get-NormalizedSHA256 -LiteralPath $FaultPoint.journalPath
        workspaceId = $WorkspaceID
        leaseId = [string]$LeaseEvidence.journal.leaseId
        ownershipToken = [string]$LeaseEvidence.journal.ownershipToken
        bridgeSessionId = $BridgeSessionID
        parentPath = [string]$LeaseEvidence.journal.parentPath
        parentSHA256 = Get-NormalizedSHA256 -LiteralPath ([string]$LeaseEvidence.journal.parentPath)
        childPath = [string]$LeaseEvidence.journal.childPath
        childSHA256 = Get-NormalizedSHA256 -LiteralPath ([string]$LeaseEvidence.journal.childPath)
        mountPath = [string]$LeaseEvidence.journal.mountPath
        workspacePath = [string]$LeaseEvidence.journal.workspacePath
        leaseJournalSHA256 = $LeaseEvidence.journalSHA256
        workspaceOwnerSHA256 = $LeaseEvidence.markerSHA256
        honeyBee = Get-ProcessIdentity -ProcessID $HoneyBeeProcess.Id
        capability = $CapabilityIdentity
        unity = $EditorIdentity
        broker = $null
        fileBackedDisks = @(Get-FileBackedDisks)
    }

    if ($Scenario -eq 'RebootPrepare') {
        $StatePath = Join-Path $ArtifactRoot 'reboot-state.json'
        $Contract = [ordered]@{
            schemaVersion = 1
            phase = 'prepared'
            preparedAt = (Get-Date).ToUniversalTime().ToString('o')
            artifactRoot = $ArtifactRoot
            statePath = $StatePath
            pointerPath = $RebootPointerPath
            rebootHarnessPath = $RebootHarnessPath
            rebootHarnessSHA256 = $ExpectedRebootHarnessSHA256.ToLowerInvariant()
            recoveryHarnessPath = $PSCommandPath
            recoveryHarnessSHA256 = Get-NormalizedSHA256 -LiteralPath $PSCommandPath
            bootBefore = Get-BootEvidence
            preState = $PreState
            testplayExecutable = $TestPlayExecutable
            testplaySHA256 = $TestPlaySHA256
            testplayCommit = $ExpectedTestPlayCommit
            unityEditorPath = $UnityEditorPath
            honeyBeeRepository = $HoneyBeeRepository
            honeyBeeCommit = $HoneyBeeCommit
            honeyBeeRuntime = $HoneyBeeRuntime
            honeyBeeProcess = Get-ProcessIdentity -ProcessID $HoneyBeeProcess.Id
            workspaceStorageExecutable = $WorkspaceStorageExecutable
            workspaceStorageSHA256 = $WorkspaceStorageSHA256
            workspaceStorageContractCommit = $WorkspaceStorageContractCommit
            agentExecutable = $AgentExecutable
            bridgePackagePath = $BridgePackagePath
            bridgePackageSHA256 = $BridgePackageSHA
            sourceProject = $SourceProject
            sourceBefore = $SourceBefore
            parentBefore = $NewParentBefore
            newReceipt = $NewReceipt
            storeRoot = $StoreRoot
            sessionRoot = $SessionRoot
            oldReceipt = $script:OldReceipt
            oldReceiptSHA256 = $OldReceiptSHA256
            oldExecutableSHA256 = $OldExecutableSHA256
            oldParentBefore = $OldParentBefore
            preservedReceiptPath = $PreservedReceiptPath
            authoritativeReceiptPath = $ReceiptPath
            fault = $FaultEvidence
            lease = $LeaseEvidence.journal
            leaseJournalPath = $LeaseEvidence.journalPath
            leaseJournalSHA256 = $LeaseEvidence.journalSHA256
            workspaceOwnerPath = $LeaseEvidence.markerPath
            workspaceOwnerSHA256 = $LeaseEvidence.markerSHA256
            honeyBeeJournalPath = $FaultPoint.journalPath
            honeyBeeJournalSHA256 = Get-NormalizedSHA256 -LiteralPath $FaultPoint.journalPath
            readySignalPath = $GateReadyPath
            readySignalSHA256 = Get-NormalizedSHA256 -LiteralPath $GateReadyPath
            releaseSignalPath = $GateReleasePath
        }
        Write-DurableJsonExclusive -LiteralPath $StatePath -Value $Contract
        $StateSHA256 = Get-NormalizedSHA256 -LiteralPath $StatePath
        $Pointer = [ordered]@{
            schemaVersion = 1
            statePath = $StatePath
            stateSHA256 = $StateSHA256
            harnessPath = $RebootHarnessPath
            harnessSHA256 = $ExpectedRebootHarnessSHA256.ToLowerInvariant()
            createdAt = (Get-Date).ToUniversalTime().ToString('o')
        }
        Write-DurableJsonExclusive -LiteralPath $RebootPointerPath -Value $Pointer
        Copy-Item -LiteralPath $RebootPointerPath -Destination (Join-Path $ArtifactRoot 'reboot-pointer.json')
        $LeaveActiveForReboot = $true
        $CleanupState = 'preserved-for-reboot'
        Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'prepare-summary.json') -Value ([ordered]@{
            schemaVersion = 1
            status = 'REBOOT_REQUIRED'
            verdict = 'PENDING_REBOOT'
            statePath = $StatePath
            stateSHA256 = $StateSHA256
            pointerPath = $RebootPointerPath
            pointerSHA256 = Get-NormalizedSHA256 -LiteralPath $RebootPointerPath
            cleanupState = $CleanupState
        })
        Write-Output 'HONEYBEE_PROTOCOL3_REBOOT_PREPARE_STATUS=REBOOT_REQUIRED'
        Write-Output "HONEYBEE_PROTOCOL3_REBOOT_STATE=$StatePath"
        Write-Output "HONEYBEE_PROTOCOL3_REBOOT_STATE_SHA256=$StateSHA256"
        Write-Output "HONEYBEE_PROTOCOL3_REBOOT_POINTER=$RebootPointerPath"
        Write-Output "HONEYBEE_PROTOCOL3_REBOOT_POINTER_SHA256=$(Get-NormalizedSHA256 -LiteralPath $RebootPointerPath)"
        return
    }
    elseif ($Scenario -eq 'ClientTermination') {
        $FaultEvidence.terminated = Stop-ExactProcess -ProcessID $CapabilityPID -ExpectedPath $TestPlayExecutable -ExpectedIdentity $CapabilityProcessIdentity
        $FaultInjected = $true
    }
    elseif ($Scenario -eq 'UnityTermination') {
        $FaultEvidence.terminated = Stop-ExactProcess -ProcessID $EditorPID -ExpectedPath $UnityEditorPath -ExpectedIdentity $EditorProcessIdentity
        $FaultInjected = $true
    }
    else {
        $OriginalService = Get-ServiceEvidence
        if ($null -eq $OriginalService -or $OriginalService.state -ne 'Running' -or [int]$OriginalService.processId -le 0) {
            throw 'Broker service is not running at the fault point.'
        }
        $BrokerIdentity = Get-ProcessIdentity -ProcessID ([int]$OriginalService.processId)
        if ($null -eq $BrokerIdentity -or -not (Test-SamePath $BrokerIdentity.executablePath ([string]$NewReceipt.executable))) {
            throw 'Broker process does not match the installed receipt.'
        }
        $FaultEvidence.broker = $BrokerIdentity
        $null = Stop-ExactProcess -ProcessID ([int]$OriginalService.processId) -ExpectedPath ([string]$NewReceipt.executable) -ExpectedIdentity ([string]$BrokerIdentity.processIdentity)
        $StoppedService = Wait-ServiceStopped
        Start-Service -Name $ServiceName -ErrorAction Stop
        $RestartedService = Wait-ServiceRunning
        if ([int]$RestartedService.processId -eq [int]$OriginalService.processId) { throw 'Broker restart reused the terminated PID.' }
        $RestartedBroker = Get-ProcessIdentity -ProcessID ([int]$RestartedService.processId)
        if ($null -eq $RestartedBroker -or -not (Test-SamePath $RestartedBroker.executablePath ([string]$NewReceipt.executable))) {
            throw 'Restarted broker executable identity mismatch.'
        }
        if (-not (Test-Path -LiteralPath $LeaseEvidence.journalPath -PathType Leaf) -or
            -not (Test-Path -LiteralPath $LeaseEvidence.journal.childPath -PathType Leaf) -or
            -not (Test-Path -LiteralPath $LeaseEvidence.journal.workspacePath -PathType Container)) {
            throw 'Restarted broker reclaimed the same-boot live workspace.'
        }
        $RestartStatusCapture = Invoke-NativeCapture -LiteralPath $TestPlayExecutable -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'broker-restart-live-status.json') -WorkingDirectory $ArtifactRoot
        if ($RestartStatusCapture.ExitCode -ne 0) { throw 'Broker status failed after same-boot restart.' }
        $RestartStatus = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'broker-restart-live-status.json')
        if ([int]$RestartStatus.activeChildCount -ne 1 -or [int]$RestartStatus.retainedChildCount -ne 0 -or
            [int]$RestartStatus.pendingCount -ne 0 -or [int]$RestartStatus.quarantineCount -ne 0 -or
            [bool]$RestartStatus.manualRecoveryRequired) {
            throw 'Restarted broker did not preserve exactly one live same-boot child.'
        }
        $BrokerRestartEvidence = [ordered]@{
            originalService = $OriginalService
            originalBroker = $BrokerIdentity
            stoppedService = $StoppedService
            restartedService = $RestartedService
            restartedBroker = $RestartedBroker
            liveStatus = $RestartStatus
        }
        Write-TextFile -LiteralPath $GateReleasePath -Value "release-$Timestamp`r`n"
        $FaultInjected = $true
    }
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'fault-evidence.json') -Value $FaultEvidence
    if ($null -ne $BrokerRestartEvidence) {
        Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'broker-restart-evidence.json') -Value $BrokerRestartEvidence
    }

    if (-not (Wait-ProcessExit -Process $HoneyBeeProcess -TimeoutSeconds 300)) {
        throw 'HoneyBee did not reach a terminal outcome after fault injection.'
    }
    $HoneyBeeExitCode = [int]$HoneyBeeProcess.ExitCode
    $Events = @(Read-HoneyBeeEvents -JournalPath $FaultPoint.journalPath)
    $ReleasedEvents = @($Events | Where-Object type -eq 'workspace.released')
    $CompletedEvents = @($Events | Where-Object type -eq 'workflow.completed')
    $FailedEvents = @($Events | Where-Object type -eq 'workflow.failed')
    $WarmStartedEvents = @($Events | Where-Object { $_.type -eq 'capability.started' -and $_.payload.kind -eq 'warm-test' })
    $WarmProcessEvents = @($Events | Where-Object { $_.type -eq 'capability.process-started' -and $_.payload.kind -eq 'warm-test' })
    $WarmCompletedEvents = @($Events | Where-Object { $_.type -eq 'capability.completed' -and $_.payload.kind -eq 'warm-test' })
    $WarmFailedEvents = @($Events | Where-Object { $_.type -eq 'capability.failed' -and $_.payload.kind -eq 'warm-test' })
    if ($ReleasedEvents.Count -ne 1 -or $WarmStartedEvents.Count -ne 1 -or $WarmProcessEvents.Count -ne 1) {
        throw 'HoneyBee did not record one exact warm-test attempt and workspace release.'
    }
    if ($Scenario -eq 'BrokerRestart') {
        if ($HoneyBeeExitCode -ne 0 -or $CompletedEvents.Count -ne 1 -or $FailedEvents.Count -ne 0 -or
            $WarmCompletedEvents.Count -ne 1 -or $WarmFailedEvents.Count -ne 0) {
            throw 'BrokerRestart did not complete the original warm-test exactly once.'
        }
        $E2EResult = Read-NativeJson -LiteralPath $HoneyBeeStdout
        if ($E2EResult.status -ne 'completed' -or $E2EResult.evidence.kind -ne 'unity-capability-evidence' -or
            $E2EResult.release.kind -ne 'workspace-release-receipt') {
            throw 'BrokerRestart HoneyBee result is incomplete.'
        }
        $CapabilityEvents = @($Events | Where-Object type -eq 'capability.completed')
        if ($CapabilityEvents.Count -ne 2) { throw 'BrokerRestart did not complete compile and warm-test exactly once.' }
        $CapabilityEvidence = @(
            Assert-CapabilityEvidence -JournalPath $FaultPoint.journalPath -Event $CapabilityEvents[0] -Kind 'compile' -ExpectedWorkspaceID $WorkspaceID -ExpectedSessionID $BridgeSessionID -ExpectedEditorPID $EditorPID
            Assert-CapabilityEvidence -JournalPath $FaultPoint.journalPath -Event $CapabilityEvents[1] -Kind 'warm-test' -ExpectedWorkspaceID $WorkspaceID -ExpectedSessionID $BridgeSessionID -ExpectedEditorPID $EditorPID
        )
    }
    else {
        if ($HoneyBeeExitCode -eq 0 -or $CompletedEvents.Count -ne 0 -or $FailedEvents.Count -ne 1 -or
            $WarmCompletedEvents.Count -ne 0 -or $WarmFailedEvents.Count -ne 1) {
            throw "$Scenario did not produce one terminal failed capability without replay."
        }
    }
    $TerminalEvidence = Get-Protocol3TerminalEvidence -Events $Events
    $TerminalEvidence['exitCode'] = $HoneyBeeExitCode
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'terminal-evidence.json') -Value $TerminalEvidence
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'capability-evidence.json') -Value $CapabilityEvidence

    $SourceAfter = Get-SourceEvidence -Root $SourceProject
    Assert-SourceEvidenceEqual -Before $SourceBefore -After $SourceAfter
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'source-after.json') -Value $SourceAfter
    $NewParentAfter = [ordered]@{
        path = $ParentPath
        sha256 = Get-NormalizedSHA256 -LiteralPath $ParentPath
        length = (Get-Item -LiteralPath $ParentPath -Force).Length
    }
    if ($NewParentAfter.sha256 -ne $NewParentBefore.sha256 -or $NewParentAfter.length -ne $NewParentBefore.length) {
        throw 'Immutable protocol 3 parent VHDX changed during HoneyBee E2E.'
    }
    Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'new-parent-after.json') -Value $NewParentAfter

    $ReleaseDeadline = (Get-Date).AddMinutes(3)
    do {
        $LeaseGone = $null -eq $LeaseEvidence -or -not (Test-Path -LiteralPath $LeaseEvidence.journalPath)
        $ChildGone = $null -eq $LeaseEvidence -or -not (Test-Path -LiteralPath $LeaseEvidence.journal.childPath)
        $WorkspaceGone = $null -eq $LeaseEvidence -or -not (Test-Path -LiteralPath $LeaseEvidence.journal.workspacePath)
        $NoDisks = @(Get-FileBackedDisks).Count -eq 0
        $NoProcesses = @(Get-RelatedProcesses).Count -eq 0
        if ($LeaseGone -and $ChildGone -and $WorkspaceGone -and $NoDisks -and $NoProcesses) { break }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $ReleaseDeadline)
    if (-not ($LeaseGone -and $ChildGone -and $WorkspaceGone -and $NoDisks -and $NoProcesses)) {
        throw 'Protocol 3 recovery did not release the exact lease, child, workspace, disk, and processes within the bound.'
    }

    $NewStatusCapture = Invoke-NativeCapture -LiteralPath $TestPlayExecutable -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'new-storage-status.json') -WorkingDirectory $ArtifactRoot
    if ($NewStatusCapture.ExitCode -ne 0) { throw 'Protocol 3 broker status failed after E2E.' }
    $NewStatus = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'new-storage-status.json')
    $null = Assert-ZeroProtectedStatus -Status $NewStatus -Label 'Protocol 3 E2E broker'
    if (@(Get-FileBackedDisks).Count -ne 0 -or @(Get-RelatedProcesses).Count -ne 0 -or
        @(Get-ChildItem -LiteralPath $NewReceipt.workspaceRoot -Force).Count -ne 0) {
        throw 'Protocol 3 E2E left an attached disk, process, or workspace residual.'
    }

    $Uninstall = Invoke-NativeCapture -LiteralPath $TestPlayExecutable -ArgumentList @('storage', 'uninstall') -OutputPath (Join-Path $ArtifactRoot 'new-storage-uninstall.txt') -WorkingDirectory $ArtifactRoot
    if ($Uninstall.ExitCode -ne 0) { throw "Protocol 3 broker uninstall failed: exit=$($Uninstall.ExitCode)" }
    $NewBrokerInstalled = $false
    $NewBrokerRemoved = $true
    Wait-ServiceAbsent
    if ((Test-Path -LiteralPath $StoreRoot) -or (Test-Path -LiteralPath $ReceiptPath)) {
        throw 'Protocol 3 broker uninstall left store or receipt residual.'
    }
    if (Test-Path -LiteralPath $SessionRoot) {
        $Entries = @(Get-ChildItem -LiteralPath $SessionRoot -Force)
        if ($Entries.Count -ne 0) { throw "Protocol 3 session root is not empty: $SessionRoot" }
        Remove-Item -LiteralPath $SessionRoot
    }
    Restore-OldBroker
    $CleanupState = 'released'
}
catch {
    $Failure = $_.Exception.ToString()
}
finally {
    if (-not $LeaveActiveForReboot) {
    if ($null -ne $HoneyBeeProcess -and -not $HoneyBeeProcess.HasExited) {
        try {
            $CurrentHoneyBee = Get-ProcessIdentity -ProcessID $HoneyBeeProcess.Id
            if ($null -ne $CurrentHoneyBee -and (Test-SamePath $CurrentHoneyBee.executablePath $Node)) {
                Stop-Process -Id $HoneyBeeProcess.Id -Force -ErrorAction Stop
                $null = Wait-ProcessExit -Process $HoneyBeeProcess -TimeoutSeconds 30
            }
        }
        catch {
            if ($null -eq $Failure) { $Failure = $_.Exception.ToString() }
            else { $Failure += [Environment]::NewLine + 'HoneyBee cleanup: ' + $_.Exception.ToString() }
        }
    }
    if ($null -ne $FaultEvidence) {
        foreach ($Owned in @(
            [ordered]@{ identity = $FaultEvidence.capability; path = $TestPlayExecutable },
            [ordered]@{ identity = $FaultEvidence.unity; path = $UnityEditorPath }
        )) {
            try {
                if ($null -ne $Owned.identity) {
                    $Current = Get-ProcessIdentity -ProcessID ([int]$Owned.identity.processId)
                    if ($null -ne $Current -and $Current.processIdentity -eq $Owned.identity.processIdentity -and
                        (Test-SamePath $Current.executablePath $Owned.path)) {
                        $null = Stop-ExactProcess -ProcessID ([int]$Current.processId) -ExpectedPath $Owned.path -ExpectedIdentity $Current.processIdentity
                    }
                }
            }
            catch {
                if ($null -eq $Failure) { $Failure = $_.Exception.ToString() }
                else { $Failure += [Environment]::NewLine + 'Owned process cleanup: ' + $_.Exception.ToString() }
            }
        }
    }
    if ($NewBrokerInstalled) {
        try {
            $FailureStatus = $null
            $RecoveryDeadline = (Get-Date).AddMinutes(3)
            do {
                $StatusCapture = Invoke-NativeCapture -LiteralPath $TestPlayExecutable -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'new-storage-status-after-failure.json') -WorkingDirectory $ArtifactRoot
                if ($StatusCapture.ExitCode -eq 0) {
                    $CandidateStatus = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'new-storage-status-after-failure.json')
                    try {
                        $null = Assert-ZeroProtectedStatus -Status $CandidateStatus -Label 'Failed protocol 3 recovery broker'
                        if (@(Get-FileBackedDisks).Count -eq 0 -and @(Get-RelatedProcesses).Count -eq 0 -and
                            $null -ne $NewReceipt -and @(Get-ChildItem -LiteralPath $NewReceipt.workspaceRoot -Force -ErrorAction SilentlyContinue).Count -eq 0) {
                            $FailureStatus = $CandidateStatus
                            break
                        }
                    }
                    catch { }
                }
                Start-Sleep -Milliseconds 500
            } while ((Get-Date) -lt $RecoveryDeadline)
            if ($null -ne $FailureStatus) {
                    $UninstallAfterFailure = Invoke-NativeCapture -LiteralPath $TestPlayExecutable -ArgumentList @('storage', 'uninstall') -OutputPath (Join-Path $ArtifactRoot 'new-storage-uninstall-after-failure.txt') -WorkingDirectory $ArtifactRoot
                    if ($UninstallAfterFailure.ExitCode -eq 0) {
                        $NewBrokerInstalled = $false
                        $NewBrokerRemoved = $true
                        Wait-ServiceAbsent
                    }
            }
        }
        catch {
            if ($null -eq $Failure) { $Failure = $_.Exception.ToString() }
            else { $Failure = $Failure + [Environment]::NewLine + 'Cleanup: ' + $_.Exception.ToString() }
        }
    }
    if (-not $NewBrokerInstalled -and $OldReceiptMoved -and -not $OldRestored) {
        try { Restore-OldBroker }
        catch {
            if ($null -eq $Failure) { $Failure = $_.Exception.ToString() }
            else { $Failure = $Failure + [Environment]::NewLine + 'Restore: ' + $_.Exception.ToString() }
        }
    }
    }
    [Environment]::SetEnvironmentVariable('TESTPLAY_UNITY_FIXTURE_MARKER', $PreviousFixtureMarker, 'Process')
    [Environment]::SetEnvironmentVariable('TESTPLAY_UNITY_FIXTURE_REBOOT_READY_FILE', $PreviousReadyFile, 'Process')
    [Environment]::SetEnvironmentVariable('TESTPLAY_UNITY_FIXTURE_REBOOT_RELEASE_FILE', $PreviousReleaseFile, 'Process')
    Stop-Transcript | Out-Null
}

$PostState = Get-HostState
$OldParentAfter = @()
$OldStatusAfter = $null
try {
    if ($OldRestored) {
        $RestoredReceiptSHA256 = Get-NormalizedSHA256 -LiteralPath $ReceiptPath
        if ($RestoredReceiptSHA256 -ne $OldReceiptSHA256 -or
            (Get-NormalizedSHA256 -LiteralPath $script:OldReceipt.executable) -ne $OldExecutableSHA256) {
            throw 'Restored old broker receipt or executable identity changed.'
        }
        $OldParentAfter = @(Get-ParentEvidence -Store $script:OldReceipt.storeRoot)
        Assert-ParentEvidenceEqual -Before $OldParentBefore -After $OldParentAfter -Label 'Existing broker'
        $OldStatusAfterCapture = Invoke-NativeCapture -LiteralPath $script:OldReceipt.executable -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'old-storage-status-after.json') -WorkingDirectory $ArtifactRoot
        if ($OldStatusAfterCapture.ExitCode -ne 0) { throw 'Restored old broker status failed.' }
        $OldStatusAfter = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'old-storage-status-after.json')
        $OldStatusAfterValue = Assert-ZeroProtectedStatus -Status $OldStatusAfter -Label 'Restored old broker'
        if ([int]$OldStatusAfterValue.parentCount -ne [int]$OldStatusBeforeValue.parentCount) {
            throw 'Restored old broker parent count changed.'
        }
    }
}
catch {
    if ($null -eq $Failure) { $Failure = $_.Exception.ToString() }
    else { $Failure = $Failure + [Environment]::NewLine + 'Post-state: ' + $_.Exception.ToString() }
}

$DriveLetterEqual = (@($PreState.driveLetters) -join ',') -eq (@($PostState.driveLetters) -join ',')
$FinalResidualZero = $OldRestored -and $NewBrokerRemoved -and
    $null -ne $PostState.service -and $PostState.service.state -eq 'Running' -and
    $PostState.fileBackedDisks.Count -eq 0 -and $PostState.relatedProcesses.Count -eq 0 -and
    $DriveLetterEqual -and -not (Test-Path -LiteralPath $StoreRoot) -and
    -not (Test-Path -LiteralPath $PreservedReceiptPath)
if (-not $FinalResidualZero -and $null -eq $Failure) {
    $Failure = 'Final broker/store/disk/drive/process residual or old identity restoration gate failed.'
}
$ScenarioGate = $FaultInjected -and $null -ne $FaultEvidence -and $null -ne $TerminalEvidence -and
    $null -ne $SourceBefore -and $null -ne $SourceAfter -and
    $null -ne $NewParentBefore -and $null -ne $NewParentAfter
$Passed = $null -eq $Failure -and $FinalResidualZero -and $ScenarioGate
if (-not $Passed) {
    if ($NewBrokerInstalled -or $PostState.fileBackedDisks.Count -ne 0) { $CleanupState = 'uncertain' }
    elseif ($CleanupState -eq 'released') { $CleanupState = 'preserved' }
}
$PassVerdict = switch ($Scenario) {
    'ClientTermination' { 'HONEYBEE_PROTOCOL3_CLIENT_TERMINATION_RECOVERY_PASS' }
    'UnityTermination' { 'HONEYBEE_PROTOCOL3_UNITY_TERMINATION_RECOVERY_PASS' }
    'BrokerRestart' { 'HONEYBEE_PROTOCOL3_BROKER_RESTART_RECOVERY_PASS' }
}

Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'post-state.json') -Value $PostState
Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'old-parent-after.json') -Value $OldParentAfter
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { $PassVerdict } else { 'FAILED' }
    scenario = $Scenario
    cleanupState = $CleanupState
    manualRecoveryRequired = $CleanupState -eq 'uncertain'
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    testplay = [ordered]@{ commit = $ExpectedTestPlayCommit; sha256 = $TestPlaySHA256; version = 'v0.14.0-dev' }
    honeyBee = [ordered]@{ commit = $HoneyBeeCommit; cliSHA256 = $HoneyBeeCLISHA256; runtime = $HoneyBeeRuntime }
    workspaceStorage = [ordered]@{ contractCommit = $WorkspaceStorageContractCommit; sha256 = $WorkspaceStorageSHA256 }
    bridgePackageTreeSHA256 = $BridgePackageSHA
    oldBroker = [ordered]@{
        storeRoot = $script:OldReceipt.storeRoot
        receiptSHA256 = $OldReceiptSHA256
        executableSHA256 = $OldExecutableSHA256
        parentCount = $OldParentBefore.Count
        restored = $OldRestored
    }
    newStoreRoot = $StoreRoot
    sourceProject = $SourceProject
    sourceUnchanged = $null -ne $SourceBefore -and $null -ne $SourceAfter -and $SourceBefore.assets.digest -eq $SourceAfter.assets.digest -and $SourceBefore.packages.digest -eq $SourceAfter.packages.digest -and $SourceBefore.projectSettings.digest -eq $SourceAfter.projectSettings.digest
    parentUnchanged = $null -ne $NewParentBefore -and $null -ne $NewParentAfter -and $NewParentBefore.sha256 -eq $NewParentAfter.sha256
    protocol = if ($null -ne $FaultEvidence) { 3 } else { $null }
    capabilityCount = $CapabilityEvidence.Count
    fault = $FaultEvidence
    terminal = $TerminalEvidence
    brokerRestart = $BrokerRestartEvidence
    brokerStatus = $NewStatus
    oldBrokerStatusAfter = $OldStatusAfter
    finalResidualZero = $FinalResidualZero
    failure = $Failure
}
Write-JsonFile -LiteralPath $SummaryPath -Value $Summary

$Archive = New-ArtifactArchive -ArtifactDirectory $ArtifactRoot -DestinationPath $ZipPath
if (-not $Archive.Success) {
    $ArchiveFailure = "Artifact archive remained locked after $($Archive.Attempts) attempts: $($Archive.Error)"
    if ($null -eq $Failure) { $Failure = $ArchiveFailure }
    else { $Failure = $Failure + [Environment]::NewLine + 'Archive: ' + $ArchiveFailure }
    $Passed = $false
    if ($CleanupState -eq 'released') { $CleanupState = 'preserved' }
    $Summary.status = 'FAILED'
    $Summary.verdict = 'FAILED'
    $Summary.cleanupState = $CleanupState
    $Summary.failure = $Failure
    Write-JsonFile -LiteralPath $SummaryPath -Value $Summary
}
$ZipSHA256 = if ($Archive.Success) { Get-NormalizedSHA256 -LiteralPath $ZipPath } else { $null }
$Completion = [ordered]@{
    status = $Summary.status
    verdict = $Summary.verdict
    cleanupState = $CleanupState
    artifact = $ArtifactRoot
    zip = if ($Archive.Success) { $ZipPath } else { $null }
    zipSHA256 = $ZipSHA256
    summary = $SummaryPath
    archive = [ordered]@{ succeeded = $Archive.Success; attempts = $Archive.Attempts; error = $Archive.Error }
}
Write-JsonFile -LiteralPath $CompletionPath -Value $Completion

Write-Output "HONEYBEE_PROTOCOL3_RECOVERY_STATUS=$($Summary.status)"
Write-Output "HONEYBEE_PROTOCOL3_RECOVERY_VERDICT=$($Summary.verdict)"
Write-Output "HONEYBEE_PROTOCOL3_RECOVERY_CLEANUP=$CleanupState"
Write-Output "HONEYBEE_PROTOCOL3_RECOVERY_ARTIFACT_ROOT=$ArtifactRoot"
if ($Archive.Success) {
    Write-Output "HONEYBEE_PROTOCOL3_RECOVERY_ARTIFACT_ZIP=$ZipPath"
    Write-Output "HONEYBEE_PROTOCOL3_RECOVERY_ARTIFACT_SHA256=$ZipSHA256"
}
Write-Output "HONEYBEE_PROTOCOL3_RECOVERY_COMPLETION=$CompletionPath"
if (-not $Passed) {
    Write-Error $Failure
    exit 1
}
