# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

FastPlay Runner (`fastplay`) is a thin Go CLI wrapper around Unity's test runner. It solves eight specific problems that make Unity's raw CLI unusable for AI agents: unreliable exit codes, XML-only results, ambiguous compile vs. test failures, no progress visibility, no pre-validation, and platform path differences.

Agents interact via four commands: `check`, `list`, `run`, `result`. All stdout is JSON; all human-readable logs go to stderr.

**Supported test platforms:** `"edit_mode"` (default) and `"play_mode"` — set via `test_platform` in `fastplay.json`. The platform is passed as `-testPlatform EditMode|PlayMode` to Unity.

**Ultimate goal:** PlayMode + network environment testing.

## Build & Run

```bash
# Build for current platform
go build ./cmd/fastplay

# Cross-compile
GOOS=darwin  GOARCH=amd64 go build -o fastplay       ./cmd/fastplay
GOOS=windows GOARCH=amd64 go build -o fastplay.exe   ./cmd/fastplay

# Run tests
go test ./...

# Run a single package's tests
go test ./internal/parser/...
```

External dependencies are kept minimal — `cobra` for CLI parsing, everything else uses stdlib.

## Package Structure

```
cmd/fastplay/        # CLI entry points for check, list, run, result subcommands
internal/
  unity/             # Unity process execution and path discovery
  parser/            # NUnit XML → Go struct → JSON conversion
  status/            # Atomic fastplay-status.json updates during run
  history/           # Result file persistence and history queries
```

## CLI Contract (stdout = JSON only)

Every command outputs a single JSON object to stdout with a `schema_version` field. Nothing else ever goes to stdout.

| Command | Purpose |
|---|---|
| `fastplay check` | Validate Unity path, project path, and fastplay.json before running |
| `fastplay list` | Static source scan returning candidate test names (not guaranteed complete) |
| `fastplay run [--filter <name>] [--category <cat>] [--compare-run <run_id>]` | Execute tests; streams progress to `fastplay-status.json` |
| `fastplay result [--last N]` | Re-read stored results; returns run_id history |

## Exit Code Semantics

| Code | Meaning | Agent action | Implemented |
|---|---|---|---|
| 0 | All tests passed | Proceed | ✅ |
| 1 | Dependency error (Unity/project not found) | Fix env, check `hint` field | ✅ |
| 2 | Compile failure | Fix source code, see `errors[].absolute_path` + `line` | ✅ |
| 3 | Test failure | Fix test logic, see `tests[].absolute_path` + `line` | ✅ |
| 4 | Timeout **or** interrupted by signal | Check `timeout_type` — `"compile"`, `"test"`, or `"total"`; absent means signal | ✅ |
| 5 | Config error (fastplay.json missing/invalid) | Fix config file | ✅ |
| 6 | Build failure (missing build target, license) | Fix build environment | ❌ not yet returned |
| 7 | Permission error | Fix path/permissions | ❌ not yet returned |
| 8 | Interrupted by signal | Retry without code changes | ❌ signal currently returns exit 4 |

**timeout_type values for exit 4:**
- `"compile"` — compile-only phase exceeded `compile_ms` deadline (two-phase mode)
- `"test"` — test phase exceeded `test_ms` deadline (two-phase mode)
- `"total"` — outer `total_ms` deadline expired (either phase)
- *(absent)* — SIGINT/SIGTERM signal interruption

**Signal behavior:** SIGINT/SIGTERM cancels the context (`cancel()`) → executor sees `context.Canceled` → returns exit 4 with no `timeout_type`. Exit 8 is not yet implemented.

## fastplay.json (project config)

```json
{
  "schema_version": "1",
  "unity_path": "/Applications/Unity/Hub/Editor/2022.3.0f1/Unity.app/Contents/MacOS/Unity",
  "project_path": "/Users/user/MyProject",
  "test_platform": "edit_mode",
  "timeout": {
    "total_ms": 300000
  },
  "result_dir": ".fastplay/results"
}
```

`test_platform` accepts `"edit_mode"` (default) or `"play_mode"`.

`unity_path` falls back to `UNITY_PATH` env var if omitted. `project_path` defaults to the directory containing `fastplay.json`.

**Two-phase execution:** when both `compile_ms` and `test_ms` are set (both > 0), two-phase execution is enabled. Both fields must be set together — setting only one is a validation error. When neither is set, single-phase execution uses only `total_ms`.

**Config path:** Always loaded from `"fastplay.json"` in cwd. No `--config` flag yet — working directory must contain `fastplay.json`.

## Runtime Files

- `fastplay-status.json` — written atomically during `run`; poll this to observe progress.
  - **Path:** hardcoded to `"fastplay-status.json"` in cwd. No config option.
  - **Phase values actually emitted:** `compiling → running → done | timeout_compile | timeout_test | timeout_total | interrupted`
  - `waiting` — defined but never written by the runner (pre-run initial state)
  - `timeout_compile`, `timeout_test` — written in two-phase mode when the respective phase deadline fires
  - `running` — written *after* Unity exits, not when tests actually start (phase detection is approximate)
  - `interrupted` — best-effort write on SIGINT/SIGTERM before context cancel; process still exits 4
- `.fastplay/results/<run_id>.json` — one file per run, never overwritten. `run_id` is a 1-second-granularity timestamp (e.g. `20250301-102200`); concurrent runs within the same second will collide.

## Agent Recommended Usage Flow

Standard flow for agents using FastPlay Runner:

**Step 1 — check**
Run `fastplay check` to validate the environment. If `ready: false`, fix the environment per the `hint` field and re-check.

**Step 2 — list**
Run `fastplay list` to get candidate test names. Collect names related to modified code as `--filter` candidates. Assume the list may be incomplete.

**Step 3 — run**
Run `fastplay run`. Use `--compare-run` to enable regression analysis. Poll `fastplay-status.json` to track progress.

**Step 4 — Evaluate result**
Branch on exit code. If exit 4, sub-branch on `timeout_type`. If exit 3 with `--compare-run` specified, check `new_failures` to determine regression.

**Step 5 — Fix or finish**
If exit 2, go to `errors[].absolute_path` + `line` to fix source. If exit 3, go directly to the failing test's `absolute_path`.

**Step 6 — result**
Run `fastplay result` to review the `run_id` list and decide the `--compare-run` value for the next run.

> `check → list → run → result` — this four-step flow is the agent's entire interface. If this flow breaks, the project breaks.

## Output Design Rules

1. **stdout = JSON only.** No banners, progress lines, or mixed output ever.
2. **stderr = human logs.** Agents may ignore stderr entirely.
3. Every JSON response includes `schema_version`.
4. All file path fields include both `file` (relative) and `absolute_path`.
5. `hint` field is included only on exit 1 — the one case where an agent can auto-recover.
6. `new_failures` in exit 3 is only populated when `--compare-run` is specified; otherwise `null`.
7. `warnings` (string array) is included only when non-fatal infrastructure issues occur (e.g. result save failed, summary write failed). Absent when no warnings.

## Known Limitations & Risks

| Area | Issue | Severity |
|---|---|---|
| `list` scanner | Detects `[Test]` and `[UnityTest]` but misses other attributes (`[TestCase]`, `[Theory]`) — list output may be incomplete | Low |
| Phase detection | `running` phase written after Unity exits, not when tests start — polling agents see misleading phase | Medium |
| runID collision | 1-second timestamp granularity; concurrent runs within the same second overwrite the result file | Medium |
| Signal exit code | SIGINT/SIGTERM returns exit 4 (timeout), not exit 8 (interrupted) — agents cannot distinguish | Medium |
| Timeout sub-types | `compile_ms`/`test_ms` enforced in two-phase mode; single-phase uses only `total_ms` | — resolved |
| Config path | Always loads `fastplay.json` from cwd; no `--config` flag — agents must `cd` to project root | Low |
| Unimplemented exit codes | Exit 6 (build failure), exit 7 (permission) are documented but never returned | Low |
| Shadow — concurrent run safety | Two simultaneous `fastplay run` invocations against the same project share a single `.fastplay-shadow/` directory. `Prepare` re-copies `Assets/` and `ProjectSettings/` and deletes `Temp/` on every call; a second run starting while the first is executing will overwrite those directories mid-flight. Running `fastplay run` in parallel against the same project path is **not currently safe** in shadow mode. | Medium |
| Shadow — `Packages/` not fully isolated | `Packages/` is linked (symlink on macOS/Linux, junction on Windows) rather than copied. If Unity or a package tool writes to the `Packages/` tree during batch execution (e.g. modifying `packages-lock.json` or an embedded package), those changes propagate back to the original project. This is best-effort isolation; projects using embedded or local-path packages should be aware of this constraint. | Low |
| Shadow — editor-open detection is best-effort | Shadow mode is activated when `Temp/UnityLockfile` exists. If Unity exits uncleanly the lockfile may be left behind, causing unnecessary shadow overhead on the next run. Conversely, in rare timing windows a lockfile may not yet exist even though the editor is starting up. The lockfile check is a heuristic, not a guaranteed signal. | Low |

## P1 Requirements — PlayMode + Network Testing

To reach the ultimate goal (PlayMode + network environment testing), these features are needed:

1. ~~**`test_platform` config field**~~ ✅ — implemented
2. ~~**`[UnityTest]` detection in `list`**~~ ✅ — implemented
3. ~~**Phase-aware timeouts**~~ ✅ — implemented: `compile_ms`/`test_ms` as separate context deadlines; emits `timeout_compile` / `timeout_test` / `timeout_total` phases with correct classification
4. **Exit 8 for signal interruption** — distinguish SIGINT/SIGTERM from timeout at the exit code level
5. **Network test configuration** — timeout tuning for network-dependent tests (larger `test_ms`); test environment flags passed through to Unity
6. **Unique runID** — nanosecond or UUID-based to prevent concurrent-run collision
7. **`--config` flag** — allow specifying config path so agents do not need to `cd`
