# GNF_ single-worker VHDX benchmark

## Status

```text
Harness: IMPLEMENTED
GNF_ correctness gate: PASS (Legacy/Physical/VHDX semantic parity)
GNF_ cold run: PASS (1/backend)
GNF_ warm 10: PASS (10/backend)
Compatibility verdict: COMPATIBLE (single-worker fixed selection)
Performance verdict: BENEFICIAL (single-worker fixed selection)
Production default readiness: NOT YET
```

This opt-in benchmark compares the existing Legacy workspace, the existing
ImageStore with `PhysicalCopyMaterializer`, and the on-demand Differencing VHDX
Helper on one real GNF_ revision. It does not add a public CLI backend, change a
default, implement fallback, run parallel workers, or install a service.

## Fixed selection

The selection identifier comes from the repository's previous GNF_ validation
in `docs/06_v0.2.0_beta_gnf_shadow_validation.md`. The current GNF_ checkout did
not contain that Mac-authored test, so the Windows validation source uses a
separate clean local GNF_ commit containing a minimal deterministic replacement.
It passed directly on Unity `6000.3.8f1` and is loaded from
`GNF.Tests.PlayMode.dll`:

```text
Platform: play_mode
Filter: CodexMovementSmokeTest.TestPlayer_MovesRight_InPlayMode
Expected count: 1
Expected assembly sentinel: Library/ScriptAssemblies/GNF.Tests.PlayMode.dll
```

Every Backend must return that exact full name, outcome, count, and canonical
semantic digest. A missing or changed selection stops the benchmark.

## Inputs and preflight

The Runner accepts only environment variables; personal paths are not compiled
into the repository:

```text
TESTPLAY_UNITY_EDITOR_PATH
TESTPLAY_GNF_PROJECT_PATH
TESTPLAY_GNF_WORK_ROOT
TESTPLAY_GNF_ARTIFACT_ROOT
TESTPLAY_GNF_PARENT_VHDX_SIZE_GIB (optional; default 16)
```

Preflight requires Administrator PowerShell, Unity and project version
`6000.3.8f1`, a clean Git GNF_ source, a resolvable revision, the fixed test in
`Assets`, an absent or empty dedicated Work Root, non-overlapping roots,
sufficient free space, and a snapshot of existing File Backed Virtual disks
and Unity/Helper processes. The original GNF_ project is hashed over `Assets`,
`Packages`, and `ProjectSettings` and is never used as a writable Unity project.
The Runner gives Smoke a two-hour Go test deadline and Full a 24-hour deadline;
these are harness safety ceilings, not performance targets.

## Common seed

```text
copy read-only GNF_ inputs to a benchmark-owned source project
-> copy a separate Seed project
-> create a dynamic Parent VHDX
-> initialize GPT/NTFS and mount at Seed/Library
-> Unity import/compile
-> require SourceAssetDB and GNF.Tests.PlayMode.dll
-> copy mounted root contents to an ordinary Physical baseline
-> detach and hash the Parent
-> create one immutable ImageStore image from that same baseline
```

The shared `internal/mountedcopy` boundary dereferences only a verified Windows
volume-mount root. It rejects nested reparse points, overlapping roots, an
existing destination, and path escape. The production
`PhysicalCopyMaterializer` link policy remains unchanged. Small-Fixture and
GNF_-specific warm-Library Sentinels remain in their respective packages.

## Backend lifecycle

Legacy uses the existing Shadow workspace and its dedicated external Library
cache. The benchmark opts into a physical per-run `Packages` copy so Unity
cannot update `packages-lock.json` through the legacy Packages link. The
correctness gate is discarded before the Full cold phase so the Legacy cold
run creates a fresh cache; warm runs reuse it.

Physical Image reuses one immutable `libraryimage.Store` image. Each run creates
a new Shadow workspace, verifies the Image, and invokes the real
`PhysicalCopyMaterializer` into a new writable `Library`. The Image is verified
again before every materialization.

VHDX copies a new project shell, starts the actual `testplay-storage-helper`,
uses the versioned NDJSON Hello/Acquire/Release/Shutdown protocol, and keeps the
Helper and Lease alive through Unity. Each run uses a new Child, Lease, mount,
and marker. The harness checks mount identity at ready, immediately before
Unity, after Unity, immediately before Release, and after Release. Volume GUID,
Disk Number, Child, Lease, mount, and Journal identity are recorded. A Volume
serial is omitted when the platform inspection does not provide one; it is not
fabricated.

Each VHDX run proves the current marker exists only in the current Child,
rejects previous markers, verifies the Parent marker is absent, and compares
the complete Parent SHA-256 before and after read-only inspection.

## Plan

`-Smoke` runs only the sequential correctness gate:

```text
Legacy 1 -> Physical Image 1 -> VHDX 1
```

`-Full` first repeats that gate, resets gate-only Legacy state, then runs one
cold round and ten warm rounds. Warm order rotates:

```text
1: Legacy -> Physical -> VHDX
2: Physical -> VHDX -> Legacy
3: VHDX -> Legacy -> Physical
... repeated through round 10
```

Concurrency is always one. The first passing Legacy gate result is the semantic
reference. Any failure or mismatch stops later runs.

## Evidence and metrics

Each session writes `manifest.json`, `plan.json`, `summary.json`, `summary.md`,
`runs.csv`, Seed logs, and a directory per Backend/run. Run directories contain
`evidence.json`, NUnit XML, Editor log, stdout/stderr, and—for VHDX—Helper
protocol/stderr and mount identities. Raw artifacts are never committed
automatically.

Only measured metrics are emitted. Unavailable fields are omitted in JSON and
written as `unavailable` in CSV. Metrics separate workspace preparation, Unity,
cleanup, logical/allocated bytes, Legacy cache write-back, Image validation and
materialization, and Helper Child/attach/mount/detach phases. A measured phase
at or above 30 seconds is disclosed as an outlier without assigning a cause.

Warm ten-run statistics include mean, median, minimum, maximum, nearest-rank
P95, and population standard deviation for Total Wall Clock and Unity time.
Percentage comparisons can be calculated only from nonzero references.
Performance classification is explicit: VHDX median below both alternatives is
`BENEFICIAL`, above both is `REGRESSION`, and between them is `NEUTRAL`.

## Cleanup gate

Success requires no new File Backed Virtual disk, directory mount, Child VHDX,
active Lease, non-released Journal, Unity/Helper process, or temporary
workspace. Existing disks and processes are compared, never detached or
terminated. The benchmark-owned Work Root is removed only after complete
success; failure evidence is preserved for diagnosis.

## Elevated execution

### Hardware attempt 1

The first elevated Smoke attempt on 2026-08-02 passed preflight and Common Seed
preparation. The Legacy NUnit result was `1/1 PASS` with the exact selected test,
but the result arrived roughly 10 minutes 23 seconds after session creation. Go's
default 10-minute package timeout terminated the harness before the Unity child
returned, so Physical Image and VHDX were not run. A second reporting error came
from recursively enumerating the preserved Unity Work Root. No Unity/Helper
process or File Backed Virtual disk remained when inspected. The Work Root and
raw Evidence were preserved.

The Runner now uses explicit mode-specific timeouts and reports only top-level
Work Root residuals. Attempt 1 is not a completed correctness gate.

### Hardware attempt 2

The second elevated Smoke attempt confirmed the explicit two-hour deadline and
completed Legacy normally in 599.03 seconds. The selected test was `1/1 PASS`,
its semantic digest was recorded, Legacy cleanup passed, and all structured
Legacy residual counts were zero. Before Physical Image could start, the source
contamination gate found one changed file in the benchmark-owned source copy:
`Packages/packages-lock.json`. Unity resolved Burst `1.8.28` to `1.8.27` and
Splines `2.8.3` to `2.8.2` through the Legacy Packages directory link.

The original clean GNF_ input remained unchanged. Physical Image and VHDX were
not run. Post-attempt inspection found no Unity/Helper process or File Backed
Virtual disk. The Work Root and raw Evidence were preserved. The Legacy path now
uses the existing `CopyPackages` isolation option; the general Legacy product
default is unchanged. Attempt 2 is not a completed correctness gate.

### Hardware attempt 3

The third elevated Smoke attempt passed both Legacy and Physical Image with the
same selected test and semantic digest. Both Backends reported cleanup success
and zero structured residual counts. VHDX project preparation and Helper Hello
succeeded, but Acquire was rejected before Child creation because the Harness
had not created the benchmark-owned `storage/children` directory required by
the Helper path-safety contract.

The structured Helper error was `invalid-child-path` at `stat-parent`. No Child
was created or attached, and post-attempt disk/process differences were zero.
The Work Root and raw Evidence were preserved. The Harness now creates only the
owned Child parent directory before Acquire; the Helper remains solely
responsible for creating the Child VHDX. Attempt 3 is not a completed
correctness gate.

### Hardware attempt 4

The fourth elevated Smoke attempt completed the full sequential correctness
gate on Unity `6000.3.8f1` and GNF_ revision
`1ed151ca3f29e587ad92d2f89f102fa1e50641ef`. Legacy, Physical Image, and VHDX
each passed the exact selected test with semantic digest
`20e4343f5e384b1cc13a0ca98a4be26dc76f83ec0d4d821dbc9d1d8885fcdc39`.

| Backend | Result | Total wall clock | Unity wall clock | Cleanup |
|---|---:|---:|---:|---:|
| Legacy | 1/1 PASS | 274,857 ms | 228,942 ms | PASS |
| Physical Image | 1/1 PASS | 126,392 ms | 66,691 ms | PASS |
| Differencing VHDX | 1/1 PASS | 69,017 ms | 43,089 ms | PASS |

The VHDX run passed Parent isolation and Mount integrity. One Child Lease stayed
on Disk 1 and the same Volume GUID through ready, before Unity, after Unity, and
before Release; the released snapshot no longer existed. Acquire took 2,299 ms,
Release took 2,015 ms, and the Child grew from 37,748,736 logical/allocated
bytes at ready to 406,847,488 bytes after Unity. These Smoke timings are
observations, not a production performance verdict.

Parent virtual size was 17,179,869,184 bytes and its detached file was
5,070,913,536 logical/allocated bytes. Parent isolation remained true. The
Helper returned a released Journal, deleted the Child, detached the disk, and
left zero residual disk, mount, Child, Journal, process, or Work Root. Final
Runner JSON reported `success: true` and `testExitCode: 0`.

Raw Evidence is preserved under session
`session-20260801T180750.829124000Z`. Correctness is `COMPATIBLE`; Cold 1 and
Warm 10 remain unexecuted, so performance remains `NOT MEASURED`.

### Hardware attempt 5 — Full PASS

Elevated Administrator PowerShell completed the Full plan on Unity `6000.3.8f1`
and GNF_ revision `1ed151ca3f29e587ad92d2f89f102fa1e50641ef` with concurrency
one. The test body passed in 4,757.46 seconds. Final Runner JSON reported
`success: true` and `testExitCode: 0`, with no disk or process difference and no
residual Work Root items.

The Full session contains 36 sequential Backend executions: a three-Backend
correctness gate, one cold execution per Backend, and ten warm executions per
Backend. Every execution passed the one fixed test and produced semantic digest
`20e4343f5e384b1cc13a0ca98a4be26dc76f83ec0d4d821dbc9d1d8885fcdc39`.

| Gate | Result |
|---|---:|
| Correctness gate | 3/3 PASS |
| Cold | 3/3 PASS |
| Warm Legacy | 10/10 PASS |
| Warm Physical Image | 10/10 PASS |
| Warm Differencing VHDX | 10/10 PASS |
| Semantic parity | 36/36 PASS |
| Cleanup | 36/36 PASS |
| Structured residual disk/mount/Child/Journal | 0 |

The 12 VHDX executions used 12 distinct Child paths and 12 distinct Lease IDs.
Parent isolation and mount integrity passed 12/12. Each released mount snapshot
was absent, every Journal reached Release, and no Child, mount, File Backed
Virtual disk, Unity/Helper process, or Work Root remained.

Warm Total Wall Clock statistics, in milliseconds:

| Backend | Mean | Median | Min | Max | P95 | Population SD |
|---|---:|---:|---:|---:|---:|---:|
| Legacy | 134,403.8 | 127,170.5 | 112,402 | 156,364 | 156,364 | 14,339.84 |
| Physical Image | 103,345.1 | 109,651.0 | 83,024 | 114,565 | 114,565 | 10,810.59 |
| Differencing VHDX | 71,013.5 | 71,682.5 | 66,859 | 75,274 | 75,274 | 2,728.16 |

Warm Unity Wall Clock statistics, in milliseconds:

| Backend | Mean | Median | Min | Max | P95 | Population SD |
|---|---:|---:|---:|---:|---:|---:|
| Legacy | 47,763.2 | 45,280.5 | 39,785 | 57,322 | 57,322 | 6,015.50 |
| Physical Image | 51,588.9 | 54,015.0 | 41,185 | 57,825 | 57,825 | 5,531.57 |
| Differencing VHDX | 45,469.0 | 45,788.5 | 42,901 | 48,672 | 48,672 | 1,714.81 |

The Evidence classifier reports `BENEFICIAL` because the VHDX warm Total Wall
Clock median is below both alternatives. The Unity-only medians are much closer:
VHDX is slightly above Legacy and below Physical Image. The observed total
advantage therefore must not be presented as a general Unity execution-speed
claim. It applies only to this deterministic PlayMode selection, this revision,
one machine, and concurrency one.

The Parent was a 17,179,869,184-byte dynamic VHDX whose detached logical and
allocated file size was 5,171,576,832 bytes. Each VHDX Child began at 37,748,736
logical/allocated bytes; per-run post-Unity sizes are retained in raw Evidence.
No aggregate storage statistic is invented where the generated summary does not
provide one.

Raw Evidence is preserved under session
`session-20260802T102851.901425300Z`. The source revision remained clean and the
benchmark-owned Work Root was removed after complete success.

```powershell
cd C:\Dev\testplay-runner

$env:TESTPLAY_UNITY_EDITOR_PATH = `
  "C:\Program Files\Unity\Hub\Editor\6000.3.8f1\Editor\Unity.exe"
$env:TESTPLAY_GNF_PROJECT_PATH = "<ACTUAL_GNF_PROJECT_PATH>"
$env:TESTPLAY_GNF_WORK_ROOT = "C:\Dev\testplay-gnf-vhdx-benchmark"
$env:TESTPLAY_GNF_ARTIFACT_ROOT = "C:\Dev\testplay-gnf-vhdx-evidence"

powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\run-gnf-vhdx-benchmark.ps1 `
  -Smoke
```

Only after Smoke passes completely:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\run-gnf-vhdx-benchmark.ps1 `
  -Full
```

Specifying both switches or neither is an error. The Smoke correctness gate is
`COMPATIBLE`. The completed Full run classifies this fixed single-worker
selection as `BENEFICIAL`. This does not establish a production default, forced
termination recovery, parallel-worker behavior, sharding, or general GNF_
performance.
