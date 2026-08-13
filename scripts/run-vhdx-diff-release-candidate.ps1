[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$UnityEditorPath,

    [Parameter(Mandatory = $true)]
    [string]$ReleaseArchivePath,

    [Parameter(Mandatory = $true)]
    [string]$ChecksumsPath,

    [string]$ExpectedVersion = 'v0.13.0-rc.1',

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
    [IO.File]::WriteAllText(
        $LiteralPath,
        (($Value | ConvertTo-Json -Depth 20) + [Environment]::NewLine),
        [Text.UTF8Encoding]::new($false)
    )
}

function Invoke-NativeCapture {
    param(
        [string]$LiteralPath,
        [string[]]$ArgumentList,
        [string]$OutputPath
    )
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $lines = @(& $LiteralPath @ArgumentList 2>&1)
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
    [IO.File]::WriteAllLines(
        $OutputPath,
        [string[]]@($lines | ForEach-Object { $_.ToString() }),
        [Text.UTF8Encoding]::new($false)
    )
    return [pscustomobject]@{
        ExitCode = $exitCode
        Lines = @($lines)
        Text = (@($lines | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine)
    }
}

if (-not $InstallApproved) {
    throw 'Pass -InstallApproved after verifying the unsigned RC archive and exact cleanup contract.'
}
if (-not (Test-Administrator)) {
    throw 'Administrator PowerShell is required.'
}
if (-not (Test-Path -LiteralPath $UnityEditorPath -PathType Leaf)) {
    throw "Unity Editor was not found: $UnityEditorPath"
}
if (-not (Test-Path -LiteralPath $ReleaseArchivePath -PathType Leaf)) {
    throw "Release archive was not found: $ReleaseArchivePath"
}
if (-not (Test-Path -LiteralPath $ChecksumsPath -PathType Leaf)) {
    throw "Checksums file was not found: $ChecksumsPath"
}
if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
    throw 'GitHub CLI is required to verify the release attestation.'
}

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$Phase1Script = Join-Path $PSScriptRoot 'run-vhdx-diff-native-phase1.ps1'
$Archive = Get-Item -LiteralPath $ReleaseArchivePath
$Checksums = Get-Item -LiteralPath $ChecksumsPath
$Stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$ArtifactRoot = Join-Path $env:TEMP "testplay-vhdx-diff-release-candidate-$Stamp"
$ScratchRoot = Join-Path $env:TEMP "testplay-vhdx-diff-release-candidate-scratch-$Stamp"
$TranscriptPath = Join-Path $ArtifactRoot 'terminal-transcript.txt'
$SummaryPath = Join-Path $ArtifactRoot 'summary.json'
$ZipPath = "$ArtifactRoot.zip"

foreach ($path in @($ArtifactRoot, $ScratchRoot, $ZipPath)) {
    if (Test-Path -LiteralPath $path) {
        throw "Unique release-candidate path already exists: $path"
    }
}

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
New-Item -ItemType Directory -Path $ScratchRoot | Out-Null
$Started = Get-Date
$Failure = $null
$ChecksumVerified = $false
$AttestationVerified = $false
$VersionVerified = $false
$UnsignedVerified = $false
$Phase1ExitCode = $null
$Phase1Artifact = $null
$Phase1ArtifactSHA256 = $null

Start-Transcript -Path $TranscriptPath -Force | Out-Null
try {
    $matchingChecksums = @()
    foreach ($line in Get-Content -LiteralPath $Checksums.FullName) {
        if ($line -match '^([0-9A-Fa-f]{64})\s+\*?(.+?)\s*$') {
            if ([IO.Path]::GetFileName($matches[2]) -eq $Archive.Name) {
                $matchingChecksums += $matches[1].ToUpperInvariant()
            }
        }
    }
    if ($matchingChecksums.Count -ne 1) {
        throw "Expected one checksum entry for $($Archive.Name), got $($matchingChecksums.Count)."
    }
    $ArchiveSHA256 = (Get-FileHash -LiteralPath $Archive.FullName -Algorithm SHA256).Hash
    if ($ArchiveSHA256 -ne $matchingChecksums[0]) {
        throw "Release archive checksum mismatch: actual=$ArchiveSHA256 expected=$($matchingChecksums[0])"
    }
    $ChecksumVerified = $true
    Write-Utf8NoBom -LiteralPath (Join-Path $ArtifactRoot 'archive-identity.json') -Value ([ordered]@{
        path = $Archive.FullName
        name = $Archive.Name
        length = $Archive.Length
        sha256 = $ArchiveSHA256
        checksumsPath = $Checksums.FullName
    })

    $Attestation = Invoke-NativeCapture -LiteralPath (Get-Command gh).Source `
        -ArgumentList @('attestation', 'verify', $Archive.FullName, '-R', 'Kubonsang/testplay-runner') `
        -OutputPath (Join-Path $ArtifactRoot 'attestation-verification.txt')
    if ($Attestation.ExitCode -ne 0) {
        throw "GitHub attestation verification failed: exit=$($Attestation.ExitCode)"
    }
    $AttestationVerified = $true

    Expand-Archive -LiteralPath $Archive.FullName -DestinationPath $ScratchRoot
    $Candidates = @(Get-ChildItem -LiteralPath $ScratchRoot -Recurse -File -Filter 'testplay.exe')
    if ($Candidates.Count -ne 1) {
        throw "Expected exactly one testplay.exe in the archive, got $($Candidates.Count)."
    }
    $CandidateExecutable = $Candidates[0].FullName
    $Signature = Get-AuthenticodeSignature -LiteralPath $CandidateExecutable
    Write-Utf8NoBom -LiteralPath (Join-Path $ArtifactRoot 'authenticode.json') -Value ([ordered]@{
        status = $Signature.Status.ToString()
        statusMessage = $Signature.StatusMessage
        signerCertificate = if ($null -eq $Signature.SignerCertificate) { $null } else { $Signature.SignerCertificate.Subject }
    })
    if ($Signature.Status.ToString() -ne 'NotSigned') {
        throw "RC signing disclosure mismatch: Authenticode status=$($Signature.Status)"
    }
    $UnsignedVerified = $true

    $Version = Invoke-NativeCapture -LiteralPath $CandidateExecutable `
        -ArgumentList @('version') `
        -OutputPath (Join-Path $ArtifactRoot 'candidate-version.json')
    if ($Version.ExitCode -ne 0 -or [string]::IsNullOrWhiteSpace($Version.Text)) {
        throw "Candidate version command failed: exit=$($Version.ExitCode) lines=$($Version.Lines.Count)"
    }
    $VersionJSON = $Version.Text | ConvertFrom-Json
    if ($VersionJSON.schema_version -ne '1' -or $VersionJSON.version -ne $ExpectedVersion -or
        [string]::IsNullOrWhiteSpace($VersionJSON.commit) -or
        [string]::IsNullOrWhiteSpace($VersionJSON.date)) {
        throw "Candidate version evidence is invalid: version=$($VersionJSON.version)"
    }
    $VersionVerified = $true

    $Phase1 = Invoke-NativeCapture -LiteralPath 'powershell.exe' `
        -ArgumentList @(
            '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $Phase1Script,
            '-UnityEditorPath', $UnityEditorPath,
            '-CandidateExecutablePath', $CandidateExecutable,
            '-InstallApproved', '-RequireUpgrade', '-RequireUnelevatedProbe'
        ) `
        -OutputPath (Join-Path $ArtifactRoot 'phase1-output.txt')
    $Phase1ExitCode = $Phase1.ExitCode
    foreach ($line in $Phase1.Lines) {
        $text = $line.ToString()
        if ($text -match '^VHDX_DIFF_PHASE1_ARTIFACT_ZIP=(.+)$') { $Phase1Artifact = $matches[1] }
        if ($text -match '^VHDX_DIFF_PHASE1_ARTIFACT_SHA256=([0-9A-Fa-f]{64})$') {
            $Phase1ArtifactSHA256 = $matches[1].ToUpperInvariant()
        }
    }
    if ($Phase1.ExitCode -ne 0 -or [string]::IsNullOrWhiteSpace($Phase1Artifact) -or
        [string]::IsNullOrWhiteSpace($Phase1ArtifactSHA256)) {
        throw "Release-candidate native Phase 1 failed: exit=$($Phase1.ExitCode)"
    }
    if (-not (Test-Path -LiteralPath $Phase1Artifact -PathType Leaf)) {
        throw "Native Phase 1 artifact was not found: $Phase1Artifact"
    }
    $MeasuredPhase1Hash = (Get-FileHash -LiteralPath $Phase1Artifact -Algorithm SHA256).Hash
    if ($MeasuredPhase1Hash -ne $Phase1ArtifactSHA256) {
        throw "Native Phase 1 artifact hash mismatch: actual=$MeasuredPhase1Hash expected=$Phase1ArtifactSHA256"
    }
}
catch {
    $Failure = $_.Exception.ToString()
}
finally {
    Stop-Transcript | Out-Null
    if (Test-Path -LiteralPath $ScratchRoot) {
        Remove-Item -LiteralPath $ScratchRoot -Recurse -Force
    }
}

$Passed = (
    $null -eq $Failure -and $ChecksumVerified -and $AttestationVerified -and
    $VersionVerified -and $UnsignedVerified -and $Phase1ExitCode -eq 0
)
$Summary = [ordered]@{
    schemaVersion = 1
    status = if ($Passed) { 'PASS' } else { 'FAILED' }
    verdict = if ($Passed) { 'VHDX_DIFF_RC_ASSET_SMOKE_PASS' } else { 'FAILED' }
    startedAt = $Started.ToUniversalTime().ToString('o')
    finishedAt = (Get-Date).ToUniversalTime().ToString('o')
    expectedVersion = $ExpectedVersion
    releaseArchive = $Archive.FullName
    releaseArchiveSHA256 = if ($ChecksumVerified) { $ArchiveSHA256 } else { $null }
    checksumVerified = $ChecksumVerified
    attestationVerified = $AttestationVerified
    versionVerified = $VersionVerified
    authenticodeUnsignedVerified = $UnsignedVerified
    nativePhase1ExitCode = $Phase1ExitCode
    nativePhase1Artifact = $Phase1Artifact
    nativePhase1ArtifactSHA256 = $Phase1ArtifactSHA256
    failure = $Failure
    notMeasured = @(
        'Authenticode signature',
        'automatic auto-backend promotion',
        'GNF eight workers',
        'long-running child growth',
        'generalized performance superiority',
        'production readiness',
        'release readiness'
    )
}
Write-Utf8NoBom -LiteralPath $SummaryPath -Value $Summary
Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $ZipPath -Force
$ZipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash

Write-Output "VHDX_DIFF_RC_STATUS=$($Summary.status)"
Write-Output "VHDX_DIFF_RC_VERDICT=$($Summary.verdict)"
Write-Output "VHDX_DIFF_RC_ARTIFACT_ZIP=$ZipPath"
Write-Output "VHDX_DIFF_RC_ARTIFACT_SHA256=$ZipHash"
if (-not $Passed) { exit 1 }
