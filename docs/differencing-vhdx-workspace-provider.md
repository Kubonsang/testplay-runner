# Differencing VHDX workspace provider

Status: **experimental, explicit opt-in**. Static and fake-broker validation do
not make this the default backend. Native promotion requires the gates listed
below.

## User and agent workflow

An administrator performs one machine-local installation for one Windows user:

```powershell
testplay storage install [--root C:\absolute\store]
```

The install records the user SID, a broker-owned store, an isolated workspace
root, a 32 GiB allocated-byte quota, and a 20 GiB host-free floor. It installs
`TestPlayStorageBroker` as LocalSystem. The named pipe DACL allows only
LocalSystem, Administrators, and the installed SID. The server impersonates
every connection and compares its token SID before dispatching a request.
The service executable is copied with flush/read-back verification into the
ACL-protected store instead of referring to the CLI's invocation path.
`storage upgrade` stops the service, atomically replaces that stable copy, and
restarts it. `storage uninstall --preserve-data` keeps the ownership receipt so
a later `storage install` can re-register the exact preserved store.

After installation an AI agent uses the ordinary CLI without elevation:

```powershell
testplay run --workspace-backend vhdx-diff
# or, while the backend remains opt-in:
testplay run --workspace-backend auto
```

The agent sends compatibility keys, run IDs, and workspace IDs. It cannot
select a VHDX, physical disk, partition, or arbitrary mount path. The broker
derives every path below its registered roots.

## Configuration and selection

Schema 1 accepts the additive optional object:

```json
{
  "workspace": {
    "backend": "auto",
    "store_root": "C:\\ProgramData\\TestPlay\\Storage",
    "store_max_allocated_bytes": 34359738368,
    "minimum_host_free_bytes": 21474836480
  }
}
```

Flags override config. `legacy` and `image` retain their existing behavior.
Explicit `vhdx-diff` never falls back. `auto` may use `legacy` only when broker
hello or capacity admission fails before parent resolution/build or child
creation begins.

## Parent and child lifecycle

Each compatibility key owns one 64 GiB dynamic NTFS parent VHDX with a 2 MiB
VHDX block and 4 KiB sector. The parent transaction is:

```text
exclusive pending record → create/format/mount staging VHDX
→ Unity import/compile → volume flush → detach
→ read-only reattach and identity/layout verification → detach
→ read-only parent → durable metadata + COMPLETE → atomic publish
```

Metadata includes source snapshot evidence, file identity and write time,
virtual-disk ID, volume GUID and serial, filesystem/cluster/geometry, logical
and allocated bytes, full commit SHA-256, and an ownership token. Normal acquire
does not rehash the entire parent. It verifies readonly state, file identity,
size/write time, virtual-disk ID, and geometry.

Each run physically copies `Assets`, `Packages`, and `ProjectSettings`, creates
one differencing child, attaches it, and directory-mounts its volume at
`workspace\Library`. Release always unmounts/detaches/deletes the child before
removing the workspace shell. Cleanup uses a fresh bounded context even if the
Unity execution context was cancelled. Broker and mount code both reject an
existing or reparse-point `Library` boundary. No physical-copy fallback exists.

`--keep-workspace` detaches without deleting the child and writes an exact
retained record. `workspace attach <run-id>` reattaches that identity;
`workspace remove <run-id>` verifies the record, parent and child identities,
then deletes only that retained child.

## Capacity, monitoring, and recovery

Admission and LRU use actual host-allocated `.vhdx` bytes, not virtual size.
Pending, active, retained, and quarantined files are never LRU-deleted. Only an
inactive parent unused for 30 days is eligible. Each active admission reserves
2 GiB. A five-second monitor records child/store growth and host free space; at
the 5 GiB safety floor it cancels only the harness-owned Unity process.

Client and Unity PIDs plus a five-second heartbeat are durable in the lease
journal. After 30 seconds of client loss, or at broker/service startup, exact
expired ephemeral leases are reconciled. Live or retained leases are preserved.
Identity/attach uncertainty and stale partial parent VHDX files are quarantined
with manual recovery required; unknown paths are not deleted.

## Native promotion gates

The first destructive/native boundary has a guarded administrator harness. It
requires an explicit install acknowledgement, refuses an existing broker or
install receipt, uses a unique store, and performs ownership-safe uninstall and
post-disk residual measurement:

```powershell
.\scripts\run-vhdx-diff-native-phase1.ps1 `
  -UnityEditorPath 'C:\Program Files\Unity\Hub\Editor\6000.3.8f1\Editor\Unity.exe' `
  -InstallApproved
```

Run in order and stop on BSOD, identity ambiguity, parent mutation, semantic
mismatch, unknown attachment, or cleanup uncertainty:

1. broker install/uninstall and unauthorized-client tests;
2. small fixture single worker, then GNF single worker;
3. fixture 2→4 workers, then GNF 2→4 workers;
4. CLI/Unity termination, broker termination, and reboot recovery;
5. quota/LRU and retained attach/remove.

Promotion additionally requires GNF 4-worker semantic parity and isolation,
one parent plus one child per worker, child batch ready within 30 seconds,
per-worker ready within 10 seconds, peak allocated storage at most 10 GiB, and
zero disk/mount/process/ephemeral-child residual. Eight workers, long-running
child growth, performance superiority, production readiness, and release
readiness remain **NOT MEASURED**.

### Native evidence history

The first Phase 1 attempt at commit
`4a98bad9918483376976983494484ce770964bde` is retained as **FAILED**:

- artifact SHA-256:
  `3497141B7C6873FEA5D4E598A49A1582E41282956608F8671A012E4A8FB8C180`;
- parent build, read-only verification, child create/attach/mount, and child
  release completed with no fallback and no attached-disk residual;
- EditMode ran 2 tests (1 pass, 1 fail) because the harness omitted the
  fixture's required `TESTPLAY_UNITY_FIXTURE_MARKER` environment variable;
- Windows PowerShell 5.1 promoted the CLI's normal stderr progress line to a
  terminating harness error;
- service deletion completed, but immediate store removal raced the stopped
  broker executable's final file lock. The exact store and install receipt were
  preserved with no file-backed disks.

This is harness/uninstall transaction evidence, not a VHDX compatibility
failure. The follow-up adds explicit marker setup, native exit-code capture,
bounded executable-lock retry, and an ownership-restricted interrupted
uninstall recovery path. It does not rewrite the failed result as a pass.

The fresh Phase 1 rerun on 2026-08-11 is
`VHDX_DIFF_NATIVE_PHASE1_PROMISING`:

- artifact SHA-256:
  `2DF2C43A7BCAF5FD6EC2E762B5E59BB66809A38B3EFD677797B7FF5093AC6D21`;
- EditMode passed 2/2 and PlayMode passed 1/1 with no fallback;
- the second run reused the same immutable parent, both children reported
  `cleanupState: released`, and uninstall left no service, store, or
  file-backed disk residual.

The native broker security gate then passed with artifact SHA-256
`F54D30959A48995537EDEDCE456B3AE7FAD175D447B6793A4E2141E0A297A575`:

- the installed user completed an authenticated protocol-v2 hello;
- a forged claimed SID and an actual SYSTEM caller were rejected by the
  broker's impersonated-token authorization check;
- an actual LOCAL SERVICE caller was denied by the named-pipe DACL;
- caller-selected workspace roots and `..\\escape` workspace IDs were
  rejected before dispatch;
- uninstall removed both exact scheduled-task probes, the service, receipt,
  store and scratch root, with zero new file-backed disks.

The pipe is configured with `PIPE_REJECT_REMOTE_CLIENTS`, but an attempted
connection from a second physical machine remains **NOT MEASURED**. Recovery
promotion gates also remain outstanding.

The GNF single-worker gate then passed as
`GNF_SINGLE_WORKER_COMPATIBLE` with artifact SHA-256
`073D47A729A9D4273F86533A24E6988B18FDADB5616B375BB2F3760C1C254DE8`.
The pinned EditMode and PlayMode tests each passed 1/1 with semantic parity,
one immutable parent was reused, each child released to zero measured
allocation, source hashes were unchanged, and uninstall left zero outer
residual. GNF multi-worker compatibility remains a later gate.

The first fixture two-worker attempt at commit `aab38d4` is retained as
**FAILED** with artifact SHA-256
`F276A940EB7BC26C18285214B52556DFCC204D8F94DA764517A8DA5D8A495266`.
One simultaneous client reached the server between accept and creation of the
next unlimited named-pipe instance and received `ERROR_PIPE_BUSY`; the other
worker passed and cleanup was residual zero. Commit `0d34e24` added a bounded,
context-aware `WaitNamedPipeW` retry only for that transient error. Two later
harness-only failures are also retained: SHA-256
`68736AA9E2EB5944859F322FFE4E23A51C55A73CDCE88DD9630165264B1D0650`
incorrectly required a small Library write to grow VHDX allocated bytes, and
SHA-256
`917CB8F184D9F785A8C55982C65194A179E1D4F5C7C9EF20AC2EEE82D31BD83B`
did not account for an `omitempty` zero-byte release field. Both executions had
two passing Unity workers, overlapping process intervals, released children,
and zero outer residual; neither failed in the provider lifecycle.

The fresh fixture two-worker rerun at commit `c2574b1` passed as
`UNITY_VHDX_DIFF_TWO_WORKERS_COMPATIBLE`:

- artifact SHA-256:
  `4C221954048969D31177A7D7B22EABDBE3B01BCEEFC28BCD8A40A03A1F2A8465`;
- both selected Library write/read tests passed, with the Unity process
  intervals overlapping for 5,299 ms;
- the workers shared one 100 MiB allocated immutable parent but used distinct
  child mounts, physical disks, volume GUIDs, PIDs, and run IDs;
- each child measured 37,748,736 allocated bytes at ready and peak, then a
  measured zero after release, with `fallbackUsed: false` and
  `cleanupState: released`;
- the committed parent SHA-256 matched metadata, the fixture source remained
  unchanged, and uninstall left zero new disks, drive letters, or processes.

The fixture four-worker gate at commit `692a012` then passed as
`UNITY_VHDX_DIFF_FOUR_WORKERS_COMPATIBLE`:

- artifact SHA-256:
  `0424CA627AD0E1017B9DD58A8A5D915FC6E81D184D7EE3F1333376776B500AF0`;
- all four selected Library write/read tests passed and all four Unity process
  intervals shared a 6,730 ms common interval;
- one 100 MiB allocated immutable parent was shared by four distinct child
  mounts, physical disks, volume GUIDs, PIDs, and run IDs;
- every child measured 37,748,736 allocated bytes at ready and peak and zero
  after release, with no fallback and `cleanupState: released`;
- parent hash/metadata, fixture source isolation, storage uninstall, and outer
  disk/drive-letter/process residual checks all passed.

The first GNF two-worker attempt at commit `b36c107` is retained as
**FAILED** with artifact SHA-256
`750E898202C30F80CC001E90BEA80C932780444ED4F6727570EBC52E21C6D70B`.
Both EditMode workers completed the selected test, but one successful Unity
process was reported as `exec: WaitDelay expired before I/O complete` because a
descendant still held the inherited output pipe. The result XML and Unity log
both proved a passing test and direct exit code zero. Cleanup and outer
residual checks were zero. Commit `9eb5539` preserves cancellation and signal
semantics while accepting `exec.ErrWaitDelay` after the direct Unity process
has exited successfully.

The next GNF two-worker attempt at commit `9eb5539` is retained as
**FAILED** with artifact SHA-256
`7027BD40180B437BD2EDD4797812B3345654531E1515AC7E69966A3D047A003B`.
Both EditMode and PlayMode worker pairs passed their selected tests, but one
PlayMode child release observed a transient Storage cmdlet access-path view and
rejected its directory mount as not owned. The broker subsequently recovered
the exact ephemeral child to active/retained/pending/quarantine counts of zero;
the exact orphaned physical workspace shell was removed only after proving the
Library mount, reparse entries, related processes, and file-backed disks were
absent. Normal ownership-safe uninstall then removed the one parent, service,
receipt, store, and workspace root with zero residual. Commit `070b234`
normalizes and boundedly re-reads the exact disk/partition access path, removes
only the single matching path returned by that partition, flushes named-pipe
responses before disconnect, and retries only pre-request pipe-instance
transients.

The fresh GNF two-worker rerun at commit `070b234` passed as
`GNF_VHDX_DIFF_TWO_WORKERS_COMPATIBLE`:

- artifact SHA-256:
  `2C7D6FB53181FDC37EFFD3A918633F28B55AF0D6F25421AC48F5FC31B6D1BF50`;
- both EditMode workers passed
  `GNF.DungeonGen.Tests.WallPropValidatorTests.NullPrefab_Error` and
  overlapped for 29,399 ms;
- both PlayMode workers passed
  `DOOR_CONSENSUS_Tests.Proximity_CountsNearestExitWithinRadius` and
  overlapped for 43,419 ms;
- every worker matched the pinned NTFS reference semantic digest, used the same
  immutable parent key, a distinct child/mount/disk/volume, no fallback, and
  `cleanupState: released`;
- child ready allocation was 37,748,736 bytes; observed peaks ranged from
  205,520,896 to 742,391,808 bytes and every released allocation was measured;
- the detached read-only parent was 4,932,501,504 bytes and its full SHA-256
  matched committed metadata;
- source evidence was unchanged, storage status reported no active, retained,
  pending, or quarantined child, and uninstall left zero new disks, drive
  letters, processes, service, receipt, store, or workspace root.

The GNF four-worker gate at commit `e2a3d7d` then passed as
`GNF_VHDX_DIFF_FOUR_WORKERS_COMPATIBLE`:

- artifact SHA-256:
  `E60F2D9CD9FE5124BDFDA461864D07DC1F5B2E7122844AB1BBC4A246C7873A57`;
- all four EditMode workers passed with a 41,162 ms common process interval,
  and all four PlayMode workers passed with a 58,722 ms common interval;
- all eight worker results matched the pinned NTFS reference semantic digest,
  shared one immutable parent key, used distinct child mount/disk/volume
  identities within each phase, reported no fallback, and released cleanly;
- each child measured 37,748,736 bytes at ready. Observed child peaks were
  205,520,896–641,728,512 bytes and every released allocation was measured;
- the read-only parent was 4,875,878,400 bytes. Parent plus simultaneous child
  peaks were approximately 6.43 GiB for EditMode and 6.84 GiB for PlayMode,
  below the 10 GiB promotion storage ceiling;
- provider create+attach+mount time was at most 364 ms per child. Warm full
  workspace preparation, including the physical project shell, measured
  14.794–15.122 seconds per worker, so this evidence does not claim the
  separate 10-second full-worker-ready objective;
- source evidence and committed parent integrity were unchanged. Storage
  status had no active, retained, pending, or quarantined child, and uninstall
  left zero service, receipt, store, workspace, disk, drive-letter, or process
  residual.

The raw four-worker artifact inherited a harness reporting bug that listed
“GNF four/eight workers” under `notMeasured` even though its four-worker
instances and verdict passed. The follow-up makes that field count-aware;
the raw worker, parity, overlap, storage, and residual evidence is unaffected.

GNF eight-worker, forced-termination, broker-restart, reboot-recovery,
quota/LRU, and production/release readiness gates remain **NOT MEASURED**.
`auto` promotion therefore remains blocked.

## Win32 references

- [Named Pipe Security and Access Rights](https://learn.microsoft.com/windows/win32/ipc/named-pipe-security-and-access-rights)
- [Impersonating a Named Pipe Client](https://learn.microsoft.com/windows/win32/ipc/impersonating-a-named-pipe-client)
- [CREATE_VIRTUAL_DISK_PARAMETERS](https://learn.microsoft.com/windows/win32/api/virtdisk/ns-virtdisk-create_virtual_disk_parameters)
- [GET_VIRTUAL_DISK_INFO](https://learn.microsoft.com/windows/win32/api/virtdisk/ns-virtdisk-get_virtual_disk_info)
