# macOS and Linux native CoW storage helper

## Status and boundary

```text
Schema-1 Hello/Acquire/Release/Shutdown protocol: SAME AS WINDOWS
macOS APFS clonefile implementation: IMPLEMENTED
macOS local native lifecycle validation: PASS on one local arm64 APFS environment
Linux reflink implementation: IMPLEMENTED
Linux and Windows cross-compilation: PASS
Linux native reflink hardware validation: NOT YET
Unity Library compatibility on macOS/Linux: NOT YET
Forced-termination recovery: NOT YET
Public testplay CLI or Image backend integration: NOT PRESENT
Production default readiness: NOT YET
```

This helper is included in v0.12.0 archives as an experimental integration
primitive. It does not add a seventh public `testplay` command, change
`testplay run`, or change the production default backend. The executable
remains `testplay-storage-helper` and accepts the
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
ChildPath. If MountPath was an existing empty directory, Release recreates an
empty directory with the recorded permission bits. It does not claim
restoration of the original directory object, inode, owner/group, ACLs,
extended attributes, filesystem flags, creation time, or exact timestamps.

Release verifies that MountPath is still the exact helper-owned link before
removing it. It refuses cleanup with `mount-ownership-lost` if another process
replaced or retargeted the link.

Immediately after cloning, the Unix helper records the Child root's device and
inode, generates a cryptographically random ownership token, and creates this
exclusive marker inside the cloned Unity Library:

```text
<ChildPath>/.testplay-storage-owner
```

The experimental schema-1 marker contains `schemaVersion`, `leaseId`, and
`ownerToken`, is created with exclusive-create semantics and mode `0600`, and
is reserved for this experimental helper contract. A Parent containing the exact
reserved marker name is rejected as `unsafe-source` so a clone cannot inherit
stale ownership evidence.

For `deleteChildOnRelease: true`, Release verifies the StoreRoot boundary, real
directory type, recorded device/inode, lease ID, token, regular non-symlink
marker, and bounded traversal. It then uses an atomic no-replace rename to a
random same-directory `.testplay-delete-*` quarantine path, repeats the
identity and marker checks there, and only then removes the quarantine tree.
Any ownership condition failure returns `child-ownership-lost` and preserves
the unproven object. Acquire failure cleanup uses the same rule; an observed
partial clone without a verified marker is preserved instead of being removed
by path alone. Release, Shutdown, and stdin EOF retain the same journal and
idempotency rules as Windows.

This narrows path-replacement races but does not claim to eliminate every
adversarial filesystem TOCTOU race around recursive deletion. In particular,
the final tree removal is still performed by the platform filesystem APIs after
quarantine revalidation.

## macOS implementation

macOS walks the immutable Parent and invokes `clonefileat(2)` for every regular
file with `CLONE_NOFOLLOW_ANY | CLONE_NOOWNERCOPY`. The helper calls the syscall
directly, so it does not inherit `/bin/cp -c`'s documented physical-copy
fallback. Directories are recreated with their permissions and timestamps. A
cross-filesystem or non-cloning filesystem error is returned as
`cow-unavailable`.

Local validation was run in one macOS arm64 APFS environment. It passed native
clone creation, Child mutation with Parent isolation, protocol Acquire/Release,
helper-owned link cleanup, recreation of an empty MountPath with its recorded
permission bits, and refusal to remove a retargeted mount.

## Linux implementation

Linux invokes GNU
`cp --archive --no-preserve=ownership --reflink=always --no-target-directory`
after the same symlink/special-file safety scan. `--reflink=always` is intentional:
unsupported filesystems and cross-filesystem layouts fail instead of consuming
the full logical Parent size. GNU coreutils `cp` is therefore a runtime
requirement for the Linux helper.

The subprocess environment fixes `LC_ALL=C` and `LANG=C`. Only missing GNU
`cp`, explicit operation-not-supported/function-not-implemented diagnostics,
and cross-device diagnostics map to `cow-unavailable`. Context cancellation or
deadline maps to `cancelled`. Permission, disk-full, I/O, source-read, and
unknown failures map to `child-create-failed`; unknown failures are not
presented as evidence that CoW is unavailable. A direct Linux `FICLONE`
implementation remains a possible follow-up, not part of this change.

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

Future cleanup candidate: rename `internal/vhdxstorage` to
`internal/cowstorage` or `internal/workspacestorage`. The current package name
is retained to keep this safety change narrowly scoped.
