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

## Win32 references

- [Named Pipe Security and Access Rights](https://learn.microsoft.com/windows/win32/ipc/named-pipe-security-and-access-rights)
- [Impersonating a Named Pipe Client](https://learn.microsoft.com/windows/win32/ipc/impersonating-a-named-pipe-client)
- [CREATE_VIRTUAL_DISK_PARAMETERS](https://learn.microsoft.com/windows/win32/api/virtdisk/ns-virtdisk-create_virtual_disk_parameters)
- [GET_VIRTUAL_DISK_INFO](https://learn.microsoft.com/windows/win32/api/virtdisk/ns-virtdisk-get_virtual_disk_info)
