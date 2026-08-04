# Managed ReFS Library Pool benchmark record

Status: `NOT MEASURED` on Windows native hardware.

This file is an evidence template, not a performance claim. The implementation
was built from `v0.12.0` as an independent architecture probe. It does not
change or invalidate the released v0.13 Image + native CoW benchmark history.

## Environment

| Field | Result |
|---|---|
| Windows edition/build | NOT MEASURED |
| Windows provider | `dev-drive-vhdx` (static configuration) |
| volume kind | `Dev Drive` (native verification NOT MEASURED) |
| Dev Drive format/query evidence | NOT MEASURED |
| Unity Editor | NOT MEASURED |
| Project identity | NOT MEASURED |
| VHDX maximum | NOT MEASURED |
| soft budget / reserve | NOT MEASURED |
| minimum host free / VHDX overhead reserve | NOT MEASURED |
| ReFS cluster size | NOT MEASURED |
| host filesystem | NOT MEASURED |

The VHDX maximum is the guest volume's virtual-size ceiling. Before setup,
testplay's separate host reservation gate requires minimum host free space plus
the full VHDX maximum plus overhead; each worker gate includes its reserve.
The defaults are 64 GiB maximum, 14 GiB independent soft budget, 2 GiB worker
reserve, 30 GiB host floor, and 2 GiB overhead, so setup requires 96 GiB free.
Maximums below 50 GiB are rejected.

## Prior generic-provider evidence

The earlier generic ReFS formatter is not a fallback and is no longer used.
Its retained native result was Windows 11 Pro 25H2 build 26200.8875,
`refs-format-unavailable`, cleanup released, with the VHDX and mount absent.
This is evidence about the rejected generic provider only. The Dev Drive VHDX
provider has not yet been run on native hardware.

## Retained Dev Drive path-validation failure

The first native Dev Drive run at commit
`6fd8074f36a38c064b6435c33dee3f3b60e4ba93` on Windows 11 Pro build
26200.8875 reached the mounted pool and then failed with `clone-failed` at
`canonical-clone-source`. `filepath.EvalSymlinks` returned `too many links`
beneath the Windows directory volume mount. Cleanup was `released`; regular
and sparse Block Clone IOCTLs were `NOT EXECUTED`. The artifact ZIP SHA-256 is
`047150F95E9B2FA772947D10E891C12B5BD236C86C488C8A9A3B10A55C988BC8`.
This evidence is retained and is not overwritten by follow-up runs.

The path-validation follow-up on the same Windows build passed Dev Drive setup,
ReFS/block-refcount capability validation, regular and sparse Block Clone, and
allocate-on-write source isolation. The subsequent detached `probe` failed
before attach with `pool-not-found` while reading the in-volume `pool.json`
through the empty host mount directory. Because `remove` was not reached, the
persistent VHDX was preserved; attached disks, new drive letters, and probe
processes measured zero. The follow-up artifact ZIP SHA-256 is
`96FC061E9D8694D2FE25D5DBEDD5C50C6CA497235FC10A17DC03998196642554`.

Required variables:

```text
TESTPLAY_REFS_PROJECT_PATH
TESTPLAY_REFS_UNITY_EDITOR_PATH
TESTPLAY_REFS_POOL_FILE
TESTPLAY_REFS_MOUNT_ROOT
TESTPLAY_REFS_ARTIFACT_ROOT
TESTPLAY_REFS_MAX_BYTES
```

## Static evidence boundaries

Worker storage limits come only from a `PoolPolicy` verified against both
metadata copies and the mounted volume; `WorkerRequest` cannot override them.
Fresh setup creates missing path segments sequentially from a canonical
existing ancestor and rejects intermediate symlinks/reparse points. Recursive
baseline protection evidence covers child directories and files. Sparse range
pages are clipped, sorted, merged, and checked for overflow/no progress.
Residual classification uses exact allowlists and reports staging and unknown
artifacts; the binary leaves process count unmeasured for the outer harness.
Release retry is supported only in the same process on the same object;
new-process journal resume, forced termination, and reboot recovery are not
implemented.

## Correctness gate

| Check | Legacy | Managed ReFS probe |
|---|---:|---:|
| exit code | NOT MEASURED | NOT MEASURED |
| total/passed/failed/skipped | NOT MEASURED | NOT MEASURED |
| test name/result pairs | NOT MEASURED | NOT MEASURED |
| compile errors | NOT MEASURED | NOT MEASURED |
| source hash unchanged | NOT MEASURED | NOT MEASURED |
| baseline hash unchanged | n/a | NOT MEASURED |
| worker A/B isolation | n/a | NOT MEASURED |
| cleanup residual zero | NOT MEASURED | NOT MEASURED |

## Parallel ladder

Run in order and stop at the first correctness or cleanup failure.

| Workers | Clone batch wall | Worker ready wall | Unity ready wall | Baseline allocated | Worker logical | ReFS used delta | Host VHDX allocated delta | Cleanup residual |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| 2 | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| 4 | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| 8 | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |

Calculations:

```text
logical amplification
= all worker Library logical bytes / baseline Library logical bytes

physical amplification
= ReFS used-byte increase / baseline Library allocated bytes
```

Expected behavior is not zero physical growth. Directory and file records,
reference-count metadata, alignment tails, and Unity modifications consume
space. The experiment succeeds only if measured growth follows metadata and
actual changed data rather than full baseline duplication.

## Three-layer storage record

### Host filesystem

| Metric | Before | After baseline | After acquire | After Unity | After release | After explicit compact |
|---|---:|---:|---:|---:|---:|---:|
| VHDX logical bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| VHDX allocated bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| host free bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |

Automatic compact is not part of the probe. Any future explicit compact result
must be recorded separately and only while no worker is active.

### ReFS volume

| Metric | Before | After baseline | After acquire | After Unity | After release |
|---|---:|---:|---:|---:|---:|
| used bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| free bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |

### Library trees

| Metric | Baseline | Worker ready | Worker after Unity | Worker released |
|---|---:|---:|---:|---:|
| logical bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| allocated bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| cloned bytes | n/a | NOT MEASURED | n/a | n/a |
| physical tail bytes | n/a | NOT MEASURED | n/a | n/a |
| sparse logical / allocated / hole bytes | n/a | NOT MEASURED | n/a | n/a |
| pre/post clone baseline verify ms | n/a | NOT MEASURED | n/a | n/a |

## Native result JSON

Do not populate PASS unless the commands and Unity gate actually ran on the
recorded Windows machine.

```json
{
  "status": "NOT MEASURED",
  "windowsProvider": "dev-drive-vhdx",
  "volumeKind": "Dev Drive",
  "devDrive": {
    "formatAttempted": false,
    "formatSucceeded": false,
    "queryExitCode": null,
    "queryOutput": "NOT MEASURED",
    "temporaryDriveLetterAssigned": false,
    "temporaryDriveLetterRemoved": false,
    "privateMountVerified": false
  },
  "filesystem": "NOT MEASURED",
  "blockCloneSupported": false,
  "physicalImageCreated": false,
  "differencingChildCreated": false,
  "sourceUnchanged": false,
  "baselineUnchanged": false,
  "semanticEquivalent": false,
  "residual": {
    "status": "NOT_MEASURED",
    "activeBaselineUses": { "measured": false, "count": 0 },
    "workerLeaseJournals": { "measured": false, "count": 0 },
    "workerDirectories": { "measured": false, "count": 0 },
    "baselineCreationLocks": { "measured": false, "count": 0 },
    "baselineStagingDirs": { "measured": false, "count": 0 },
    "workerStagingDirs": { "measured": false, "count": 0 },
    "unknownLeaseArtifacts": { "measured": false, "count": 0 },
    "unknownBaselineEntries": { "measured": false, "count": 0 },
    "unknownWorkerArtifacts": { "measured": false, "count": 0 },
    "quarantineEntries": { "measured": false, "count": 0 },
    "reservationLocks": { "measured": false, "count": 0 },
    "baselineCoordinationLocks": { "measured": false, "count": 0 },
    "baselineMutationMarkers": { "measured": false, "count": 0 },
    "coordinationArtifacts": { "measured": false, "count": 0 },
    "syntheticProbeDirectories": { "measured": false, "count": 0 },
    "mountReparsePoints": { "measured": false, "count": 0 },
    "mountDirectoryEntries": { "measured": false, "count": 0 },
    "junctions": { "measured": false, "count": 0 },
    "attachedDisks": { "measured": false, "count": 0 },
    "probeProcesses": { "measured": false, "count": 0 },
    "ownedVhdxFiles": { "measured": false, "count": 0 }
  }
}
```

The Windows script may record `PROMISING` only after native setup, regular and
sparse synthetic Block Clone, allocate-on-write isolation, forbidden-path
checks, every binary-measurable residual, and the outer probe-process count are
measured zero. The binary intentionally leaves process count unmeasured;
unknown artifacts or staging remnants fail the harness. Unity parity and the
1/2/4/8 ladder remain `NOT MEASURED`; `PROMISING` is not `PROVEN`.

Full baseline hashes remain the correctness gate. Record
`baselinePreCloneVerifyMs`, `baselinePostCloneVerifyMs`,
`baselineVerifyFileCount`, and `baselineVerifyLogicalBytes` before considering
a generation-token, USN, or validated-cache optimization.

## Verdict

`NOT MEASURED`

The standalone source and cross-platform tests are implementation evidence;
they are not native ReFS or Unity benchmark evidence.
