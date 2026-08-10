[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$UnityEditorPath,

    [switch]$InstallApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Test-Administrator {
    $principal = [Security.Principal.WindowsPrincipal](
        [Security.Principal.WindowsIdentity]::GetCurrent()
    )
    return $principal.IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator
    )
}

function Write-Utf8NoBom {
    param([string]$LiteralPath, [object]$Value)
    $json = $Value | ConvertTo-Json -Depth 12
    [IO.File]::WriteAllText(
        $LiteralPath,
        $json + [Environment]::NewLine,
        [Text.UTF8Encoding]::new($false)
    )
}

function Invoke-NativeCapture {
    param(
        [string]$LiteralPath,
        [string[]]$ArgumentList,
        [string]$OutputPath
    )
    $PreviousPreference = $ErrorActionPreference
    try {
        # Windows PowerShell 5.1 wraps native stderr as non-terminating
        # NativeCommandError records. CLI progress on stderr is not failure;
        # the process exit code remains authoritative.
        $ErrorActionPreference = 'Continue'
        $Lines = @(& $LiteralPath @ArgumentList 2>&1)
        $ExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $PreviousPreference
    }
    [IO.File]::WriteAllLines(
        $OutputPath,
        [string[]]@($Lines | ForEach-Object { $_.ToString() }),
        [Text.UTF8Encoding]::new($false)
    )
    return [pscustomobject]@{ ExitCode = $ExitCode; Lines = @($Lines) }
}

function Get-FileBackedDisks {
    return @(
        Get-Disk -ErrorAction SilentlyContinue |
            Where-Object { $_.BusType -eq 'File Backed Virtual' } |
            Select-Object Number, FriendlyName, BusType, PartitionStyle
    )
}

if (-not $InstallApproved) {
    throw 'Pass -InstallApproved after reviewing the unique store and cleanup contract.'
}
if (-not (Test-Administrator)) {
    throw 'Administrator PowerShell is required.'
}
if (-not (Test-Path -LiteralPath $UnityEditorPath -PathType Leaf)) {
    throw "Unity Editor was not found: $UnityEditorPath"
}

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$FixturePath = Join-Path $RepositoryRoot 'testdata\unity-vhdx-fixture'
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-native-phase1-$Stamp"
$StoreRoot = Join-Path $env:ProgramData "TestPlay\VHDXDiffNativePhase1-$Stamp"
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-phase1.exe'
$TranscriptPath = Join-Path $ArtifactRoot 'terminal-transcript.txt'
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$ZipPath = "$ArtifactRoot.zip"

if (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) {
    throw 'TestPlayStorageBroker already exists; this harness will not replace it.'
}
if (Test-Path -LiteralPath $ReceiptPath) {
    throw "An install receipt already exists; this harness will not replace it: $ReceiptPath"
}
if (Test-Path -LiteralPath $StoreRoot) {
    throw "Unique storage root already exists: $StoreRoot"
}
if (Test-Path -LiteralPath $ArtifactRoot) {
    throw "Unique artifact root already exists: $ArtifactRoot"
}

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
$PreDisks = @(Get-FileBackedDisks)
$Installed = $false
$Uninstalled = $false
$Failure = $null
$Started = Get-Date
$MarkerWasSet = Test-Path Env:TESTPLAY_UNITY_FIXTURE_MARKER
$PreviousMarker = $env:TESTPLAY_UNITY_FIXTURE_MARKER
$env:TESTPLAY_UNITY_FIXTURE_MARKER = "vhdx-diff-native-phase1-$Stamp"

Start-Transcript -Path $TranscriptPath -Force | Out-Null
try {
    Push-Location $RepositoryRoot
    try {
        & go build -o $ExecutablePath .\cmd\testplay
        if ($LASTEXITCODE -ne 0) { throw "go build failed: exit=$LASTEXITCODE" }
    }
    finally {
        Pop-Location
    }

    $Install = Invoke-NativeCapture -LiteralPath $ExecutablePath `
        -ArgumentList @('storage', 'install', '--root', $StoreRoot) `
        -OutputPath (Join-Path $ArtifactRoot 'storage-install.txt')
    if ($Install.ExitCode -ne 0) { throw "storage install failed: exit=$($Install.ExitCode)" }
    $Installed = $true

    foreach ($Platform in @('edit_mode', 'play_mode')) {
        $ConfigPath = Join-Path $ArtifactRoot "testplay-$Platform.json"
        $ResultRoot = Join-Path $ArtifactRoot "results-$Platform"
        Write-Utf8NoBom -LiteralPath $ConfigPath -Value ([ordered]@{
            schema_version = '1'
            unity_path = $UnityEditorPath
            project_path = $FixturePath
            test_platform = $Platform
            timeout = [ordered]@{ total_ms = 600000 }
            result_dir = $ResultRoot
            workspace = [ordered]@{
                backend = 'vhdx-diff'
                store_root = $StoreRoot
                store_max_allocated_bytes = 34359738368
                minimum_host_free_bytes = 21474836480
            }
        })
        $Run = Invoke-NativeCapture -LiteralPath $ExecutablePath `
            -ArgumentList @('--config', $ConfigPath, 'run', '--workspace-backend', 'vhdx-diff', '--workspace-store-root', $StoreRoot, '--no-bridge') `
            -OutputPath (Join-Path $ArtifactRoot "run-$Platform.txt")
        if ($Run.ExitCode -ne 0) {
            throw "$Platform fixture run failed: exit=$($Run.ExitCode)"
        }
    }

    $Status = Invoke-NativeCapture -LiteralPath $ExecutablePath `
        -ArgumentList @('storage', 'status', '--json') `
        -OutputPath (Join-Path $ArtifactRoot 'storage-status.json')
    if ($Status.ExitCode -ne 0) { throw "storage status failed: exit=$($Status.ExitCode)" }
}
catch {
    $Failure = $_.Exception.ToString()
}
finally {
    if ($Installed) {
        try {
            $Uninstall = Invoke-NativeCapture -LiteralPath $ExecutablePath `
                -ArgumentList @('storage', 'uninstall') `
                -OutputPath (Join-Path $ArtifactRoot 'storage-uninstall.txt')
            $Uninstalled = $Uninstall.ExitCode -eq 0
            if (-not $Uninstalled -and $null -eq $Failure) {
                $Failure = "storage uninstall failed: exit=$($Uninstall.ExitCode)"
            }
        }
        catch {
            if ($null -eq $Failure) { $Failure = $_.Exception.ToString() }
        }
    }
    if ($MarkerWasSet) {
        $env:TESTPLAY_UNITY_FIXTURE_MARKER = $PreviousMarker
    }
    else {
        Remove-Item Env:TESTPLAY_UNITY_FIXTURE_MARKER -ErrorAction SilentlyContinue
    }
    Stop-Transcript | Out-Null
}

$PostDisks = @(Get-FileBackedDisks)
$PreIDs = @($PreDisks | ForEach-Object { $_.Number })
$NewDisks = @($PostDisks | Where-Object { $PreIDs -notcontains $_.Number })
$ResidualZero = (
    $NewDisks.Count -eq 0 -and
    -not (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) -and
    -not (Test-Path -LiteralPath $ReceiptPath) -and
    -not (Test-Path -LiteralPath $StoreRoot)
)
$Passed = $null -eq $Failure -and $Uninstalled -and $ResidualZero
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { 'VHDX_DIFF_NATIVE_PHASE1_PROMISING' } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    repository = $RepositoryRoot
    unityEditor = $UnityEditorPath
    fixture = $FixturePath
    storeRoot = $StoreRoot
    installed = $Installed
    uninstalled = $Uninstalled
    residualZero = $ResidualZero
    preFileBackedDisks = @($PreDisks)
    postFileBackedDisks = @($PostDisks)
    newFileBackedDisks = @($NewDisks)
    failure = $Failure
    notMeasured = @(
        'unauthorized SID native client',
        'GNF single/multi worker',
        'forced termination recovery',
        'broker restart recovery',
        'Windows reboot recovery',
        'quota/LRU native behavior',
        'production readiness',
        'release readiness'
    )
}
Write-Utf8NoBom -LiteralPath $SummaryPath -Value $Summary
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash

Write-Output "VHDX_DIFF_PHASE1_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_PHASE1_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_PHASE1_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
