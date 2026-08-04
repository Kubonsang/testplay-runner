# Managed ReFS Library Pool benchmark record

Status: `NOT MEASURED` on Windows native hardware.

This file is an evidence template, not a performance claim. The implementation
was built from `v0.12.0` as an independent architecture probe. It does not
change or invalidate the released v0.13 Image + native CoW benchmark history.

## Environment

| Field | Result |
|---|---|
| Windows edition/build | NOT MEASURED |
| ReFS format support | NOT MEASURED |
| Unity Editor | NOT MEASURED |
| Project identity | NOT MEASURED |
| VHDX maximum | NOT MEASURED |
| soft budget / reserve | NOT MEASURED |
| ReFS cluster size | NOT MEASURED |
| host filesystem | NOT MEASURED |

Required variables:

```text
TESTPLAY_REFS_PROJECT_PATH
TESTPLAY_REFS_UNITY_EDITOR_PATH
TESTPLAY_REFS_POOL_FILE
TESTPLAY_REFS_MOUNT_ROOT
TESTPLAY_REFS_ARTIFACT_ROOT
TESTPLAY_REFS_MAX_BYTES
```

## Correctness gate

| Check | Legacy | Managed ReFS probe |
|---|---:|---:|
| exit code | NOT MEASURED | NOT MEASURED |
| total/passed/failed/skipped | NOT MEASURED | NOT MEASURED |
| test name/result pairs | NOT MEASURED | NOT MEASURED |
| compile errors | NOT MEASURED | NOT MEASURED |
| source hash unchanged | NOT MEASURED | NOT MEASURED |
| baseline hash unchanged | n/a | NOT MEASURED |
| worker A/B isolation | n/a | NOT MEASURED |
| cleanup residual zero | NOT MEASURED | NOT MEASURED |

## Parallel ladder

Run in order and stop at the first correctness or cleanup failure.

| Workers | Clone batch wall | Worker ready wall | Unity ready wall | Baseline allocated | Worker logical | ReFS used delta | Host VHDX allocated delta | Cleanup residual |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| 2 | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| 4 | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| 8 | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |

Calculations:

```text
logical amplification
= all worker Library logical bytes / baseline Library logical bytes

physical amplification
= ReFS used-byte increase / baseline Library allocated bytes
```

Expected behavior is not zero physical growth. Directory and file records,
reference-count metadata, alignment tails, and Unity modifications consume
space. The experiment succeeds only if measured growth follows metadata and
actual changed data rather than full baseline duplication.

## Three-layer storage record

### Host filesystem

| Metric | Before | After baseline | After acquire | After Unity | After release | After explicit compact |
|---|---:|---:|---:|---:|---:|---:|
| VHDX logical bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| VHDX allocated bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| host free bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |

Automatic compact is not part of the probe. Any future explicit compact result
must be recorded separately and only while no worker is active.

### ReFS volume

| Metric | Before | After baseline | After acquire | After Unity | After release |
|---|---:|---:|---:|---:|---:|
| used bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| free bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |

### Library trees

| Metric | Baseline | Worker ready | Worker after Unity | Worker released |
|---|---:|---:|---:|---:|
| logical bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| allocated bytes | NOT MEASURED | NOT MEASURED | NOT MEASURED | NOT MEASURED |
| cloned bytes | n/a | NOT MEASURED | n/a | n/a |
| physical tail bytes | n/a | NOT MEASURED | n/a | n/a |

## Native result JSON

Do not populate PASS unless the commands and Unity gate actually ran on the
recorded Windows machine.

```json
{
  "status": "NOT MEASURED",
  "filesystem": "NOT MEASURED",
  "blockCloneSupported": false,
  "physicalImageCreated": false,
  "differencingChildCreated": false,
  "sourceUnchanged": false,
  "baselineUnchanged": false,
  "semanticEquivalent": false,
  "residual": {
    "activeLeases": 0,
    "workerLibraries": 0,
    "junctions": 0,
    "attachedDisks": 0,
    "probeProcesses": 0
  }
}
```

## Verdict

`NOT MEASURED`

The standalone source and cross-platform tests are implementation evidence;
they are not native ReFS or Unity benchmark evidence.
