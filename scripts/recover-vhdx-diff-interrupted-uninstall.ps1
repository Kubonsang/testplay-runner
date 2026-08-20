[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$ExpectedStoreRoot,

    [switch]$RecoveryApproved
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
    [IO.File]::WriteAllText(
        $LiteralPath,
        ($Value | ConvertTo-Json -Depth 12) + [Environment]::NewLine,
        [Text.UTF8Encoding]::new($false)
    )
}

function Invoke-NativeCapture {
    param([string]$LiteralPath, [string[]]$ArgumentList, [string]$OutputPath)
    $PreviousPreference = $ErrorActionPreference
    try {
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

if (-not $RecoveryApproved) {
    throw 'Pass -RecoveryApproved only for the exact interrupted uninstall residual.'
}
if (-not (Test-Administrator)) {
    throw 'Administrator PowerShell is required.'
}

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$ExpectedStoreRoot = [IO.Path]::GetFullPath($ExpectedStoreRoot)
$ReceiptPath = Join-Path $env:ProgramData 'TestPlay\storage-install.json'
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-uninstall-recovery-$Stamp"
$ZipPath = "$ArtifactRoot.zip"
$ExecutablePath = Join-Path $ArtifactRoot 'testplay-vhdx-diff-recovery.exe'
$TranscriptPath = Join-Path $ArtifactRoot 'terminal-transcript.txt'

if (-not (Test-Path -LiteralPath $ReceiptPath -PathType Leaf)) {
    throw "Install receipt is absent: $ReceiptPath"
}
$Receipt = Get-Content -Raw -LiteralPath $ReceiptPath | ConvertFrom-Json
if (-not [string]::Equals(
    [IO.Path]::GetFullPath([string]$Receipt.storeRoot),
    $ExpectedStoreRoot,
    [StringComparison]::OrdinalIgnoreCase
)) {
    throw "Receipt store root does not match the approved root: $($Receipt.storeRoot)"
}
if (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) {
    throw 'The broker service still exists; this recovery only handles a service-deleted uninstall.'
}
if (-not (Test-Path -LiteralPath $ExpectedStoreRoot -PathType Container)) {
    throw "Expected residual store is absent: $ExpectedStoreRoot"
}

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
$Started = Get-Date
$Failure = $null
$PreDisks = @(Get-FileBackedDisks)
$PreListing = @(
    Get-ChildItem -LiteralPath $ExpectedStoreRoot -Force -Recurse -Depth 3 |
        Select-Object -First 100 FullName, Length, Attributes, CreationTimeUtc, LastWriteTimeUtc
)
Write-Utf8NoBom -LiteralPath (Join-Path $ArtifactRoot 'pre-state.json') -Value ([ordered]@{
    receipt = $Receipt
    serviceExists = $false
    fileBackedDisks = @($PreDisks)
    storeListing = @($PreListing)
})

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
    $Uninstall = Invoke-NativeCapture -LiteralPath $ExecutablePath `
        -ArgumentList @('storage', 'uninstall') `
        -OutputPath (Join-Path $ArtifactRoot 'storage-uninstall.txt')
    if ($Uninstall.ExitCode -ne 0) {
        throw "storage uninstall recovery failed: exit=$($Uninstall.ExitCode)"
    }
}
catch {
    $Failure = $_.Exception.ToString()
}
finally {
    Stop-Transcript | Out-Null
}

$PostDisks = @(Get-FileBackedDisks)
$ResidualZero = (
    $PostDisks.Count -eq 0 -and
    -not (Get-Service -Name TestPlayStorageBroker -ErrorAction SilentlyContinue) -and
    -not (Test-Path -LiteralPath $ReceiptPath) -and
    -not (Test-Path -LiteralPath $ExpectedStoreRoot) -and
    -not (Test-Path -LiteralPath ([string]$Receipt.workspaceRoot))
)
$Passed = $null -eq $Failure -and $ResidualZero
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'RECOVERED' } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    approvedStoreRoot = $ExpectedStoreRoot
    receiptPath = $ReceiptPath
    preFileBackedDisks = @($PreDisks)
    postFileBackedDisks = @($PostDisks)
    residualZero = $ResidualZero
    failure = $Failure
}
Write-Utf8NoBom -LiteralPath (Join-Path $ArtifactRoot 'summary.json') -Value $Summary
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash

Write-Output "VHDX_DIFF_UNINSTALL_RECOVERY_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_UNINSTALL_RECOVERY_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_UNINSTALL_RECOVERY_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
