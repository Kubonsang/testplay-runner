# Security policy

## Supported versions

Security fixes are provided for the latest published release candidate and the
latest stable release. Older prereleases and development branches are not
supported. `vhdx-diff` in v0.13.0 is an experimental Windows 11 x64
opt-in; the legacy backend remains the default.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub's private
vulnerability reporting flow on the repository Security tab. Include the
affected version, Windows build, command, redacted logs, and whether any
service, mount, VHDX, or process remained. Do not attach secrets or proprietary
Unity project contents.

If private reporting is unavailable, open a public issue containing only a
request for a private maintainer contact. Do not include exploit details in
that issue.

## Privileged storage boundary

`testplay storage install` copies the matching CLI into an ACL-protected store
and registers `TestPlayStorageBroker` as a LocalSystem Windows service. The
broker is intentionally restricted to:

- the installed store and workspace roots;
- a named-pipe DACL limited to LocalSystem, Administrators, and the registered
  user SID, followed by application authorization of the exact registered SID;
- VHDX files and mount points whose ownership and file identities it recorded;
- versioned named-pipe requests whose connected client token is verified.

Clients provide compatibility keys, run IDs, and workspace IDs, not disk,
partition, VHDX, or arbitrary mount paths. The provider does not modify a
physical partition, ReFS/Dev Drive configuration, Defender, the registry, or
system-wide trust settings.

Cleanup fails closed. Unknown paths, identity mismatches, live or retained
leases, and detach uncertainty are preserved or quarantined rather than
deleted. When output reports `manualRecoveryRequired: true` or
`cleanupState: uncertain`, do not manually delete the VHDX, mount, receipt, or
store. Preserve evidence and report the state privately.

## Release integrity

The v0.13.0 Windows executables are not Authenticode-signed and may trigger
SmartScreen. Verify the published SHA-256 and GitHub build-provenance
attestation before approving the elevated installation. Provenance binds an
archive to a repository workflow and commit; it does not claim that the binary
is vulnerability-free. Public-trust Authenticode remains optional future
hardening.
