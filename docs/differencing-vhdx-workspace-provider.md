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

The fixture client/Unity forced-termination gate at commit `4786fd9` passed as
`VHDX_DIFF_FIXTURE_FORCED_TERMINATION_RECOVERY_PASS`:

- artifact SHA-256:
  `9D5BBE6F395F0C041EF3D1CEBCBF73C22B3C9FF292F9C5C86AB55F92FC486F3C`;
- the harness durably captured and hashed the exact broker-authored lease
  journal and workspace ownership marker before termination;
- only the journal's client PID 32688 and Unity PID 39008 were terminated,
  after their executable paths, lease ID, ownership token, workspace, mount,
  and child VHDX identities were verified;
- after the 30-second client-loss grace, the broker removed the exact orphan
  child, journal, and physical workspace. The recovered store retained its one
  immutable parent and reported active, retained, pending, and quarantined
  counts of zero with `manualRecoveryRequired: false`;
- normal uninstall then removed the service, receipt, store, workspace root,
  file-backed disk, drive-letter, and process residuals to measured zero.

This is fixture evidence for simultaneous CLI/Unity termination.

The first fixture broker-process termination/restart run failed safely at
commit `ebbf739` and preserved the exact owned residual:

- artifact SHA-256:
  `92C8238263ADFD94A2696FA1DF232EE25C3F2AF9E84EEA8AEDC953EBFD2F0FCE`;
- terminating the broker closed its VirtDisk handle and detached the exact
  child, but the workspace `Library` directory-volume mount reparse point
  remained. The restarted broker could not attach over that stale mount;
- no unknown disk was attached and cleanup was correctly classified as
  `preserved` rather than deleting an identity it had not revalidated.

Recovery development preserved two further exact failures. Artifact
`24C60A0694AEB281103A5FB5F1F24850315C4D299383477A4520FDF986C681E9`
showed that Go's directory classification was insufficient for a Windows
volume-mount reparse point. Artifact
`788E0417EFE31BC86E76338970298BA8BC536DFA72DE657EE1A1A1894702E154`
then proved stale-mount removal and child release worked, but left the now
empty ordinary `Library` directory and therefore stopped before deleting the
workspace journal or owner marker.

The follow-up recovery is deliberately exact and ownership-gated: it verifies
the journal's child, mount target, volume GUID, workspace marker, and detached
disk state; removes only the verified stale mount/empty mount directory; then
reattaches and releases the same child. The retained residual was recovered as
`VHDX_DIFF_BROKER_RESTART_RESIDUAL_RECOVERED`:

- artifact SHA-256:
  `E2C64B69372A2641B83207F8912EC39E0A9785EED4458AA3ADEBDFBEAFEF9003`;
- child, journal, owner marker, workspace, service, receipt, store, attached
  disk, drive letter, and harness-owned process residuals were measured zero.

A fresh end-to-end run at commit `a082aa9` then passed as
`VHDX_DIFF_FIXTURE_BROKER_RESTART_RECOVERY_PASS`:

- artifact SHA-256:
  `0FBE83BCD4EA2C4B546714D1D75595DFE9753125206563456091661280A70B02`;
- broker PID 29092 was terminated and the service restarted as PID 14824;
- the child was detached while the exact stale mount still targeted
  `Volume{f3be417f-5382-4348-a20e-b1f860f39f48}`;
- the restarted broker reconciled the exact lease and normal uninstall left
  active, retained, pending, quarantined, disk, drive-letter, service, store,
  workspace, and process residuals at measured zero.

The raw PASS summary serialized `crashClientExitCode` as null because Windows
PowerShell read the asynchronous `Process` snapshot before redirected streams
were fully drained. `crash-client-stdout.txt` independently records the
expected testplay exit code 2 and exact ownership-mismatch warning. The harness
now performs the parameterless process wait and refreshes the snapshot before
capturing that field; this reporting correction does not change the native
recovery result.

The Windows reboot gate uses a separate two-phase harness:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\run-vhdx-diff-reboot-recovery.ps1 `
  -Phase Prepare `
  -UnityEditorPath 'C:\Program Files\Unity\Hub\Editor\6000.3.8f1\Editor\Unity.exe' `
  -InstallApproved `
  -RebootApproved
```

`Prepare` builds/reuses one fixture parent, waits for one exact ephemeral child
and Unity process to reach the ready journal state, durably records their paths,
tokens, hashes, service identity, and boot-session identity, then returns
`REBOOT_REQUIRED`. It does not invoke the reboot and does not delete the active
lease. After the user reboots, the exact `VHDX_DIFF_REBOOT_VERIFY_COMMAND`
printed by the prepare phase validates that Windows and broker boot identities
changed, waits for service-start recovery, requires the exact journal, child,
workspace, disk, and mount to disappear, checks storage status, and only then
runs the normal ownership-safe uninstall.

Lease journals record the Windows System process creation FILETIME as
`bootSessionId`. A broker restart during the same boot still honors live PIDs
and the client-loss grace. After a measured boot-session change, recovery does
not allow a coincidentally reused PID or a recent journal timestamp to preserve
the prior-boot orphan. If Windows cannot measure this identity, the field stays
empty and recovery conservatively retains the legacy PID/grace behavior.

The two phases completed around a real Windows reboot at commit `f9b4e33` as
`VHDX_DIFF_FIXTURE_REBOOT_RECOVERY_PASS`:

- artifact ZIP:
  `C:\Users\user\AppData\Local\Temp\testplay-vhdx-diff-reboot-recovery-20260812-002747-769.zip`;
- artifact SHA-256:
  `B4BD55C3557D6730441647F01B63A7D9F74655FB7D1B6AB20C02C1AFAAB28B7F`;
- Windows boot time changed from `2026-08-10T12:43:17.5Z` to
  `2026-08-12T00:28:35.5Z`, while the computer identity remained unchanged;
- broker boot identity changed from
  `system-process-01dd28c5d5c45358` to
  `system-process-01dd29f187cfa41f`;
- the pre-reboot lease was
  `lease-db98ab3334f88c9ecd92b6aff9e96fa9`. Its durable journal SHA-256 was
  `E1E3DC0F0ACE5E8068B6C042E0D43F78F24B8549DE6694C09BE95081118B8A71`
  and the matching workspace-owner SHA-256 was
  `CB4305E5B7C3409127B0961378E52D873FCB6EBD76B48CFE886858F1548C8FC9`;
- the automatically started broker reconciled the prior-boot ephemeral lease
  and reported active, retained, pending, and quarantine counts of zero with
  `manualRecoveryRequired: false` and the immutable parent still valid;
- normal uninstall succeeded with `cleanupState: released`. The exact pointer,
  receipt, service, store, workspace, child, mount, attached disk, new drive
  letter, Unity/testplay process, pending, retained, and quarantine residuals
  were independently measured zero.

GNF forced-termination, GNF eight-worker, quota/LRU, and production/release
readiness gates remain **NOT MEASURED**. `auto` promotion therefore remains
blocked.

## Win32 references

- [Named Pipe Security and Access Rights](https://learn.microsoft.com/windows/win32/ipc/named-pipe-security-and-access-rights)
- [Impersonating a Named Pipe Client](https://learn.microsoft.com/windows/win32/ipc/impersonating-a-named-pipe-client)
- [CREATE_VIRTUAL_DISK_PARAMETERS](https://learn.microsoft.com/windows/win32/api/virtdisk/ns-virtdisk-create_virtual_disk_parameters)
- [GET_VIRTUAL_DISK_INFO](https://learn.microsoft.com/windows/win32/api/virtdisk/ns-virtdisk-get_virtual_disk_info)
