# Differencing VHDX workspace probe

## Status

```text
PROVEN — standalone VHDX lifecycle and isolation
```

This verdict is limited to the standalone Windows Differencing VHDX probe. It
does not prove Unity Library compatibility, production latency, crash
recovery, concurrent workers, or daemon/service readiness.

## Scope

The probe creates a dynamic Parent VHDX and two independent differencing
children, then verifies:

- Parent creation, initialization, seed, and detach
- Child A/B creation from the same immutable Parent
- Parent isolation
- sibling isolation
- detach/reattach persistence
- cleanup and absence of residual disks, mounts, and VHDX files

The implementation remains research code under `internal/vhdxprobe`. It is not
connected to the public `testplay` CLI, `LibraryMaterializer`, the Image
backend, Unity, or a long-running service.

## Hardware evidence

The evidence was recorded from an elevated PowerShell session on the actual
Windows host. The one-run probe and the five-run repetition both passed. The
console output was supplied as user-recorded hardware evidence; a repository
raw transcript artifact was not preserved. GitHub CI, cross-builds, and mock
tests are not treated as hardware evidence.

| Environment | Value |
|---|---|
| Windows edition | Windows 10 Pro |
| Display version | 25H2 |
| OS build | 26200.8875 |
| Architecture | amd64 |
| Go | go1.26.2 windows/amd64 |
| Shell | elevated PowerShell |
| Probe root | `C:\Dev\testplay-vhdx-probe` |
| Virtual disk API | `VirtDisk.dll` |
| Parent virtual size | 512 MiB |
| Logical payload | 64 MiB |
| Validation | one run, then five repetitions |

## Correctness

| Check | Result |
|---|---:|
| Parent create/initialize/seed/detach | 5/5 PASS |
| Child A/B differencing creation | 5/5 PASS |
| Parent isolation | 5/5 PASS |
| Sibling isolation | 5/5 PASS |
| Reattach persistence | 5/5 PASS |
| Parent VHDX SHA-256 unchanged | 5/5 PASS |
| Deterministic payload hash unchanged | 5/5 PASS |
| Cleanup | 5/5 PASS |
| Residual File Backed Virtual disks | 0 |
| Residual mount points | 0 |
| Residual probe artifacts/VHDX files | 0 |

The preceding one-run validation also passed all isolation, persistence, and
cleanup assertions. Its complete Parent VHDX SHA-256 was identical before and
after the Child operations.

## API and safety boundary

The core lifecycle calls these documented `VirtDisk.dll` operations:

- `CreateVirtualDisk`
- `OpenVirtualDisk`
- `AttachVirtualDisk`
- `DetachVirtualDisk`
- `GetVirtualDiskPhysicalPath`
- `GetVirtualDiskInformation`

The integration-only filesystem bootstrap accepts only a physical path
returned by the handle in the exact form `\\.\PhysicalDriveN`. Before
initialization or formatting, the single matching `Get-Disk -Number N` result
must report `File Backed Virtual`, must be RAW, and must contain no partitions.
The disk number is never guessed or supplied by a caller. Any missing,
ambiguous, non-file-backed, non-RAW, or already-partitioned candidate stops the
probe.

`AttachVirtualDisk` uses `ATTACH_VIRTUAL_DISK_FLAG_NO_DRIVE_LETTER`. A
probe-owned empty directory is used as the partition access path, so no global
drive letter is selected. Existing physical disks, partitions, and volumes are
outside the mutation boundary.

## File-size metrics

The reported byte fields do not all represent the same kind of size:

| Metric | Code definition and measurement point | Observed |
|---|---|---:|
| Parent virtual | `GetVirtualDiskInformation(...).VirtualSize` | 512 MiB |
| Parent file | `os.Stat(parent).Size()` after Parent detach and hash | 164 MiB |
| Child initial | `os.Stat(childA).Size()` after creation and Parent-link verification, before attach | 4 MiB |
| Child after attach | `os.Stat(childA).Size()` while Child A is attached and mounted, before mutation | 36 MiB |
| Child after mutation | `os.Stat(childA).Size()` after mutation, unmount, and detach | 19 MiB |
| Child after reattach | `os.Stat(childA).Size()` after persistence verification, unmount, and detach | 19 MiB |
| Logical payload | deterministic payload length | 64 MiB |

`os.Stat().Size()` is the logical file length (EOF). It is not allocated
filesystem bytes, compressed size, sparse physical allocation, or VHDX virtual
capacity.

All five repetitions reported the same sizes. In particular, Child A was 36
MiB while attached before mutation and 19 MiB after mutation and detach. This
is not a monotonic growth curve and must not be presented as one. The decrease
does not by itself show a CoW correctness failure: Parent and sibling
isolation, the mutated payload hash, reattach persistence, and cleanup all
passed. VHDX metadata finalization, sparse allocation behavior, and the
attach/detach measurement point are possible explanations only; the current
evidence does not establish a cause.

Production storage claims require one fixed lifecycle measurement point plus a
defined allocated-byte metric. The useful confirmed observation is narrower:
the child started as a 4 MiB logical file and ended as a persistent 19 MiB
logical file after the 64 MiB logical-payload mutation.

## Phase timing

The following averages are the recorded five-run values; they have not been
recalculated or adjusted:

| Phase | Average |
|---|---:|
| Parent create | 5.8 ms |
| Parent attach | 11.6 ms |
| Parent initialize | 3,725.0 ms |
| Parent seed | 124.2 ms |
| Parent detach | 946.4 ms |
| Child create | 38.0 ms |
| Child attach | 929.6 ms |
| Mount resolve | 914.8 ms |
| Mutation | 41.6 ms |
| Child detach | 947.2 ms |
| Cleanup | 9.4 ms |

One complete run took 132.14 seconds; the other runs were approximately 15
seconds. No correctness or cleanup failure accompanied the outlier. The
recorded phase durations do not explain the complete wall-clock outlier.
Unmeasured PnP, volume readiness, mount visibility, or detach visibility waits
are hypotheses, not conclusions.

Before production performance claims, add explicit measurements for:

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

## Cleanup evidence

The final comparison reported:

```text
beforeVirtualDisks:       0
afterVirtualDisks:        0
virtualDiskDifference:    0
residualProbeMounts:      0
residualArtifacts:        0
residualVHDXFiles:        0
cleanupPassed:            true
```

Cleanup unmounts the partition before detaching each virtual disk and closes
handles before removing probe-owned files. If unmount or detach fails,
automatic directory deletion stops so that the failure remains diagnosable.
The probe does not detach pre-existing virtual disks or delete unrelated
files.

## Validation commands

General validation:

```powershell
go test ./...
go vet ./...
git diff --check
```

Elevated hardware validation:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\run-vhdx-probe.ps1

powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\run-vhdx-probe.ps1 `
  -Count 5
```

The script permits only the dedicated root
`C:\Dev\testplay-vhdx-probe`, records before/after File Backed Virtual disks,
and fails if the root is not empty before a run.

## Remaining gates

The standalone lifecycle and isolation result is proven. Separate work remains
for:

- on-demand helper protocol and privilege boundary
- journal, lease ownership, and idempotency
- crash and orphan recovery
- a small Unity fixture and path-sensitivity checks
- GNF_ benchmark
- concurrent workers and sharding
- production storage and latency measurements
