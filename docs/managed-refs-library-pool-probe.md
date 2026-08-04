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
verify baseline
→ reject orphan leases
→ check current ReFS used + active reservations + new reservation
→ acquire baseline active-use marker
→ persist requested/cloning lease
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

Forced termination and reboot recovery are not completion claims of this
probe. A dead-process lease is reported as `orphan-found`; it is not silently
reused.

## Block Clone policy

The Windows implementation walks the tree and rejects reparse points and
unsupported entry types. For each regular file it:

1. creates and pre-sizes the destination;
2. compares volume serials, filesystem names, and block-refcount flags;
3. mirrors sparse state;
4. compares source and destination integrity settings;
5. verifies the measured ReFS cluster size;
6. submits cluster-aligned requests strictly smaller than 4 GiB;
7. physically writes only the measured unaligned tail;
8. restores file attributes and creation/access/write timestamps; and
9. verifies destination logical size.

If no aligned bytes were cloned, if native Block Clone is unsupported, or if
physical bytes exceed measured tails, the operation fails with
`refs-block-clone-unavailable` and `fallbackUsed: false`.

These constraints follow Microsoft's [ReFS Block Cloning documentation](https://learn.microsoft.com/en-us/windows-server/storage/refs/block-cloning),
[`FSCTL_DUPLICATE_EXTENTS_TO_FILE`](https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ni-winioctl-fsctl_duplicate_extents_to_file),
and [`GetVolumeInformationByHandleW`](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getvolumeinformationbyhandlew).

## Storage ceilings and metrics

The default is provisional until Windows hardware measurements exist:

```text
VHDX hard ceiling: 16 GiB
testplay soft budget: 14 GiB
per-worker reservation / emergency reserve: 2 GiB
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
