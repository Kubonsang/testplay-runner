# Unity Library on Differencing VHDX fixture

## Status

```text
Fixture and elevated harness: IMPLEMENTED
Elevated Unity VHDX hardware validation: ATTEMPT 2 FAILED BEFORE PHYSICAL UNITY
Verdict: FIXED / AWAITING HARDWARE REVALIDATION
```

This fixture answers one narrow question: can a small Unity project's
`Library` remain correct while it is a directory mount backed by a
Differencing VHDX Child? It does not connect the helper to the public
`testplay` CLI, Image Backend, GNF_, parallel workers, sharding, or a Windows
service.

## Fixture

The repository fixture is `testdata/unity-vhdx-fixture` and targets Unity
`6000.3.8f1`. It contains only `Assets`, `Packages`, and `ProjectSettings` plus
an exclusion file. Generated `Library`, `Temp`, `Logs`, `UserSettings`, and
`obj` directories are forbidden.

Tests:

- EditMode `LibraryMountWriteReadTest` writes and reads the unique run marker
  under `Library/TestPlayVHDX`.
- EditMode `DeterministicRuntimeStateTest` exercises the shared Runtime
  assembly.
- PlayMode `DeterministicPlayModeSmokeTest` creates a GameObject, attaches the
  shared Runtime component, waits a frame, verifies deterministic state, and
  destroys the object.

## Lifecycle

```text
copy Seed project
-> create and initialize a dynamic 4 GiB Parent VHDX
-> mount Parent at Seed/Library
-> Unity BatchMode import and compile
-> physical-copy the warm Library baseline
-> unmount and detach Parent
-> hash Parent
-> run Physical EditMode and PlayMode
-> start the real testplay-storage-helper process
-> Acquire one Child mounted at VHDXProject/Library
-> run VHDX EditMode and PlayMode under the same Lease
-> compare canonical semantic results
-> Release and verify released Journal
-> verify Parent hash and marker isolation
-> verify no new disk, mount, Child, or non-released Journal
```

The Seed, Physical, and VHDX projects use different absolute paths. Path-driven
reimport is an observation, not an automatic compatibility failure, provided
semantic parity, Parent isolation, mount integrity, and cleanup all pass.

## Result parity

The harness reuses `internal/parser` for Unity NUnit XML. It sorts test cases
by full name and outcome, then hashes only:

- Unity exit code and test platform
- total, passed, failed, skipped, and inconclusive counts
- every full test name and outcome

Duration, timestamp, file path, stack text, and XML ordering are excluded.
Tests are never dropped or renamed to make parity pass.

## Storage boundaries

- Parent creation and mount use `internal/vhdxstorage`.
- The Physical baseline uses a fixture-only contents copy boundary. A verified
  Windows volume-mount root is dereferenced once, while nested reparse points
  are rejected. The resulting baseline and Physical project `Library` must be
  ordinary directories with an accessible `SourceAssetDB` before Unity starts.
- The VHDX run launches the actual `testplay-storage-helper` executable and
  uses its versioned NDJSON protocol; it does not call the Backend directly.
- The Child handle remains owned by the Helper across both Unity platforms.
- Mount identity is inspected after Acquire, after each platform, immediately
  before Release, and after Release.
- The Parent is hashed before and after Child use and inspected read-only for
  the Child marker.

## Evidence

Each hardware run writes a unique directory below
`TESTPLAY_UNITY_VHDX_ARTIFACT_ROOT` containing `evidence.json`, raw Physical and
VHDX results XML and Editor logs, Helper protocol/stderr, and mount snapshots.
Large raw artifacts are not committed automatically.

### Hardware attempt 1

The preserved user-recorded evidence is:

```text
C:\Dev\testplay-unity-vhdx-evidence\
unity-vhdx-20260731T160449.463178300Z-01
```

Attempt 1 failed during Physical EditMode, before Helper Acquire or any VHDX
Unity run. The Seed `Library` was a Windows directory mount, but the former
physical-copy path reproduced that root as a reparse link. Detaching the Parent
and deleting the Seed project left the Physical project's `Library` dangling,
so Unity could not open `Library/SourceAssetDB` and crashed in LMDB
initialization (`0xC0000005`). Licensing initialized successfully and was not
the cause. Attached virtual-disk cleanup passed; VHDX Unity compatibility
remains **NOT RUN**.

### Hardware attempt 2

The preserved failure root is `C:\Dev\testplay-unity-vhdx-fixture`.

Attempt 2 confirmed that mounted-Library contents were copied into an ordinary
Physical directory and that the File Backed Virtual disk difference remained
zero. Validation stopped before Physical Unity execution because the validator
incorrectly required `SourceAssetDB` to be a directory. Unity 6000.3.8f1
actually generated it as a 2 MiB regular LMDB data file, alongside an observed
8 KiB `SourceAssetDB-lock`. `ScriptAssemblies/TestPlayFixture.Runtime.dll` was
also present as a regular file.

The corrected contract requires a readable, nonempty, non-reparse
`SourceAssetDB` regular file plus an ordinary `ScriptAssemblies` directory and
readable `TestPlayFixture.Runtime.dll`. `SourceAssetDB-lock` is observed but is
not a seed-completion requirement; when present it must be a regular,
non-reparse file. Physical Unity, Helper Acquire, and VHDX Unity remained
**NOT RUN** in Attempt 2.

### Hardware attempt 3

The elevated one-run Unity/VHDX integration lifecycle completed successfully:

```text
--- PASS: TestUnityVHDXLibraryFixture (55.75s)
PASS
```

The PowerShell Runner then failed while producing its final JSON. Cleanup had
correctly produced an empty residual array, but StrictMode rejected direct
property enumeration through `$residualFixtureItems.FullName`. The lifecycle
evidence is a PASS; the Runner's final reporting is a post-test FAIL. A
StrictMode runtime self-check now exercises both an empty residual collection
and a non-empty collection through the same final-report function used by the
Runner. The one-run final JSON and five-run hardware validations still require
an elevated rerun after this reporting fix.

Only measured values are emitted. Missing values remain absent. No performance
threshold is applied in this PR; unexplained phases over 30 seconds must be
reported as observations rather than assigned a cause.

## Elevated validation

Run only from an Administrator PowerShell with an absent or empty dedicated
Fixture Root:

```powershell
cd C:\Dev\testplay-runner

$env:TESTPLAY_UNITY_EDITOR_PATH = `
  "C:\Program Files\Unity\Hub\Editor\6000.3.8f1\Editor\Unity.exe"
$env:TESTPLAY_UNITY_VHDX_FIXTURE_ROOT = `
  "C:\Dev\testplay-unity-vhdx-fixture"
$env:TESTPLAY_UNITY_VHDX_ARTIFACT_ROOT = `
  "C:\Dev\testplay-unity-vhdx-evidence"

powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\run-unity-vhdx-fixture.ps1 `
  -Count 1
```

Only after the one-run lifecycle succeeds completely:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\run-unity-vhdx-fixture.ps1 `
  -Count 5 `
  -ReuseParent
```

The five-run mode prepares one immutable Parent and uses five distinct
sequential Children. The script records existing File Backed Virtual disks but
never detaches them or modifies physical disks.

## Remaining gates

Hardware evidence is required before writing `PROVEN`. Even a successful
fixture does not prove GNF_ compatibility, large-project performance,
forced-termination recovery, parallel workers, or production latency.
