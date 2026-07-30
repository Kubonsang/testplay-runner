# Differencing VHDX workspace probe

## Purpose

This probe evaluates a Windows-native copy-on-write workspace primitive before
it is connected to Unity, the Image backend, or a long-running service. It
creates one immutable Parent VHDX and two independent differencing children,
then checks Parent isolation, sibling isolation, reattach persistence, file
growth, detach, and cleanup.

The probe is research code under `internal/vhdxprobe`. It is not exposed by the
public `testplay` CLI and does not alter `LibraryMaterializer`.

## Architecture

```text
512 MiB dynamic Parent VHDX
├─ Child A differencing VHDX
└─ Child B differencing VHDX
```

The Parent contains a deterministic 64 MiB payload and JSON manifest. Payload
blocks include their block index so that mutations and parity checks are
repeatable.

## Windows environment

The implementation targets 64-bit Windows and the Microsoft VHDX provider.
Development structure layouts were checked against Windows SDK
`10.0.26100.0` `virtdisk.h`.

The initial development session reported:

```text
WindowsProductName: Windows 10 Pro
WindowsVersion: 2009
DisplayVersion: 25H2
Build: 26200.8875
Architecture: amd64
Go: go1.26.2 windows/amd64
```

## Permissions

Unit tests and compilation do not require elevation. The integration probe
checks administrator membership before it creates a VHDX. A non-elevated
process returns the structured `not-elevated` error without creating,
attaching, initializing, or formatting anything.

Run the integration command only from an elevated PowerShell after confirming
that the probe root is dedicated and empty.

## APIs

The core lifecycle calls `VirtDisk.dll` directly:

- `CreateVirtualDisk`
- `OpenVirtualDisk`
- `AttachVirtualDisk`
- `DetachVirtualDisk`
- `GetVirtualDiskPhysicalPath`
- `GetVirtualDiskInformation`

Dynamic and differencing VHDX creation uses
`CREATE_VIRTUAL_DISK_PARAMETERS` Version 2. Child creation supplies the
absolute Parent path and the Microsoft VHDX storage type.

The integration-only filesystem bootstrap uses built-in Storage PowerShell
cmdlets after the VirtDisk handle supplies a path of the exact form
`\\.\PhysicalDriveN`:

- `Get-Disk`
- `Initialize-Disk`
- `New-Partition`
- `Format-Volume`
- `Add-PartitionAccessPath`
- `Remove-PartitionAccessPath`

`Initialize-Disk` and `Format-Volume` are unreachable until the single
corresponding `Get-Disk -Number N` result reports `File Backed Virtual`, is
RAW, and has no partitions. No external or user-supplied disk number is
accepted.

Microsoft API references:

- <https://learn.microsoft.com/windows/win32/api/virtdisk/nf-virtdisk-createvirtualdisk>
- <https://learn.microsoft.com/windows/win32/api/virtdisk/nf-virtdisk-openvirtualdisk>
- <https://learn.microsoft.com/windows/win32/api/virtdisk/nf-virtdisk-attachvirtualdisk>
- <https://learn.microsoft.com/windows/win32/api/virtdisk/nf-virtdisk-detachvirtualdisk>
- <https://learn.microsoft.com/windows/win32/api/virtdisk/nf-virtdisk-getvirtualdiskphysicalpath>

## Parent fixture

Each run owns exactly one newly created directory:

```text
<probe-root>\
└─ testplay-vhdx-probe-<operation-id>\
   ├─ parent.vhdx
   ├─ child-a.vhdx
   ├─ child-b.vhdx
   └─ mounts\
```

The configured root must be absolute, must not be a drive root or symlink, and
must be absent or empty. Its parent must already exist. Existing files are
never overwritten.

## Mount strategy

`AttachVirtualDisk` uses `ATTACH_VIRTUAL_DISK_FLAG_NO_DRIVE_LETTER`. After the
physical device and BusType safety checks, the single basic-data partition is
mounted at a probe-owned empty directory with `Add-PartitionAccessPath`.

Directory mount points avoid global drive-letter selection and keep all
visible paths inside the unique operation directory. If disk or partition
identity is ambiguous, the probe stops.

## Correctness checks

1. Seed and detach the Parent.
2. Record the complete Parent VHDX SHA-256 and file size.
3. Create Child A and Child B from the same absolute Parent path.
4. Verify each child's resolved Parent with `GetVirtualDiskInformation`.
5. Confirm Child A initially reads the baseline payload.
6. Mutate Child A's payload, rename the manifest, and create marker files.
7. Confirm the complete Parent VHDX hash is unchanged.
8. Confirm Child B still reads the baseline and contains no Child A markers.
9. Mutate Child B and confirm its marker never appears in Child A.
10. Detach and reattach Child A and verify its exact mutated payload hash.
11. Attach the Parent read-only and verify its baseline and manifest.

The result exposes:

```text
parentIsolationPassed
siblingIsolationPassed
reattachPersistencePassed
cleanupPassed
```

## Storage and timing

The result records:

```text
parentVirtualBytes
parentFileBytes
childInitialFileBytes
childAfterAttachFileBytes
childAfterMutationFileBytes
childAfterReattachFileBytes
logicalPayloadBytes
```

It also records durations for Parent create/attach/initialize/seed/detach,
Child create/attach/detach, mount resolution, mutation, and cleanup.

These VHDX file-size measurements are not directly comparable to a copy of the
entire VHDX file. A future Physical Copy comparison should copy only the same
64 MiB logical payload and document filesystem cache effects.

## Validation commands

General validation:

```powershell
go test ./...
go vet ./...
git diff --check
go env CGO_ENABLED
```

Elevated, explicitly opted-in integration:

```powershell
New-Item -ItemType Directory -Force -Path C:\Dev\testplay-vhdx-probe
$env:TESTPLAY_VHDX_PROBE_ROOT = "C:\Dev\testplay-vhdx-probe"

go test -tags=vhdx_integration ./internal/vhdxprobe `
  -run '^TestDifferencingVHDX' `
  -v -count=1
```

Only after the complete command succeeds:

```powershell
go test -tags=vhdx_integration ./internal/vhdxprobe `
  -run '^TestDifferencingVHDXProbe$' `
  -v -count=5
```

If the environment variable is absent, integration tests skip with an explicit
reason. Once opted in, a lifecycle failure is a test failure, not a skip.

## Cleanup

Every mounted partition is unmounted before `DetachVirtualDisk`, and every
handle is closed. The operation directory is removed only after cleanup
succeeds. If unmount or detach fails, automatic directory deletion stops so
the failure remains diagnosable and no mounted path is traversed by cleanup.

After any integration run, inspect for:

```powershell
Get-Disk | Where-Object BusType -eq 'File Backed Virtual'
Get-ChildItem C:\Dev\testplay-vhdx-probe -Force
```

## Current evidence and verdict

The implementation, Windows build, unit tests, static analysis, and formatting
checks were completed in a non-elevated session. The live integration probe
was not executed because the safety policy requires elevation.

```text
Verdict: IMPLEMENTED / AWAITING ELEVATED VALIDATION
```

Do not report `PROVEN` until all Parent, sibling, reattach, cleanup, and
residual-attachment checks pass five times.

## Storage Daemon readiness

The API and safety boundary are suitable for evaluating a later Storage Daemon
protocol, but daemon implementation should wait for elevated 5/5 evidence.
Crash recovery, orphan adoption, concurrency, worker sharding, Unity execution,
and service installation remain separate follow-up work.
