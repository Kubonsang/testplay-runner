# macOS and Linux native CoW storage helper

## Status and boundary

```text
Schema-1 Hello/Acquire/Release/Shutdown protocol: SAME AS WINDOWS
macOS APFS clonefile implementation: COMPLETE
macOS local native lifecycle validation: PASS
Linux reflink implementation: COMPLETE
Linux and Windows cross-compilation: PASS
Linux native reflink hardware validation: NOT YET
Unity Library compatibility on macOS/Linux: NOT YET
Public testplay CLI or Image backend integration: NOT PRESENT
Production default readiness: NOT YET
```

This is an unreleased integration primitive after v0.11.0. It does not add a
seventh public `testplay` command, change `testplay run`, or change the released
CLI version. The executable remains `testplay-storage-helper` and accepts the
same schema-1 NDJSON operations as the Windows Differencing VHDX provider.
The additive cross-platform implementation reports `helperVersion: "v2"`;
`schemaVersion` remains 1.

## Provider selection

| Platform | Provider | Elevation | CoW failure behavior |
|---|---|---:|---|
| Windows | `vhdx-differencing` | required | structured VHDX error |
| macOS | `apfs-clonefile` | not required | `cow-unavailable`; no copy fallback |
| Linux | `linux-reflink` | not required | `cow-unavailable`; no copy fallback |

`hello` now includes `provider` and `requiresElevation` while retaining the
existing `platform` and `elevated` observations. Callers must use
`requiresElevation`, not assume that a non-root Unix helper is unusable.

## Unix Acquire and Release

The request field names remain stable, but the native objects differ:

```text
ParentPath  immutable Library directory
ChildPath   new CoW-cloned directory below StoreRoot
MountPath   workspace Library path below WorkspaceRoot
```

Acquire validates all roots and path ownership, rejects symlinks and special
files in the Parent, creates and revalidates the Child with the platform CoW
primitive, and creates a helper-owned symbolic link from MountPath to
ChildPath. If MountPath was an existing empty directory, Release restores that
empty directory.

Release verifies that MountPath is still the exact helper-owned link before
removing it. It refuses cleanup with `mount-ownership-lost` if another process
replaced or retargeted the link. `deleteChildOnRelease: true` removes only the
validated real Child directory created for the lease. Release, Shutdown, and
stdin EOF use the same journal and idempotency rules as Windows.

## macOS implementation

macOS walks the immutable Parent and invokes `clonefileat(2)` for every regular
file with `CLONE_NOFOLLOW_ANY | CLONE_NOOWNERCOPY`. The helper calls the syscall
directly, so it does not inherit `/bin/cp -c`'s documented physical-copy
fallback. Directories are recreated with their permissions and timestamps. A
cross-filesystem or non-cloning filesystem error is returned as
`cow-unavailable`.

Local validation was run on macOS 26.5.1 arm64 with the workspace on APFS. It
passed native clone creation, Child mutation with Parent isolation, protocol
Acquire/Release, helper-owned link cleanup, existing empty MountPath
restoration, and refusal to remove a retargeted mount.

## Linux implementation

Linux invokes GNU
`cp --archive --no-preserve=ownership --reflink=always --no-target-directory`
after the same symlink/special-file safety scan. `--reflink=always` is intentional:
unsupported filesystems and cross-filesystem layouts fail instead of consuming
the full logical Parent size. GNU coreutils `cp` is therefore a runtime
requirement for the Linux helper.

The reported `child*AllocatedBytes` fields are filesystem block-accounting
observations. On CoW filesystems they are not exclusive or incremental physical
consumption and must not be presented as the space newly consumed by a Child.

The Linux binary and tests cross-compile successfully. A native run on a known
reflink-capable filesystem such as Btrfs or reflink-enabled XFS is still
required before claiming Linux hardware proof or Unity compatibility.

## Validation

General and macOS native validation:

```bash
go test -count=1 ./internal/vhdxstorage ./internal/storagehelper
go test ./...
go vet ./...
git diff --check
```

The native tests skip with an explicit CoW-unavailable reason when the test
filesystem cannot provide the required primitive. Such a skip is not a pass
for Linux hardware readiness.
