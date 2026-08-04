# Managed ReFS Library Pool probe

Status: experimental architecture probe, future v0.14.0 candidate, not a
released version.

## Version lineage

`v0.13.0` is the released Image + native CoW integration and remains historical
product evidence. This probe starts independently from the `v0.12.0` Native CoW
Storage Foundation at commit `7c6f6783708d632b96f8db0ed1c19ed61e4664b4`.
Successful probe code is to be ported deliberately onto the latest `main` for a
future v0.14.0 candidate. The probe branch itself is not a release branch.

The protected v0.13 reference is:

- branch: `codex/v0.13-image-cow-integration`
- commit: `c0280d3 refactor: use provider-native CoW baselines`

No tag, release, artifact, benchmark, or commit from that line is changed by
this probe.

## Architecture

The canonical payload is the ReFS-resident baseline itself:

```text
managed-library-pool.vhdx
└─ ReFS
   └─ testplay/
      ├─ pool.json
      ├─ baselines/<compatibility-key>/
      │  ├─ Library/
      │  ├─ metadata.json
      │  └─ COMPLETE
      ├─ workers/<lease>/Library/
      ├─ leases/
      └─ quarantine/
```

There is one dynamically expanding VHDX, one ReFS volume, one Library baseline
per compatibility key, and N worker Libraries. A worker is a directory tree of
file-range Block Clones on the same ReFS volume. No worker VHDX, parent/child
VHDX chain, Physical Directory Image, or silent whole-Library copy fallback is
part of this data path.

An external shadow workspace keeps `Assets`, `Packages`, and `ProjectSettings`
outside the pool. Its `Library` path is a verified directory junction to
`workers/<lease>/Library`.

## Standalone commands

Build on Windows amd64:

```powershell
go build -o testplay-refs-probe.exe ./cmd/testplay-refs-probe
```

Run from an elevated terminal:

```powershell
.\testplay-refs-probe.exe setup
.\testplay-refs-probe.exe status
.\testplay-refs-probe.exe probe
.\testplay-refs-probe.exe remove
```

The default host root is `%LOCALAPPDATA%\TestPlay\Storage`. `setup` performs:

1. Windows and elevation checks.
2. Dynamic VHDX creation through the existing v0.12 VirtDisk wrapper.
3. attach without a drive letter.
4. RAW disk identity and File Backed Virtual bus validation.
5. GPT initialization and maximum-size basic partition creation.
6. ReFS quick format with file integrity streams disabled by default.
7. an NTFS-hosted private directory mount.
8. filesystem, cluster size, volume GUID, and block-refcount capability checks.
9. a synthetic Block Clone plus allocate-on-write isolation check.
10. matching host and in-volume ownership metadata writes.
11. clean unmount, detach, visibility wait, and handle close.

The command leaves the pool detached between invocations. This avoids a
long-lived helper process while preserving an installation represented by one
VHDX file. `status` and `probe` attach temporarily and detach before returning.

`remove` mounts the exact owned VHDX, compares the host token, in-volume token,
VHDX file identity, volume GUID, filesystem, and cluster size, refuses active
baseline references or worker directories, detaches, rechecks the VHDX file
identity, and only then removes explicitly owned paths. An uncertain detach or
identity mismatch preserves data for operator inspection.

## Environment overrides

The Phase 1 executable consumes:

```text
TESTPLAY_REFS_POOL_FILE
TESTPLAY_REFS_MOUNT_ROOT
TESTPLAY_REFS_MAX_BYTES
```

The VHDX file and mount root must be absolute direct children of the same
storage root. This restriction makes later deletion bounded and independently
verifiable.

The complete Windows Unity hardware gate additionally reserves:

```text
TESTPLAY_REFS_PROJECT_PATH
TESTPLAY_REFS_UNITY_EDITOR_PATH
TESTPLAY_REFS_ARTIFACT_ROOT
```

Those Unity variables are not consumed by the Phase 1 executable. The
`LibraryBaselineStore` and worker primitives are implemented for controlled
Phase 2/3 fixtures; public `testplay run` integration remains out of scope.

## Baseline lifecycle

`LibraryBaselineStore` computes a compatibility key from:

- schema version;
- Unity version and SHA-256 of the Unity executable contents;
- `Packages/manifest.json` and optional `packages-lock.json`;
- the complete `ProjectSettings` tree;
- build target and scripting backend;
- canonical project identity; and
- the complete `Assets` tree for this correctness-first probe.

It measures `keyComputationMs`, `assetsHashMs`, `packagesHashMs`, and
`projectSettingsHashMs` separately.

`Ensure` takes a builder callback whose destination is already the staging
`Library` inside ReFS. The builder is expected to junction an isolated Unity
builder workspace to that destination and wait for Unity to exit. The store
does not accept a separate Image payload. After the builder exits it applies
read-only attributes and an ACL boundary, records a full tree digest, writes
metadata, writes `COMPLETE` last, re-verifies, and performs a same-volume atomic
rename to the final key directory.

Full integrity verification protects every worker acquire. Active-use markers
block clear and quarantine. Corruption prevents new workers and is moved by a
no-replace rename into `quarantine` before a replacement can be built.

## Worker lifecycle

Acquire is:

```text
acquire pool reservation lock
→ authoritatively remeasure ReFS used and host free space
→ validate orphan leases and active reservations
→ persist requested lease with O_EXCL
→ release reservation lock
→ verify baseline under its coordination lock
→ acquire baseline active-use marker with O_EXCL
→ persist cloning lease
→ Block Clone every regular file
→ verify baseline again
→ make worker writable
→ persist ready lease
→ create and verify Library junction
```

Release requires the caller to have observed complete Unity process exit:

```text
persist releasing
→ verify and remove the exact junction
→ verify worker ownership token
→ no-replace rename to quarantine
→ re-verify ownership
→ recursive deletion
→ remove active-use marker
→ persist released and remove lease record
```

Repeated `Release` calls are supported only in the same process through the
same `WorkerLease` object. Resuming a journal in a new process, forced
termination recovery, and reboot recovery are not implemented. Future design
names include `ResumeLeaseFromJournal`, `RecoverOrphanWorker`, and
`ReconcilePool`; a dead-process lease is currently reported as `orphan-found`.

## Block Clone policy

The Windows implementation walks the tree and rejects reparse points and
unsupported entry types. For each regular file it:

1. creates the destination and marks it sparse before sizing when required;
2. compares volume serials, filesystem names, and block-refcount flags;
3. queries sparse allocation with `FSCTL_QUERY_ALLOCATED_RANGES`, clips every
   page to query/file bounds, then sorts and merges overlap, adjacency, and
   duplicates while rejecting overflow and no-progress pagination;
4. compares source and destination integrity settings;
5. verifies the measured ReFS cluster size;
6. submits only allocated, cluster-aligned requests strictly smaller than 4 GiB;
7. leaves holes unallocated and copies only unaligned allocated fragments;
8. restores file attributes and creation/access/write timestamps; and
9. verifies destination logical size.

If no aligned bytes were cloned, if native Block Clone is unsupported, or if
physical bytes exceed measured unaligned fragments, the operation fails with
`refs-block-clone-unavailable` and `fallbackUsed: false`.

These constraints follow Microsoft's [ReFS Block Cloning documentation](https://learn.microsoft.com/en-us/windows-server/storage/refs/block-cloning),
[`FSCTL_DUPLICATE_EXTENTS_TO_FILE`](https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ni-winioctl-fsctl_duplicate_extents_to_file),
and [`GetVolumeInformationByHandleW`](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getvolumeinformationbyhandlew).

## Storage ceilings and metrics

The default is provisional until Windows hardware measurements exist:

```text
VHDX guest virtual-size ceiling: 16 GiB
testplay soft budget: 14 GiB
per-worker reservation / emergency reserve: 2 GiB
minimum host free-space floor: 30 GiB
experimental VHDX overhead reserve: 2 GiB
```

When the maximum is overridden, the default soft budget is the maximum minus
the reserve. An acquire exceeding the soft budget fails before cloning with
`storage-budget-exceeded`; it does not wait for a real disk-full event.

Metrics keep these layers separate:

- host filesystem: VHDX logical/allocated bytes and host free bytes;
- ReFS volume: total/free/used bytes at lifecycle points; and
- Library trees: logical/allocated bytes, cloned bytes, tails, metadata, and
  failures.

Deleting a worker can free ReFS space without shrinking the host VHDX file.
Automatic compact, resize, and compact-during-run are not implemented.

## Current validation status

Platform-independent lifecycle tests and Windows cross-compilation can be run
on non-Windows hosts. That does not execute VirtDisk, Storage PowerShell,
ReFS, `FSCTL_DUPLICATE_EXTENTS_TO_FILE`, junction, or Unity code.

Until the Windows hardware gate is run, the native verdict is `NOT MEASURED`.
No unexecuted native result may be recorded as PASS.

## Pre-native hardening

Worker capacity is reserved under the process-safe
`<pool>/leases/.reservation.lock`. While holding it, acquire remeasures ReFS
used bytes and host free space, validates every worker journal and orphan, sums
active reservations, and creates the new lease with `O_EXCL`. The lock honors
context cancellation, is released before baseline verification or cloning,
and is never deleted merely because it appears old. Caller-supplied current
volume usage is not accepted.

Setup requires the provisional 30 GiB host floor plus the full 16 GiB VHDX
maximum and 2 GiB overhead reserve. The VHDX maximum is a guest-volume
virtual-size ceiling; a separate testplay reservation policy protects the host
disk floor. Worker acquire requires the floor plus its full worker reserve and
fails closed on overflow or measurement failure. These values remain
experimental until native measurements exist.

`WorkerRequest` contains only the compatibility key, lease ID, and junction
path. `PoolPolicy` is built only after host metadata, in-volume metadata, and
the mounted ReFS identity agree on every identity, capability, and storage
field. Callers cannot override maximum, soft budget, reserve, host floor, or
cluster size.

Fresh setup supports a missing `%LOCALAPPDATA%\TestPlay\Storage` tree. It finds
and canonicalizes the nearest existing ancestor, rejects symlinks/reparse
points and files, creates each segment sequentially, then revalidates the final
canonical identity. Existing non-empty roots are rejected.

Baseline acquire, clear, and quarantine are serialized by
`leases/baseline-<digest>.coord`; mutation also records a marker. Active-use
checks and marker creation occur inside the same critical section, so baseline
rename and a new reference cannot both succeed. Protection metadata records a
path-sorted recursive descriptor digest over every directory and regular file,
including object type, read-only state, and inheritance state. Native Windows
inspection uses `GetNamedSecurityInfo`; a changed child/file ACL, enabled
inheritance, changed entry count, or writable file is corrupt even when content
is identical.

Worker release persists the `releasing`, junction removed, worker quarantined,
worker deleted, active-use released, and `released` milestones. A repeated
same-process call on the same object resumes safely; post-crash journal resume
is not implemented. Path absence is accepted only with prior ownership
evidence.

Sparse files are cloned from their allocated ranges. The destination is marked
sparse before sizing; only aligned allocated extents are block-cloned; holes
remain holes; and unaligned allocated fragments are physically copied and
measured. There is no query-failure or whole-file copy fallback.

Mounted cleanup uses a bounded 20-second context and joins primary and cleanup
errors. Structured failures report `cleanupState`,
`ownerMetadataCommitted`, `ownedVhdxPath`, and
`manualRecoveryRequired`; uncertain detach or ownership always preserves the
VHDX.

Each residual is `{ "measured": boolean, "count": n }`. Exact allowlists
separately count baseline creation/coordination/reservation/mutation artifacts,
baseline and worker staging directories, and unknown lease/baseline/worker
entries in addition to lifecycle, mount, junction, disk, and VHDX evidence.
The binary leaves `probeProcesses.measured=false`; only the outer PowerShell
harness completes that measurement. `attachedDisks` is measured only after
the bounded detach visibility check succeeds. Unmeasured is never treated as
zero. The native script records
`PROMISING` only after regular and sparse clone, allocate-on-write isolation,
forbidden-path checks, and measured-zero residuals all pass.

Pre/post clone verification latency, verified file count, and verified logical
bytes retain the cost of full hashes. A later latest-main design may evaluate a
generation token, USN journal, or validated cache without weakening this
correctness gate.

Current repository status is `STATIC READY FOR WINDOWS VALIDATION`. This is a
source/test readiness statement, not native evidence and not a release.
