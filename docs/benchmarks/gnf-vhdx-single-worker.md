# GNF_ single-worker VHDX benchmark

## Status

```text
Harness: IMPLEMENTED
GNF_ correctness gate: NOT RUN
GNF_ cold run: NOT RUN
GNF_ warm 10: NOT RUN
Compatibility verdict: BLOCKED pending elevated hardware evidence
Performance verdict: NOT MEASURED
```

This opt-in benchmark compares the existing Legacy workspace, the existing
ImageStore with `PhysicalCopyMaterializer`, and the on-demand Differencing VHDX
Helper on one real GNF_ revision. It does not add a public CLI backend, change a
default, implement fallback, run parallel workers, or install a service.

## Fixed selection

The selection is not invented by this PR. It comes from the repository's
previous GNF_ validation in `docs/06_v0.2.0_beta_gnf_shadow_validation.md`,
where it passed twice on Unity `6000.3.8f1` and was loaded from
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
cache. The correctness gate is discarded before the Full cold phase so the
Legacy cold run creates a fresh cache; warm runs reuse it.

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

Specifying both switches or neither is an error. This code session did not run
either command. Until real elevated evidence exists, the only honest verdict is
`IMPLEMENTED — awaiting elevated GNF_ single-worker validation`.
