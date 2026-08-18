[CmdletBinding()]
param(
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

    [switch]$InstallApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ExpectedUnityVersion = '6000.3.8f1'
$PackageName = 'com.testplay.bridge'
$ProvisionTest = 'TestPlayFixture.Tests.DeterministicPlayModeTests.DeterministicPlayModeSmokeTest'
$WarmTest = 'TestPlayFixture.Tests.LibraryMountTests.DeterministicRuntimeStateTest'
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$ServiceName = 'TestPlayStorageBroker'
$Timestamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-honeybee-protocol3-e2e-$Timestamp"
$SessionRoot = Join-Path $env:LOCALAPPDATA "TestPlay\HoneyBeeProtocol3E2E-$Timestamp"
$StoreRoot = Join-Path $SessionRoot 'store'
$SourceProject = Join-Path $ArtifactRoot 'source-project'
$HoneyBeeRunRoot = Join-Path $ArtifactRoot 'honeybee-run'
$PreservedReceiptPath = Join-Path (Split-Path -Parent $ReceiptPath) "storage-install.protocol3-preserved-$Timestamp.json"
$ZipPath = "$ArtifactRoot.zip"
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$CompletionPath = Join-Path $env:TEMP "testplay-honeybee-protocol3-e2e-$Timestamp-complete.json"

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

function Get-NormalizedSHA256 {
    param([string]$LiteralPath)
    return (Get-FileHash -LiteralPath $LiteralPath -Algorithm SHA256).Hash.ToLowerInvariant()
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
    $Files = @($Roots | ForEach-Object { Get-ChildItem -LiteralPath $_ -Recurse -File -Force } |
        Sort-Object FullName)
    if ($Files.Count -eq 0) { throw 'HoneyBee built runtime is empty.' }
    $Builder = [Text.StringBuilder]::new()
    [long]$LogicalBytes = 0
    foreach ($File in $Files) {
        $Relative = $File.FullName.Substring($Repository.TrimEnd('\').Length).TrimStart('\').Replace('\', '/')
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

if (-not $InstallApproved) { throw 'Explicit -InstallApproved is required.' }
if (-not (Test-Administrator)) { throw 'Administrator PowerShell is required.' }
if ((Test-Path -LiteralPath $ArtifactRoot) -or
    (Test-Path -LiteralPath $SessionRoot) -or
    (Test-Path -LiteralPath $ZipPath) -or
    (Test-Path -LiteralPath $PreservedReceiptPath)) {
    throw 'A supposedly unique E2E path already exists.'
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
$NewParentBefore = $null
$NewParentAfter = $null
$SourceBefore = $null
$SourceAfter = $null
$NewStatus = $null
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
    $HoneyBee = Invoke-NativeCapture -LiteralPath $Node `
        -ArgumentList @($HoneyBeeCLI, 'unity', 'run', '--config', $HoneyBeeConfigPath, '--task', $Task, '--json') `
        -OutputPath (Join-Path $ArtifactRoot 'honeybee-e2e-output.json') `
        -WorkingDirectory $HoneyBeeRunRoot
    if ($HoneyBee.ExitCode -ne 0) { throw "HoneyBee protocol 3 E2E failed: exit=$($HoneyBee.ExitCode)" }
    $E2EResult = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'honeybee-e2e-output.json')
    if ($E2EResult.status -ne 'completed' -or $E2EResult.evidence.kind -ne 'unity-capability-evidence' -or
        $E2EResult.release.kind -ne 'workspace-release-receipt') {
        throw "HoneyBee E2E result is not completed with evidence and release: status=$($E2EResult.status)"
    }
    $JournalPath = [string]$E2EResult.journalPath
    if (-not (Test-Path -LiteralPath $JournalPath -PathType Leaf)) { throw 'HoneyBee journal is missing.' }
    $Events = @(Get-Content -LiteralPath $JournalPath | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | ForEach-Object { $_ | ConvertFrom-Json })
    if ($Events.Count -lt 2 -or $Events[$Events.Count - 2].type -ne 'workspace.released' -or $Events[$Events.Count - 1].type -ne 'workflow.completed') {
        throw 'HoneyBee terminal journal ordering is invalid.'
    }
    foreach ($RequiredType in @('workspace.acquired', 'editor.ownership-established', 'editor.bridge-bound')) {
        if (@($Events | Where-Object { $_.type -eq $RequiredType }).Count -ne 1) {
            throw "HoneyBee journal is missing exact event: $RequiredType"
        }
    }
    $OwnershipEvent = @($Events | Where-Object { $_.type -eq 'editor.ownership-established' })[0]
    $BindingEvent = @($Events | Where-Object { $_.type -eq 'editor.bridge-bound' })[0]
    $WorkspaceEvent = @($Events | Where-Object { $_.type -eq 'workspace.acquired' })[0]
    $WorkspaceID = [string]$WorkspaceEvent.payload.workspaceId
    $EditorPID = [int]$OwnershipEvent.payload.pid
    $BridgeSessionID = [string]$BindingEvent.payload.bridgeSessionId
    if ([string]::IsNullOrWhiteSpace($WorkspaceID) -or $EditorPID -le 0 -or [string]::IsNullOrWhiteSpace($BridgeSessionID)) {
        throw 'HoneyBee exact bridge binding identity is incomplete.'
    }
    $CapabilityEvents = @($Events | Where-Object { $_.type -eq 'capability.completed' })
    if ($CapabilityEvents.Count -ne 2) { throw "Expected exactly two completed capabilities, got $($CapabilityEvents.Count)." }
    $CapabilityEvidence = @(
        Assert-CapabilityEvidence -JournalPath $JournalPath -Event $CapabilityEvents[0] -Kind 'compile' -ExpectedWorkspaceID $WorkspaceID -ExpectedSessionID $BridgeSessionID -ExpectedEditorPID $EditorPID
        Assert-CapabilityEvidence -JournalPath $JournalPath -Event $CapabilityEvents[1] -Kind 'warm-test' -ExpectedWorkspaceID $WorkspaceID -ExpectedSessionID $BridgeSessionID -ExpectedEditorPID $EditorPID
    )
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
    if ($NewBrokerInstalled) {
        try {
            $StatusCapture = Invoke-NativeCapture -LiteralPath $TestPlayExecutable -ArgumentList @('storage', 'status', '--json') -OutputPath (Join-Path $ArtifactRoot 'new-storage-status-after-failure.json') -WorkingDirectory $ArtifactRoot
            if ($StatusCapture.ExitCode -eq 0) {
                $FailureStatus = Read-NativeJson -LiteralPath (Join-Path $ArtifactRoot 'new-storage-status-after-failure.json')
                $null = Assert-ZeroProtectedStatus -Status $FailureStatus -Label 'Failed protocol 3 E2E broker'
                if (@(Get-FileBackedDisks).Count -eq 0 -and @(Get-RelatedProcesses).Count -eq 0 -and
                    @(Get-ChildItem -LiteralPath $script:OldReceipt.workspaceRoot -Force).Count -eq 0) {
                    $UninstallAfterFailure = Invoke-NativeCapture -LiteralPath $TestPlayExecutable -ArgumentList @('storage', 'uninstall') -OutputPath (Join-Path $ArtifactRoot 'new-storage-uninstall-after-failure.txt') -WorkingDirectory $ArtifactRoot
                    if ($UninstallAfterFailure.ExitCode -eq 0) {
                        $NewBrokerInstalled = $false
                        $NewBrokerRemoved = $true
                        Wait-ServiceAbsent
                    }
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
$Passed = $null -eq $Failure -and $FinalResidualZero -and $null -ne $E2EResult
if (-not $Passed -and $CleanupState -eq 'released') { $CleanupState = 'preserved' }

Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'post-state.json') -Value $PostState
Write-JsonFile -LiteralPath (Join-Path $ArtifactRoot 'old-parent-after.json') -Value $OldParentAfter
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { 'HONEYBEE_PROTOCOL3_E2E_PASS' } else { 'FAILED' }
    cleanupState = $CleanupState
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
    protocol = if ($CapabilityEvidence.Count -eq 2) { 3 } else { $null }
    capabilityCount = $CapabilityEvidence.Count
    brokerStatus = $NewStatus
    oldBrokerStatusAfter = $OldStatusAfter
    finalResidualZero = $FinalResidualZero
    failure = $Failure
}
Write-JsonFile -LiteralPath $SummaryPath -Value $Summary

Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipSHA256 = Get-NormalizedSHA256 -LiteralPath $ZipPath
$Completion = [ordered]@{
    status = $Summary.status
    verdict = $Summary.verdict
    cleanupState = $CleanupState
    artifact = $ArtifactRoot
    zip = $ZipPath
    zipSHA256 = $ZipSHA256
    summary = $SummaryPath
}
Write-JsonFile -LiteralPath $CompletionPath -Value $Completion

Write-Output "HONEYBEE_PROTOCOL3_E2E_STATUS=$($Summary.status)"
Write-Output "HONEYBEE_PROTOCOL3_E2E_VERDICT=$($Summary.verdict)"
Write-Output "HONEYBEE_PROTOCOL3_E2E_CLEANUP=$CleanupState"
Write-Output "HONEYBEE_PROTOCOL3_E2E_ARTIFACT_ZIP=$ZipPath"
Write-Output "HONEYBEE_PROTOCOL3_E2E_ARTIFACT_SHA256=$ZipSHA256"
Write-Output "HONEYBEE_PROTOCOL3_E2E_COMPLETION=$CompletionPath"
if (-not $Passed) {
    Write-Error $Failure
    exit 1
}
