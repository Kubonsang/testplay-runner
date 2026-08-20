# Differencing VHDX quickstart (v0.13.0)

`vhdx-diff` is an experimental, explicit opt-in for Windows 11 x64. It keeps
one immutable NTFS parent VHDX per Unity compatibility key and mounts one
private differencing child at each isolated workspace's `Library` directory.
It does not format a physical partition or change ReFS, Dev Drive, Defender, or
registry settings.

## Requirements and verification

- Windows 11 x64 with an NTFS store volume;
- 20 GiB host free space by default;
- one elevated install for one Windows user SID;
- a matching `testplay` CLI and broker from the same release.

The stable Windows executables must be Authenticode-signed by the publisher
documented in the release validation record and carry an RFC 3161 timestamp.
Do not approve the elevated install until the extracted executable and archive
have been verified from the release directory:

```powershell
Get-AuthenticodeSignature .\testplay.exe | Format-List Status,SignerCertificate,TimeStamperCertificate
Get-FileHash .\testplay_0.13.0_windows_amd64.zip -Algorithm SHA256
```

Compare the result with `checksums.txt`, then verify the GitHub build
provenance:

```powershell
gh attestation verify `
  .\testplay_0.13.0_windows_amd64.zip `
  -R Kubonsang/testplay-runner
```

An attestation proves which repository, commit, and workflow produced an
archive. It does not by itself prove that the program is vulnerability-free.
A valid public-trust signature also does not guarantee immediate SmartScreen
reputation.

## One-time administrator setup

From an elevated PowerShell prompt, install the restricted local broker:

```powershell
testplay storage install
```

Use `--root C:\absolute\ntfs\path` only when a different store is required.
The service is installed as LocalSystem. Its named-pipe DACL admits only
LocalSystem, Administrators, and the registered user SID; the broker then
impersonates each connection and authorizes the exact registered SID before
dispatch. A different administrator or service token is not authorized merely
because the DACL allowed it to open the pipe. The client cannot choose a VHDX,
physical disk, partition, or arbitrary mount path.

Return to a normal, non-elevated terminal and confirm that the broker is ready:

```powershell
testplay storage status --json
```

Do not continue when the status reports `manualRecoveryRequired: true`.

## AI-agent use

Use explicit `vhdx-diff` when failure must be visible and fallback is not
allowed:

```powershell
testplay run --workspace-backend vhdx-diff
```

An agent may explicitly request `auto` instead. `auto` can fall back to
`legacy` only if broker connection or capacity admission fails before parent or
child creation starts. Neither backend is selected automatically when the flag
and config are absent.

Equivalent schema-1 configuration:

```json
{
  "workspace": {
    "backend": "vhdx-diff",
    "store_max_allocated_bytes": 34359738368,
    "minimum_host_free_bytes": 21474836480
  }
}
```

The default allocated-byte quota is 32 GiB and every new active child reserves
2 GiB. Inspect capacity before a large run:

```powershell
testplay storage status --json
testplay storage gc --dry-run
```

Only expired, inactive, unprotected parents are eligible for GC. Active,
pending, retained, and quarantined objects are never silently deleted.

## Retention, upgrade, and cleanup

Keep a completed workspace only when it is needed for debugging:

```powershell
testplay run --workspace-backend vhdx-diff --keep-workspace
testplay workspace attach <run-id>
testplay workspace remove <run-id>
```

Upgrade the installed service from an elevated prompt after replacing the CLI
with the matching release:

```powershell
testplay storage upgrade
```

Normal uninstall removes only identities owned by the installation:

```powershell
testplay storage uninstall
```

To remove the service but retain exact store data for a later reinstall:

```powershell
testplay storage uninstall --preserve-data
```

Before downgrading to v0.12, run one of those uninstall commands with the
v0.13 binary. v0.12 does not manage the versioned broker lifecycle.

If a command reports `cleanupState: preserved`, keep the authoritative store
and inspect status. If it reports `cleanupState: uncertain` or
`manualRecoveryRequired: true`, do not use `Remove-Item`, Disk Management, or
`Dismount-DiskImage` against the residual. Preserve the output and report it
through the repository's private vulnerability or support channel.

## Stable experimental boundaries

Native Windows gates passed for fixture and GNF_ 1/2/4 workers, forced client
and Unity termination, broker restart, Windows reboot, quota/LRU, and retained
workspaces. The provider remains experimental because the separate 10-second
per-worker preparation objective was not met. GNF_ 8 workers, long-running
child growth, generalized performance superiority, and production readiness
are not claimed.
