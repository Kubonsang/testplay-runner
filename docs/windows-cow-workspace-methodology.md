# Windows copy-on-write workspace methodology

## Evidence classes

Use these labels consistently:

- **Hardware evidence**: output from official storage APIs on an actual Windows
  host.
- **User-recorded hardware evidence**: hardware output supplied from an
  interactive run when no repository raw transcript artifact was preserved.
- **CI evidence**: build, unit-test, parser, and static-analysis results.
- **Mock or cross-build evidence**: interface and compilation confidence only.

GitHub-hosted CI is not a substitute for an elevated local VHDX lifecycle run.
Do not promote a provider based only on compilation or mocks.

## Correctness protocol

A Differencing VHDX hardware run must establish all of the following:

1. Create, attach, initialize, seed, and detach a new Parent.
2. Create Child A and Child B from that Parent.
3. Mutate Child A and prove the complete Parent VHDX hash is unchanged.
4. Prove Child B retains the baseline and cannot see Child A markers.
5. Mutate Child B and prove Child A cannot see the Child B marker.
6. Detach and reattach Child A and verify its exact mutated payload.
7. Reattach the Parent read-only and verify its baseline.
8. Unmount, detach, close, and remove every probe-owned artifact.
9. Compare File Backed Virtual disks and probe mounts before and after.

A five-run `PROVEN` verdict requires every isolation, persistence, and cleanup
assertion to pass 5/5 with zero residual probe disks, mounts, or VHDX files.
Any failure prevents that verdict.

## Storage measurements

Name virtual capacity, logical file length, and allocated storage separately:

- `GetVirtualDiskInformation(...).VirtualSize` is virtual capacity.
- `os.Stat(path).Size()` is logical file length at that instant.
- neither value is filesystem allocated bytes or compressed/sparse physical
  consumption.

Record the lifecycle point for every measurement. A value collected while a
VHDX is attached is not directly comparable to a detached-file value without
that qualification.

The current hardware evidence observed Child A at 36 MiB while attached before
mutation and 19 MiB after mutation and detach. This is not a monotonic growth
curve. It is also not, by itself, a correctness failure because isolation,
payload parity, reattach persistence, and cleanup passed. VHDX metadata
finalization, sparse behavior, and timing are possible explanations only.
Production storage claims require a fixed lifecycle point and an explicit
allocated-byte metric.

## Timing measurements

Wall-clock duration covers everything the user waits for. It is not equivalent
to the sum of individually instrumented functions.

Keep outliers in the evidence. For the current probe, disclose the 132.14-second
run alongside the approximately 15-second runs. No correctness or cleanup
failure occurred, and the source of the delay is not established.

Future instrumentation should include:

```text
totalWallClockMs
parentLifecycleWallClockMs
childALifecycleWallClockMs
childBLifecycleWallClockMs
pnpDiscoveryWaitMs
volumeReadyWaitMs
mountVisibilityWaitMs
detachVisibilityWaitMs
powershellBootstrapMs
```

PnP, volume readiness, mount visibility, and detach visibility remain
hypotheses until those measurements exist.

## Provider decision

```text
Differencing VHDX + On-demand Storage Helper
→ default Windows CoW candidate

Physical Copy
→ safe fallback

ReFS Block Clone
→ experimental opt-in

WSL
→ Linux Agent Runtime only
```

The standalone VHDX lifecycle is proven, so the on-demand helper protocol is
ready to design. Unity compatibility, path sensitivity, GNF_ behavior,
concurrent workers, crash recovery, and production performance remain separate
gates.
