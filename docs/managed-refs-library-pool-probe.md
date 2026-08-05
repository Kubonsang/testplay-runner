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

There is one persistent dynamically expanding VHDX, one Dev Drive/ReFS volume, one Library baseline
per compatibility key, and N worker Libraries. A worker is a directory tree of
file-range Block Clones on the same ReFS volume. No worker VHDX, parent/child
VHDX chain, Physical Directory Image, or silent whole-Library copy fallback is
part of this data path.

The installation remains one ordinary `.vhdx` file on the NTFS host while
detached. When attached, Windows exposes that same file as an exact virtual
disk with a GPT partition. Detach is not deletion.

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
2. read-only Dev Drive capability checks (Windows build, elevation,
   `Format-Volume -DevDrive`, and `fsutil devdrv query`).
3. Dynamic VHDX creation through the existing v0.12 VirtDisk wrapper.
4. attach without a permanent drive letter.
5. RAW disk identity and File Backed Virtual bus validation.
6. GPT initialization and maximum-size basic partition creation.
7. assignment of an unused temporary drive letter and Dev Drive formatting via
   `Format-Volume -DriveLetter <letter> -DevDrive`.
8. ReFS and `fsutil devdrv query` verification, with raw query output retained
   as an artifact.
9. an NTFS-hosted private directory mount followed by removal and verification
   of the temporary drive letter.
10. filesystem, cluster size, volume GUID, and block-refcount capability checks.
11. a synthetic Block Clone plus allocate-on-write isolation check.
12. matching host and in-volume ownership metadata writes.
13. clean unmount, detach, visibility wait, and handle close.

The command leaves the persistent pool detached between invocations. This avoids a
long-lived helper process while preserving an installation represented by one
VHDX file. `status` and `probe` attach temporarily and detach before returning.
They inspect the existing Dev Drive and never format it again. Only `remove`
deletes the owned VHDX and metadata; ordinary unmount/detach is not deletion.

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
→ create and verify an exact owned worker staging root
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
VHDX guest virtual-size ceiling: 64 GiB
testplay soft budget: 14 GiB
per-worker reservation / emergency reserve: 2 GiB
minimum host free-space floor: 30 GiB
experimental VHDX overhead reserve: 2 GiB
```

The 14 GiB default soft budget is independent of the VHDX maximum. An acquire
exceeding the soft budget fails before cloning with
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

Native Phase 1 is now `PROMISING` on Windows 11 Pro build 26200. It proves the
standalone persistent Dev Drive lifecycle and Block Clone probe described
below; it does not measure Unity, canonical baseline ACL correctness, the
worker ladder, or release readiness.

## Pre-native hardening

Worker capacity is reserved under the process-safe
`<pool>/leases/.reservation.lock`. While holding it, acquire remeasures ReFS
used bytes and host free space, validates every worker journal and orphan, sums
active reservations, and creates the new lease with `O_EXCL`. The lock honors
context cancellation, is released before baseline verification or cloning,
and is never deleted merely because it appears old. Caller-supplied current
volume usage is not accepted.

Setup requires the provisional 30 GiB host floor plus the full 64 GiB VHDX
maximum and 2 GiB overhead reserve: 96 GiB free with defaults. VHDX sizes below
50 GiB are rejected because Dev Drive formatting requires a suitably sized
volume. The VHDX maximum is a guest-volume
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

Setup is a host/in-volume transaction. It creates
`pool-owner.pending.json` with `O_EXCL`, creates and identifies the exact VHDX,
writes the matching in-volume `pool.json` with a flushed temporary file and
atomic rename, reads it and the required layout back, flushes the exact mounted
volume, then detaches and reattaches the same VHDX. Only after the reattached
volume GUID, ReFS capability, Dev Drive query, ownership token, metadata,
layout, and VHDX file identity pass does setup detach again and atomically
commit `pool-owner.json`. `status`, `probe`, workers, and normal `remove` do not
accept a pending owner. A separately named `recover-incomplete-setup` command
can delete an incomplete pool only after exact ownership and a strict empty
layout gate; it never repairs metadata or falls back to a copy provider.

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

The prior generic `Format-Volume -FileSystem ReFS` attempt on Windows 11 Pro
25H2 build 26200.8875 is `UNSUPPORTED`; it failed with
`refs-format-unavailable`. Cleanup state was `released` and
the disk and left no owned VHDX or mount. That result is retained as evidence
for the rejected generic provider; it is not evidence against the Dev Drive
provider.

The first Dev Drive provider run at commit
`6fd8074f36a38c064b6435c33dee3f3b60e4ba93` failed after mount at
`canonical-clone-source`: `filepath.EvalSymlinks` returned `too many links`
under the Windows volume mount. Cleanup was `released`; regular and sparse
Block Clone IOCTLs were not executed. Its artifact ZIP SHA-256 is
`047150F95E9B2FA772947D10E891C12B5BD236C86C488C8A9A3B10A55C988BC8`.

A follow-up run with trusted-root clone validation completed Dev Drive setup,
regular/sparse Block Clone, and CoW isolation, then failed at the next detached
`probe` with `pool-not-found`. Inspection already attached the VHDX and
registered the directory access path before reading metadata; the corrected
cause is that access-path visibility did not guarantee immediate mounted
filesystem content readiness. The persistent VHDX was preserved and no disk,
temporary drive letter, or probe process remained attached. Follow-up artifact
ZIP SHA-256:
`96FC061E9D8694D2FE25D5DBEDD5C50C6CA497235FC10A17DC03998196642554`.

The next implementation added a bounded 20-second mounted-content readiness
boundary, structural `pool-mount-not-ready` diagnostics, persistent-operation
`preserved` cleanup evidence, IOCTL-attempt aggregation, staged summary
retention, and explicit UTF-8 PowerShell output. Its ownership-safe status of
the retained pool verified the private mount, ReFS, Dev Drive query, 4096-byte
clusters, and Block Clone capability, but `pool.json` remained absent for the
entire readiness window. The status failed with operation
`wait-mounted-pool-metadata`; cleanup was `preserved`, owner metadata and VHDX
were retained, and attached disks, temporary drive letters, and probe processes
were zero. Per the ownership gate, remove and a fresh Phase 1 were not run.
Artifact ZIP SHA-256:
`500273C1A9B50C1589B24BA453ECF89037D6BA2ABD59F9D2EE4AA7B21FD71714`.

Read-only forensics of that retained VHDX classified it as
`C_LAYOUT_EXISTS_POOL_METADATA_MISSING`. The VHDX file identity
`a00b8212:00280000003f9ecf`, expected/actual volume GUID
`\\?\Volume{d0c73e68-afdf-4872-84c2-a6e9db9e9b48}\`, ReFS, 4096-byte
clusters, Dev Drive query, and Block Clone capability all matched. The required
layout and the prior synthetic probe files were present, while `pool.json` was
absent at every candidate path. This confirms that detach lost a tail of ReFS
namespace changes (probe deletion and pool metadata creation), not that a
different volume or partition was selected. Forensic ZIP SHA-256:
`1A87B7F3A7792D823130B316BE04776970035635526D9C8C94BB34540DEE560E`.

The explicit, ownership-gated incomplete-setup recovery removed that retained
VHDX and owner metadata and measured storage root, mount, attached disk,
temporary drive letter, and probe-process residuals at zero. Recovery ZIP
SHA-256:
`BBEA3BA201A1A3B2FC680991DBDC0022F7749E5AD7DE46AE306FCD0FA49D0A5B`.

A fresh transactional native Phase 1 on Windows 11 Pro build 26200 is
`PROMISING`. Initial Dev Drive format, ReFS, 4096-byte clusters, volume flush,
regular/sparse Block Clone, CoW isolation, internal detach/reattach durability
verification, post-proof owner commit, external probe/status reattach, and
explicit remove all passed without fallback. Final VHDX, owner, pending owner,
mount, storage root, attached disk, temporary drive letter, and probe process
residuals were zero. Artifact ZIP SHA-256:
`AF020C740B80FBB6472A3C5EC416E6DF69EA73DD8BDEDCDCBEB8DBE000E0CF36`.
Unity correctness, canonical baseline ACL correctness, 1/2/4/8 workers, and
release readiness remain `NOT MEASURED`; `PROMISING` is not `PROVEN`.

The first Unity Phase 2A single-worker run at commit
`5838a2cba06f0138e4092fa1962a94ebeb0832ce` built and protected a canonical
Unity 6000.3.8f1 Library baseline and passed reference EditMode 2/2 and
PlayMode 1/1 with no compile errors. The first worker acquire then failed at
`validate-clone-destination-parent`: `WorkerManager` had calculated, but had
not created, its exact staging root before passing `staging\Library` to the
strict clone path validator. Worker Block Clone and worker Unity were not
executed. Cleanup was `released`; the VHDX, owner records, mount, storage root,
related attached disk, temporary drive letter, and owned processes had zero
residuals. Artifact ZIP SHA-256:
`242F3365EFA1BF1B359D5D913640ECC1045649EFD9D4D0BE57DAA36D69DD304A`.
