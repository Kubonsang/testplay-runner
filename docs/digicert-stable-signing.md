# Optional DigiCert Authenticode hardening

This document preserves a future public-trust Authenticode path. Authenticode
is not a `v0.13.0` release gate; that release is intentionally unsigned and
uses SHA-256, GitHub provenance, CI, and explicit disclosure.

## Acquisition gate

If future hardening is funded, obtain a public-trust Authenticode identity that DigiCert accepts for a South
Korean individual developer. The certificate must use a non-exportable
KeyLocker/Software Trust key, include the Code Signing EKU, and support RFC 3161
timestamping. If DigiCert rejects that identity or cannot provide these
properties, do not enable optional signing or claim signed assets.

Use `scripts/collect-authenticode-signing-proof.ps1` with an unsigned and signed
throwaway executable before configuring GitHub. Commit only the resulting
redacted validation record and artifact SHA-256, never credentials or the
client-auth certificate.

## Protected GitHub environment

Create an environment named `stable-signing`, require a maintainer approval,
and restrict deployment branches to `codex/release-v0.13.0` and protected tag
`v0.13.0`. Configure these environment values:

| Kind | Name | Value |
|---|---|---|
| variable | `SM_HOST` | DigiCert Software Trust endpoint |
| variable | `SM_KEYPAIR_ALIAS` | production keypair alias |
| variable | `SM_EXPECTED_SIGNER_SUBJECT` | exact X.509 subject |
| variable | `SM_EXPECTED_SIGNER_THUMBPRINT` | expected certificate thumbprint |
| secret | `SM_API_KEY` | service-user API key |
| secret | `SM_CLIENT_CERT_FILE_B64` | base64 DigiCert client-auth `.p12` |
| secret | `SM_CLIENT_CERT_PASSWORD` | client-auth `.p12` password |

The `.p12` authenticates the service user; it must not contain an exportable
copy of the public-trust code-signing private key. Rotate it according to the
DigiCert account policy and revoke it after suspected disclosure.

## Future candidate execution

Add a separately reviewed, manually dispatched hardening workflow for a future
release. It must stop for `stable-signing` environment approval, sign both
Windows executables, rebuild the two Windows archives, regenerate all nine
checksums, verify signatures, and attest the signed archives. It must not run
for the published unsigned v0.13.0 tag or replace its assets.

After the candidate succeeds, download both Windows ZIPs and `checksums.txt`.
Run the stable native harness from an elevated Windows PowerShell:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\run-vhdx-diff-release-candidate.ps1 `
  -UnityEditorPath 'C:\Program Files\Unity\Hub\Editor\6000.3.8f1\Editor\Unity.exe' `
  -ReleaseArchivePath '<testplay Windows ZIP>' `
  -HelperArchivePath '<helper Windows ZIP>' `
  -ChecksumsPath '<checksums.txt>' `
  -ExpectedVersion '<future-version-SNAPSHOT-commit>' `
  -SignaturePolicy TrustedStable `
  -ExpectedSignerSubject '<exact subject>' `
  -ExpectedSignerThumbprint '<exact thumbprint>' `
  -InstallApproved
```

Record the candidate run and native artifact ZIP SHA-256 in the future release
validation record. Optional signing must use a later release version and must
never replace or mutate published v0.13.0 assets.
