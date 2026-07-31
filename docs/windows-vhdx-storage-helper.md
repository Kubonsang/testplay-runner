# On-demand Differencing VHDX storage helper

## Status

```text
Protocol and implementation: COMPLETE
Cross-platform fake-backend tests: PASS
Elevated Windows integration: NOT RUN in the implementation session
Unity and public CLI integration: NOT PRESENT
```

The helper is an on-demand child process. It is not a permanent daemon, a
Windows service, or a public `testplay` command. A caller starts it for one
workspace lease, communicates over NDJSON, and keeps the process alive until
Release or stdin EOF completes cleanup.

## Process model

```text
Caller starts testplay-storage-helper
→ Hello
→ Acquire
→ create Child VHDX
→ open and attach while retaining the VirtDisk handle
→ resolve the exact File Backed Virtual disk and volume
→ mount in the caller-owned workspace root
→ return a ready WorkspaceLease
→ Release, Shutdown, or stdin EOF
→ unmount
→ detach
→ wait for detach visibility
→ optionally delete the helper-created Child
→ record released
→ exit
```

Version 1 permits one active lease per helper process. It neither installs nor
contacts the Windows Service Manager and never requests UAC elevation.

## Protocol

stdin and stdout use NDJSON: exactly one JSON object per line. stdout is
reserved for protocol responses. Human-readable diagnostics use stderr.

Every request contains:

```json
{"schemaVersion":1,"operation":"hello","requestId":"req-001"}
```

Supported operations are `hello`, `acquire`, `release`, and `shutdown`.
Unknown fields, schema versions, operations, or invalid request IDs return a
structured error.

Acquire returns a lease only after the mount is visible:

```json
{
  "schemaVersion": 1,
  "requestId": "req-002",
  "ok": true,
  "lease": {
    "leaseId": "lease-...",
    "provider": "vhdx-differencing",
    "requestId": "req-002",
    "parentPath": "C:\\Dev\\testplay-storage\\parents\\base.vhdx",
    "childPath": "C:\\Dev\\testplay-storage\\children\\worker-00.vhdx",
    "physicalPath": "\\\\.\\PhysicalDriveN",
    "volumeGuidPath": "\\\\?\\Volume{...}\\",
    "mountPath": "C:\\Dev\\testplay-workspaces\\worker-00\\Library",
    "state": "ready",
    "createdAt": "..."
  }
}
```

## Handle and ownership boundary

`internal/vhdxstorage` contains the reusable VirtDisk layer extracted from the
standalone probe. Both the probe and helper use the same Create/Open/Attach,
physical-path validation, volume resolution, mount, unmount, and detach code.
Probe-only Parent fixture, deterministic payload, hash, parity, and sibling
assertions remain in `internal/vhdxprobe`.

The helper retains the Child's `OpenVirtualDisk` handle for the entire ready
lease. Attach does not use `PERMANENT_LIFETIME`. A successful Release explicitly
unmounts and detaches before closing the handle or deleting the Child.

## Journal

Each lease is atomically persisted at:

```text
<storeRoot>\leases\<lease-id>.json
```

Writes use a temporary file, file flush, close, and atomic rename. The helper
will not perform the next storage operation if the preceding journal write
fails. The `leases` directory must be a real directory, not a symlink or
reparse path.

States are:

```text
requested
creating-child
opening
attaching
waiting-volume
mounting
ready
releasing
unmounting
detaching
released
quarantined
```

Non-released journals from another process produce `orphan-found`. Version 1
does not adopt, delete, or automatically recover them.

## Idempotency

- Repeating an Acquire `requestId` in the same process returns the same lease.
- A request ID cannot be reused for another operation.
- Repeating Release for a released lease returns the recorded result without a
  second unmount or detach.
- A different request for an active Child or mount path is rejected.
- Version 1 does not provide cross-process idempotency.

## Path safety

`storeRoot`, `workspaceRoot`, `parentPath`, `childPath`, and `mountPath` must be
absolute local paths. Drive roots and network paths are forbidden.

- `childPath` must be a new `.vhdx` below `storeRoot`.
- `mountPath` must be absent or an empty real directory below `workspaceRoot`.
- Parent and Child must differ.
- The Parent must be an existing regular `.vhdx` file.
- Existing path components may not escape through symlinks or reparse points.
- Only the exact `\\.\PhysicalDriveN` returned by the retained VirtDisk handle
  is accepted.
- The matching disk must report `File Backed Virtual` before PowerShell storage
  operations run.
- Only a mount directory and Child created by this helper are eligible for
  deletion.

Physical disks, existing partitions, existing volumes, existing Child files,
and unrelated mount paths are never initialized, formatted, overwritten,
detached, or deleted.

## EOF and failure behavior

stdin EOF with an active lease starts a bounded best-effort Release. On a
successful cleanup the helper exits normally. If unmount, detach, visibility,
journal, or cleanup fails:

- the journal is marked `quarantined` when possible;
- the Child and journal are preserved;
- stderr and the final protocol error include the structured operation/path;
- the helper returns a non-zero exit code.

This is not proof of recovery after forced termination or power loss. Orphan
recovery remains separate work.

## Metrics

Only measured values are emitted. Unavailable values are omitted rather than
estimated.

```text
totalWallClockMs
acquireWallClockMs
releaseWallClockMs
childCreateMs
childOpenMs
attachCallMs
physicalPathResolveMs
pnpDiscoveryWaitMs
volumeReadyWaitMs
mountCallMs
mountVisibilityWaitMs
workspaceReadyMs
unmountCallMs
detachCallMs
detachVisibilityWaitMs
cleanupMs
powershellBootstrapMs
childBeforeAttachLogicalBytes
childReadyLogicalBytes
childReleasedLogicalBytes
childReadyAllocatedBytes
childReleasedAllocatedBytes
```

Logical file length and allocated filesystem bytes are separate fields.
Allocated bytes use `GetCompressedFileSizeW` when available.

## Validation

General validation:

```powershell
go test ./...
go vet ./...
git diff --check
```

Run hardware integration only from an elevated PowerShell with a dedicated
absent or empty root:

```powershell
cd C:\Dev\testplay-runner
$env:TESTPLAY_VHDX_HELPER_ROOT = "C:\Dev\testplay-vhdx-helper-test"

go test -tags=vhdx_helper_integration ./internal/storagehelper `
  -run '^TestVHDXStorageHelper' `
  -v -count=1
```

Only after the complete one-run suite passes:

```powershell
go test -tags=vhdx_helper_integration ./internal/storagehelper `
  -run '^TestVHDXStorageHelperAcquireRelease$' `
  -v -count=5
```

The integration suite prepares a Parent through the probe fixture, launches a
real helper child process, verifies the baseline payload, mutates the mounted
Child, tests duplicate Acquire, performs Release or EOF cleanup, and compares
File Backed Virtual disks before and after.

## Explicit non-goals

- Unity, GNF_, Image backend, or `LibraryMaterializer` integration
- public `testplay` CLI changes
- Windows Service, permanent daemon, named-pipe broker, or boot startup
- multi-client operation, parallel workers, or sharding
- cross-process orphan adoption or deletion
- ReFS or Physical Copy fallback
- Honey Bee integration
