# Windows copy-on-write workspace strategy

## Decision

```text
Differencing VHDX + On-demand Storage Helper
→ default Windows CoW candidate

Physical Copy
→ correctness-first fallback

ReFS Block Clone
→ experimental opt-in provider

WSL
→ Linux Agent Runtime only
```

Differencing VHDX is the preferred Windows-native CoW candidate because it
provides an explicit immutable Parent and independently writable Child files
through the documented `VirtDisk.dll` interface. It does not require the host
workspace to be on ReFS.

Physical Copy remains the conservative fallback. It must not be described as
copy-on-write because it duplicates the logical payload before mutation.

ReFS Block Clone remains an experimental opt-in for an explicitly approved
ReFS volume. Its evidence and implementation stay isolated from the VHDX
provider.

WSL is reserved for a Linux Agent Runtime. It is not the storage provider for
native Windows Unity workspaces.

## Current evidence

Elevated Windows hardware validation completed the standalone Differencing
VHDX probe once and then five times:

- Parent create, initialize, seed, and detach: 5/5
- Child A/B differencing creation: 5/5
- Parent isolation: 5/5
- sibling isolation: 5/5
- reattach persistence: 5/5
- cleanup: 5/5
- residual disks, mounts, and VHDX files: 0

The recorded file-length observations were 4 MiB initially, 36 MiB while
attached before mutation, and 19 MiB after mutation and detach. Because these
are `os.Stat().Size()` values from different lifecycle points, they do not
establish a monotonic growth curve or production capacity behavior. One run
also took 132.14 seconds while the others took approximately 15 seconds.
Neither observation affected correctness, but both require tighter performance
and storage metrics before production claims.

## Promotion gate

```text
Standalone Differencing VHDX correctness: PROVEN
On-demand Helper lifecycle: PROVEN
Small Unity fixture readiness: YES
Unity workspace readiness: NOT YET
Forced termination recovery: NOT YET
Production performance: NOT YET
```

`PROVEN` is limited to the standalone lifecycle, Parent and sibling isolation,
reattach persistence, and cleanup. It does not mean that a Unity production
backend exists.

## Current boundary

The standalone probe owns only:

- Parent and Child VHDX creation
- attach and detach
- verified virtual-disk identity
- integration-only NTFS bootstrap on the newly created file-backed disk
- directory mount points within a probe-owned operation root
- isolation and persistence assertions
- basic duration and file-length observations
- safe cleanup

It does not integrate with:

- `LibraryMaterializer` or the Image backend
- the public `testplay` CLI
- Unity or GNF_
- a Windows service or permanent daemon
- crash recovery or orphan adoption
- parallel workers or test sharding
- ReFS code

## On-demand helper boundary

The on-demand helper now implements the following boundary without becoming a
permanently running daemon:

```text
On-demand Storage Helper
|- versioned NDJSON Acquire/Release
|- one Workspace Lease per process
|- atomic Journal
|- same-process Idempotency
|- explicit Detach/Cleanup and EOF cleanup
`- administrator privilege boundary
```

The helper must make ownership and recovery decisions explicit and must retain
the probe's rule that only its newly created File Backed Virtual disks may be
initialized, mounted, detached, or deleted.

Its implementation status is:

```text
Protocol and fake-backend validation: COMPLETE
On-demand Helper Acquire/Release: PROVEN
EOF automatic cleanup: PROVEN
Lease lifetime management: PROVEN
Same-process idempotency: PROVEN
Cleanup safety: PROVEN
Small Unity fixture readiness: YES
Unity workspace readiness: NOT YET
Forced termination recovery: NOT YET
Production performance: NOT YET
```

These helper results are user-recorded elevated Windows hardware evidence; the
raw transcript and per-run metrics are not stored in the repository. They
permit a separate small Unity fixture validation PR, but do not establish Unity
Library compatibility, forced-termination recovery, or production latency. A
permanent broker service remains deferred.

## Small Unity fixture gate

The stacked `unity-vhdx-library-fixture` harness is implemented without
changing the public CLI or Image Backend. It prepares a warm Parent Library,
runs the same deterministic EditMode and PlayMode tests against a Physical
Library and a Helper-mounted Differencing Child, compares semantic NUnit
results, verifies the Parent hash and marker isolation, and checks cleanup.

```text
Fixture implementation: COMPLETE
Elevated Unity VHDX validation: NOT RUN
Verdict: IMPLEMENTED / AWAITING HARDWARE VALIDATION
```

Only elevated evidence with Physical/VHDX parity, stable mount identity,
unchanged Parent SHA-256, and zero residual resources can promote this gate to
`PROVEN — small Unity Library compatible with Differencing VHDX`.
