# Beta Smoke Evidence

> Status: DONE

Records that `fastplay run` was executed against a real Unity project before the
beta release. This file captures the actual local smoke evidence used for the
`v0.1.0-beta` release gate.

## Environment

| Field | Value |
|---|---|
| Date | 2026-03-27 |
| Tester | gubonsang (Codex-assisted) |
| fastplay version | `v0.1.0-beta` |
| Unity version | `6000.3.8f1` |
| OS | `macOS 26.3 arm64` |
| Command | `env UNITY_PATH=/Applications/Unity/Hub/Editor/6000.3.8f1/Unity.app/Contents/MacOS/Unity SMOKE_DIR=/tmp/fastplay-smoke-editfix.3OR4vV ./scripts/smoke.sh` |
| Smoke project | `/tmp/fastplay-smoke-editfix.3OR4vV` (copy of `fixtures/smoke-project`) |

## Run Records

| Platform | Run ID | Filter | Exit Code | Total | Passed | Failed |
|---|---|---|---:|---:|---:|---:|
| `edit_mode` | `20260327-132107` | _(none)_ | 0 | 1 | 1 | 0 |
| `play_mode` | `20260327-132119` | _(none)_ | 0 | 1 | 1 | 0 |

## Artifacts Produced

Verified under `.fastplay/runs/<run_id>/` for both runs:

```
<run_id>/
  results.xml      - present
  summary.json     - present
  manifest.json    - present
  stdout.log       - present
  stderr.log       - present
  events.ndjson    - present
```

Verified in the smoke project root:

```
fastplay-status.json - present (phase: done)
```

Final status snapshot after the PlayMode run:

```json
{
  "schema_version": "1",
  "phase": "done",
  "run_id": "20260327-132119",
  "total": 1,
  "passed": 1,
  "exit_code": 0
}
```

## Notes

- The smoke run was executed against a `/tmp` copy of `fixtures/smoke-project`
  to avoid mutating the tracked fixture while validating Unity `6000.3.8f1`.
- `fixtures/smoke-project/Assets/Tests/EditMode/FastPlaySmoke.EditMode.asmdef`
  was updated to include `"Editor"` in `includePlatforms`, which restored proper
  EditMode-only discovery under Unity `6000.3.8f1`.
- A sandboxed attempt failed with Licensing Client and Package Manager IPC
  restrictions. The successful evidence above was captured with an unsandboxed
  local run on the maintainer machine.
