# ADR 0004: Managed ReFS Library Pool clean-room probe

- Status: Proposed and implemented as a standalone probe
- Scope: experimental architecture probe, future v0.14.0 candidate
- Base: `v0.12.0` / `7c6f6783708d632b96f8db0ed1c19ed61e4664b4`

## Context

`v0.13.0` is an already released public result. It established the Image
lifecycle, Storage Helper protocol, differencing VHDX lifecycle, native CoW
provider integration, lease/cleanup behavior, reduced Library preparation
time, and the disk trade-off from duplicate representations. Those findings
remain valid historical evidence.

The Managed ReFS question is different: can Unity create one canonical Library
inside a managed ReFS volume and can each run derive an isolated writable
Library by ReFS Block Clone without first creating an Image representation?

Starting this experiment from v0.12 isolates that question from the released
Image integration. It also prevents experimental assumptions from being
mistaken for changes to v0.13. Public product lineage remains linear: a
successful result is re-integrated onto the latest `main`, reviewed against all
post-v0.13 changes, and developed as a future v0.14.0 candidate.

## Decision

Implement a standalone `cmd/testplay-refs-probe` and
`internal/refsworkspace` package with one dynamically expanding VHDX containing
one ReFS volume. Store each canonical baseline at
`baselines/<compatibility-key>/Library`. Store every worker at
`workers/<lease>/Library` on the same volume.

The baseline Library is the canonical payload. There is no Physical Directory
Image, parent VHDX, differencing child, per-worker VHDX, or Image/parent dual
representation in this architecture.

Use the VHDX for:

- private managed-volume installation and removal;
- host filesystem isolation;
- attach/detach lifecycle; and
- a maximum guest-volume virtual-size ceiling.

Use ReFS for:

- file extent sharing through Block Clone;
- allocate-on-write isolation; and
- allocation of worker metadata and changed clusters.

The distinction matters. VHDX does not provide the worker clone primitive, and
ReFS does not provide the installable single-file boundary. The VHDX maximum
is not a host-file allocation guarantee; testplay separately reserves the full
maximum plus overhead so maximum guest growth still preserves the host floor.

## Same-volume constraint

Block Clone is a byte-range file operation, not a directory snapshot API.
Source and destination must be on the same ReFS volume. Therefore the baseline
and every worker Library are inside the single managed volume. Source project
directories stay in normal shadow workspaces and are not ReFS-cloned.

Each file is pre-sized; clone ranges are cluster-aligned and below 4 GiB;
integrity and sparse settings are checked; unaligned tails are explicitly
copied and measured. A native failure does not select a whole-tree physical
copy fallback.

## Baseline safety

`LibraryBaselineStore` owns the compatibility key, creation lock, direct ReFS
staging destination, metadata, COMPLETE-last commit, full integrity hash,
read-only attributes, ACL boundary, active-use references, quarantine, and
clear. Unity never runs directly against a canonical baseline. Every Unity run
uses a worker clone.

Read-only attributes alone are not considered immutability. Verification and
active-use ownership remain authoritative even when an administrator can
override an ACL.

Protection is independently verified evidence. Metadata records a path-sorted
recursive security-descriptor digest for every directory and regular file,
including read-only and inheritance state. Windows reads descriptors with
`GetNamedSecurityInfo`. A byte-identical baseline with a changed root, child,
or file ACL, enabled inheritance, changed count, or writability is corrupt.

## Concurrency and cleanup decision

Reserve worker capacity under one process-safe pool lock. Inside that lock,
remeasure ReFS used bytes and host free bytes, validate journals and orphans,
sum reservations, and create the worker journal with `O_EXCL`. Release the lock
before baseline verification and cloning. Never accept a caller's used-byte
estimate and never silently break an abandoned lock.

Serialize baseline active-use creation and baseline mutation with a per-key
coordination lock and explicit mutation marker. Clear/quarantine checks active
uses before rename; acquire checks mutation and validity before marker
creation. Baseline rename and a new active marker cannot both win.

Worker release persists milestones: junction removed, worker quarantined,
ownership verified, worker deleted, active-use released, released, and lease
deleted. Retry is supported only in the same process through the same
`WorkerLease` object. New-process journal resume, forced-termination recovery,
and reboot recovery are not implemented; unexplained absence is an ownership
error.

Mounted cleanup uses a bounded context and joins primary and cleanup errors.
Uncertain detach, visibility, or ownership preserves the VHDX and emits manual
recovery evidence. Residual counts carry an explicit measured bit so unknown
cannot masquerade as zero.

## Sparse-file decision

Sparse Library files stay sparse. Each `FSCTL_QUERY_ALLOCATED_RANGES` page is
clipped to its query and file boundaries, sorted, and merged across overlap,
adjacency, and pagination duplicates. Overflow and no-progress responses fail.
The destination is sparse before sizing; only aligned extents are cloned,
holes remain unallocated, and only unaligned fragments are copied.

## Why allocation deltas are not zero

Block Clone shares existing extents, but worker directory entries, file
records, ReFS reference-count metadata, allocation alignment tails, and Unity
writes require physical allocation. Logical amplification is expected to grow
with worker count. The hypothesis is that physical amplification tracks
metadata and actual deltas rather than complete Library copies; exact zero
growth is not a requirement.

Dynamic VHDX host allocation is also distinct from ReFS internal free space.
Deleting workers does not imply automatic host-file shrink. Compact is a
future explicit maintenance operation and is never run while Unity is active.

## Consequences

Storage policy is an authority boundary: `WorkerRequest` contains no policy
fields, and workers receive a `PoolPolicy` only after host metadata, in-volume
metadata, and the mounted ReFS volume match in full. Fresh installation creates
missing parent segments from a canonical existing ancestor and rejects
intermediate symlinks/reparse points. Residual inspection uses exact artifact
allowlists; unknown and staging entries are explicit evidence. The binary does
not claim probe-process zero, leaving that measurement to the outer harness.

Benefits:

- one canonical Library representation;
- one managed volume instead of one VHDX per worker;
- explicit storage ceilings and reservations;
- a reusable Library Pool primitive for a later integration; and
- direct measurement of host, volume, and tree allocation layers.

Costs and risks:

- ReFS format support depends on Windows edition and policy;
- directory mounts require a suitable NTFS host path;
- native Block Clone has strict alignment, integrity, sparse, and same-volume
  requirements;
- ownership-safe recovery after forced termination is not complete; and
- Windows hardware and Unity correctness results are required before product
  integration.

## Integration rule

Do not merge the research branch wholesale merely because the standalone probe
passes. Check out the latest `main`, inspect post-v0.13 changes, and port the
core package and tests deliberately. Resolve architecture conflicts in the
latest structure. Public CLI integration such as
`testplay run --workspace-backend=refs` is a separate reviewable change.
