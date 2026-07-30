# Windows copy-on-write workspace strategy

## Decision

The Windows workspace candidates are ordered as follows:

```text
Differencing VHDX + future Storage Daemon
→ default Windows CoW candidate

Physical Copy
→ conservative fallback

ReFS Block Clone
→ experimental opt-in provider
```

## Rationale

Differencing VHDX is a Windows-native copy-on-write primitive with an explicit
immutable Parent and independently writable Child files. It does not require a
ReFS host volume and can be managed through the documented `VirtDisk.dll`
interface.

Physical Copy remains the correctness-first fallback. It must not be described
as copy-on-write because it duplicates the logical payload before mutation.

ReFS Block Clone remains an experimental provider for approved ReFS volumes.
Its evidence and implementation stay isolated from the VHDX probe.

## Current boundary

The standalone VHDX probe owns only creation, attach, verified virtual-disk
identity, test-only NTFS bootstrap, mount, isolation checks, detach, metrics,
and safe cleanup.

It does not integrate with:

- `LibraryMaterializer` or the Image backend
- the public `testplay` CLI
- Unity
- Windows Service installation
- crash recovery or orphan adoption
- parallel workers or test sharding
- ReFS code
- Honey Bee

## Promotion gate

Before a Storage Daemon PR begins, elevated Windows evidence must show:

- Parent isolation 5/5
- sibling Child isolation 5/5
- detach/reattach persistence 5/5
- cleanup 5/5
- zero residual mounts and attached VHDX devices
- measurable Child file growth from a small initial differencing file

Until then, the status is:

```text
IMPLEMENTED / AWAITING ELEVATED VALIDATION
```
