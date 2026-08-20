[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$UnsignedExecutablePath,

    [Parameter(Mandatory = $true)]
    [string]$SignedExecutablePath,

    [Parameter(Mandatory = $true)]
    [string]$ExpectedSignerSubject,

    [Parameter(Mandatory = $true)]
    [string]$ExpectedSignerThumbprint,

    [Parameter(Mandatory = $true)]
    [string]$ArtifactRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Write-JsonFile {
    param([string]$LiteralPath, [object]$Value)
    [IO.File]::WriteAllText(
        $LiteralPath,
        (($Value | ConvertTo-Json -Depth 12) + [Environment]::NewLine),
        [Text.UTF8Encoding]::new($false)
    )
}

function Get-CertificateEvidence {
    param([Security.Cryptography.X509Certificates.X509Certificate2]$Certificate)
    if ($null -eq $Certificate) { return $null }
    return [ordered]@{
        subject = $Certificate.Subject
        issuer = $Certificate.Issuer
        serialNumber = $Certificate.SerialNumber
        thumbprint = $Certificate.Thumbprint
        notBefore = $Certificate.NotBefore.ToUniversalTime().ToString('o')
        notAfter = $Certificate.NotAfter.ToUniversalTime().ToString('o')
        enhancedKeyUsage = @($Certificate.EnhancedKeyUsageList | ForEach-Object {
            [ordered]@{ name = $_.FriendlyName; oid = $_.ObjectId.Value }
        })
    }
}

foreach ($path in @($UnsignedExecutablePath, $SignedExecutablePath)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Executable was not found: $path"
    }
}
if (Test-Path -LiteralPath $ArtifactRoot) {
    throw "Artifact root already exists: $ArtifactRoot"
}
$signtool = Get-Command signtool.exe -ErrorAction Stop
$expectedThumbprint = $ExpectedSignerThumbprint.Replace(' ', '').ToUpperInvariant()

New-Item -ItemType Directory -Path $ArtifactRoot | Out-Null
$transcriptPath = Join-Path $ArtifactRoot 'terminal-transcript.txt'
$summaryPath = Join-Path $ArtifactRoot 'summary.json'
$signtoolPath = Join-Path $ArtifactRoot 'signtool-verify.txt'
$zipPath = "$ArtifactRoot.zip"
$started = Get-Date
$failure = $null

Start-Transcript -Path $transcriptPath -Force | Out-Null
try {
    $unsigned = Get-AuthenticodeSignature -LiteralPath $UnsignedExecutablePath
    if ($unsigned.Status -ne 'NotSigned') {
        throw "Unsigned input has unexpected Authenticode status: $($unsigned.Status)"
    }

    $signed = Get-AuthenticodeSignature -LiteralPath $SignedExecutablePath
    if ($signed.Status -ne 'Valid') {
        throw "Signed input is not Valid: $($signed.Status) $($signed.StatusMessage)"
    }
    if ($null -eq $signed.SignerCertificate -or
        $signed.SignerCertificate.Subject -ne $ExpectedSignerSubject) {
        throw "Signer subject mismatch: $($signed.SignerCertificate.Subject)"
    }
    if ($signed.SignerCertificate.Thumbprint.ToUpperInvariant() -ne $expectedThumbprint) {
        throw "Signer thumbprint mismatch: $($signed.SignerCertificate.Thumbprint)"
    }
    $codeSigningEKU = @($signed.SignerCertificate.EnhancedKeyUsageList |
        Where-Object { $_.ObjectId.Value -eq '1.3.6.1.5.5.7.3.3' })
    if ($codeSigningEKU.Count -ne 1) {
        throw 'Signer certificate does not contain the Code Signing EKU.'
    }
    if ($null -eq $signed.TimeStamperCertificate) {
        throw 'RFC 3161 timestamp certificate evidence is missing.'
    }

    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $signtoolOutput = @(& $signtool.Source verify /pa /all /v $SignedExecutablePath 2>&1)
        $signtoolExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
    [IO.File]::WriteAllLines(
        $signtoolPath,
        [string[]]@($signtoolOutput | ForEach-Object { $_.ToString() }),
        [Text.UTF8Encoding]::new($false)
    )
    if ($signtoolExitCode -ne 0) {
        throw "signtool verify failed: exit=$signtoolExitCode"
    }

    $summary = [ordered]@{
        schemaVersion = 1
        status = 'PASS'
        verdict = 'PUBLIC_TRUST_AUTHENTICODE_PROOF_PASS'
        startedAt = $started.ToUniversalTime().ToString('o')
        finishedAt = (Get-Date).ToUniversalTime().ToString('o')
        unsigned = [ordered]@{
            path = (Resolve-Path $UnsignedExecutablePath).Path
            sha256 = (Get-FileHash $UnsignedExecutablePath -Algorithm SHA256).Hash
            authenticodeStatus = $unsigned.Status.ToString()
        }
        signed = [ordered]@{
            path = (Resolve-Path $SignedExecutablePath).Path
            sha256 = (Get-FileHash $SignedExecutablePath -Algorithm SHA256).Hash
            authenticodeStatus = $signed.Status.ToString()
            signer = (Get-CertificateEvidence -Certificate $signed.SignerCertificate)
            timestamp = (Get-CertificateEvidence -Certificate $signed.TimeStamperCertificate)
            signtoolExitCode = $signtoolExitCode
        }
        privateKeyExported = $false
    }
    Write-JsonFile $summaryPath $summary
}
catch {
    $failure = $_.Exception.ToString()
    Write-JsonFile $summaryPath ([ordered]@{
        schemaVersion = 1
        status = 'FAILED'
        verdict = 'FAILED'
        startedAt = $started.ToUniversalTime().ToString('o')
        finishedAt = (Get-Date).ToUniversalTime().ToString('o')
        failure = $failure
    })
}
finally {
    Stop-Transcript | Out-Null
}

Compress-Archive -Path (Join-Path $ArtifactRoot '*') -DestinationPath $zipPath -Force
$zipHash = (Get-FileHash $zipPath -Algorithm SHA256).Hash
Write-Output "AUTHENTICODE_PROOF_STATUS=$(if ($null -eq $failure) { 'PASS' } else { 'FAILED' })"
Write-Output "AUTHENTICODE_PROOF_ARTIFACT_ZIP=$zipPath"
Write-Output "AUTHENTICODE_PROOF_ARTIFACT_SHA256=$zipHash"
if ($null -ne $failure) { exit 1 }
