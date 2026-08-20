[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$UnityEditorPath,

    [Parameter(Mandatory = $true)]
    [string]$ReleaseArchivePath,

    [Parameter(Mandatory = $true)]
    [string]$ChecksumsPath,

    [Parameter(Mandatory = $true)]
    [ValidateSet(3, 7)]
    [int]$ObservationDay,

    [datetime]$PublishedAtUtc = [datetime]'2026-08-13T09:57:14Z',

    [switch]$InstallApproved,

    [switch]$QuotaMutationApproved
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Write-JsonFile {
    param([string]$LiteralPath, [object]$Value)
    $Json = ($Value | ConvertTo-Json -Depth 24) + [Environment]::NewLine
    [IO.File]::WriteAllText($LiteralPath, $Json, [Text.UTF8Encoding]::new($false))
}

function Invoke-ObservationScript {
    param([string]$LiteralPath, [string[]]$ArgumentList, [string]$OutputPath)
    $PreviousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $Lines = @(& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $LiteralPath @ArgumentList 2>&1)
        $ExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $PreviousPreference
    }
    [IO.File]::WriteAllLines($OutputPath, [string[]]$Lines, [Text.UTF8Encoding]::new($false))
    return [pscustomobject]@{ ExitCode = $ExitCode; Lines = @($Lines | ForEach-Object { [string]$_ }) }
}

function Get-OutputValue {
    param([string[]]$Lines, [string]$Name)
    $Prefix = $Name + '='
    $Matches = @($Lines | Where-Object { $_.StartsWith($Prefix, [StringComparison]::Ordinal) })
    if ($Matches.Count -ne 1) { throw "Expected exactly one $Name output, got $($Matches.Count)." }
    return $Matches[0].Substring($Prefix.Length)
}

function Confirm-ArtifactHash {
    param([string]$LiteralPath, [string]$ExpectedSHA256)
    if (-not (Test-Path -LiteralPath $LiteralPath -PathType Leaf)) { throw "Artifact was not found: $LiteralPath" }
    $Actual = (Get-FileHash -LiteralPath $LiteralPath -Algorithm SHA256).Hash
    if ($Actual -ne $ExpectedSHA256) { throw "Artifact SHA-256 mismatch: expected=$ExpectedSHA256 actual=$Actual path=$LiteralPath" }
}

if (-not $InstallApproved) { throw 'Pass -InstallApproved after approving the broker install/uninstall lifecycle.' }
if (-not $QuotaMutationApproved) { throw 'Pass -QuotaMutationApproved after approving temporary harness-owned quota changes.' }
if (-not (Test-Path -LiteralPath $UnityEditorPath -PathType Leaf)) { throw "Unity Editor was not found: $UnityEditorPath" }
foreach ($Path in @($ReleaseArchivePath, $ChecksumsPath)) {
    if (-not [IO.Path]::IsPathRooted($Path) -or -not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Required release input must be an existing absolute file: $Path"
    }
}

$Published = $PublishedAtUtc.ToUniversalTime()
$NotBefore = $Published.AddDays($ObservationDay)
$Now = (Get-Date).ToUniversalTime()
if ($Now -lt $NotBefore) {
    throw "Day $ObservationDay observation cannot start before $($NotBefore.ToString('o')); current=$($Now.ToString('o'))."
}

$Started = Get-Date
$Stamp = $Started.ToString('yyyyMMdd-HHmmss-fff')
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-rc-day$ObservationDay-observation-$Stamp"
$ScratchRoot = Join-Path $env:TEMP "testplay-vhdx-diff-rc-day$ObservationDay-scratch-$Stamp"
$ZipPath = "$ArtifactRoot.zip"
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$ReleaseScript = Join-Path $PSScriptRoot 'run-vhdx-diff-release-candidate.ps1'
$QuotaScript = Join-Path $PSScriptRoot 'run-vhdx-diff-quota-lru-retained.ps1'
New-Item -ItemType Directory -Path $ArtifactRoot, $ScratchRoot | Out-Null

$Failure = $null
$RC = $null
$Quota = $null
$RCArtifact = $null
$RCArtifactSHA256 = $null
$QuotaArtifact = $null
$QuotaArtifactSHA256 = $null

Start-Transcript -Path (Join-Path $ArtifactRoot 'terminal-transcript.txt') -Force | Out-Null
try {
    Expand-Archive -LiteralPath $ReleaseArchivePath -DestinationPath $ScratchRoot
    $Candidates = @(Get-ChildItem -LiteralPath $ScratchRoot -Recurse -File -Filter 'testplay.exe')
    if ($Candidates.Count -ne 1) { throw "Expected exactly one testplay.exe in the release archive, got $($Candidates.Count)." }
    $CandidateExecutable = $Candidates[0].FullName

    $RC = Invoke-ObservationScript $ReleaseScript @(
        '-UnityEditorPath', $UnityEditorPath,
        '-ReleaseArchivePath', (Resolve-Path -LiteralPath $ReleaseArchivePath).Path,
        '-ChecksumsPath', (Resolve-Path -LiteralPath $ChecksumsPath).Path,
        '-ExpectedVersion', 'v0.13.0-rc.1',
        '-InstallApproved'
    ) (Join-Path $ArtifactRoot 'release-candidate-smoke-output.txt')
    if ($RC.ExitCode -ne 0) { throw "Release-candidate smoke failed: exit=$($RC.ExitCode)" }
    if ((Get-OutputValue $RC.Lines 'VHDX_DIFF_RC_STATUS') -ne 'PASS') { throw 'Release-candidate smoke did not report PASS.' }
    $RCArtifact = Get-OutputValue $RC.Lines 'VHDX_DIFF_RC_ARTIFACT_ZIP'
    $RCArtifactSHA256 = Get-OutputValue $RC.Lines 'VHDX_DIFF_RC_ARTIFACT_SHA256'
    Confirm-ArtifactHash $RCArtifact $RCArtifactSHA256

    $Quota = Invoke-ObservationScript $QuotaScript @(
        '-UnityEditorPath', $UnityEditorPath,
        '-CandidateExecutablePath', $CandidateExecutable,
        '-InstallApproved',
        '-QuotaMutationApproved'
    ) (Join-Path $ArtifactRoot 'quota-lru-retained-output.txt')
    if ($Quota.ExitCode -ne 0) { throw "Quota/LRU/retained observation failed: exit=$($Quota.ExitCode)" }
    if ((Get-OutputValue $Quota.Lines 'VHDX_DIFF_QUOTA_LRU_STATUS') -ne 'PASS') { throw 'Quota/LRU/retained observation did not report PASS.' }
    $QuotaArtifact = Get-OutputValue $Quota.Lines 'VHDX_DIFF_QUOTA_LRU_ARTIFACT_ZIP'
    $QuotaArtifactSHA256 = Get-OutputValue $Quota.Lines 'VHDX_DIFF_QUOTA_LRU_ARTIFACT_SHA256'
    Confirm-ArtifactHash $QuotaArtifact $QuotaArtifactSHA256
}
catch {
    $Failure = $_.Exception.ToString()
}
finally {
    Stop-Transcript | Out-Null
    if (Test-Path -LiteralPath $ScratchRoot) { Remove-Item -LiteralPath $ScratchRoot -Recurse -Force }
}

$Passed = $null -eq $Failure -and $null -ne $RC -and $null -ne $Quota
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { "VHDX_DIFF_RC_DAY_${ObservationDay}_OBSERVATION_PASS" } else { 'FAILED' }
    observationDay = $ObservationDay
    publishedAt = $Published.ToString('o')
    notBefore = $NotBefore.ToString('o')
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    releaseArchive = (Resolve-Path -LiteralPath $ReleaseArchivePath).Path
    releaseArchiveSHA256 = (Get-FileHash -LiteralPath $ReleaseArchivePath -Algorithm SHA256).Hash
    releaseCandidateArtifact = $RCArtifact
    releaseCandidateArtifactSHA256 = $RCArtifactSHA256
    quotaLRURetainedArtifact = $QuotaArtifact
    quotaLRURetainedArtifactSHA256 = $QuotaArtifactSHA256
    failure = $Failure
    notMeasured = @('multi-hardware compatibility', 'production readiness', 'stable release readiness')
}
Write-JsonFile $SummaryPath $Summary
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash
Write-Output "VHDX_DIFF_RC_OBSERVATION_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_RC_OBSERVATION_VERDICT=$($Summary.verdict)"
Write-Output "VHDX_DIFF_RC_OBSERVATION_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_RC_OBSERVATION_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
