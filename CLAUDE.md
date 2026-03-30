# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

FastPlay Runner (`fastplay`) is a thin Go CLI wrapper around Unity's test runner. It solves eight specific problems that make Unity's raw CLI unusable for AI agents: unreliable exit codes, XML-only results, ambiguous compile vs. test failures, no progress visibility, no pre-validation, and platform path differences.

Agents interact via five commands: `version`, `check`, `list`, `run`, `result`. All stdout is JSON; all human-readable logs go to stderr.

**Supported test platforms:** `"edit_mode"` (default) and `"play_mode"` — set via `test_platform` in `fastplay.json`. The platform is passed as `-testPlatform EditMode|PlayMode` to Unity.

**Current version:** `v0.1.0-beta` (main). Shadow Workspace (`v0.2.0-beta`) is in review — see PR #15.

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
cmd/fastplay/        # CLI entry points for version, check, list, run, result subcommands
internal/
  unity/             # Unity process execution and path discovery
  parser/            # NUnit XML → Go struct → JSON conversion
  status/            # Atomic fastplay-status.json updates during run
  history/           # Result file persistence and history queries
  runsvc/            # Run orchestration service (backend selection, path remap)
  shadow/            # Shadow Workspace — lockfile detection, copy/link, path remap
  artifacts/         # Per-run artifact directory and file management
  config/            # fastplay.json loading and validation
```

## CLI Contract (stdout = JSON only)

Every command outputs a single JSON object to stdout with a `schema_version` field. Nothing else ever goes to stdout.

| Command | Purpose |
|---|---|
| `fastplay version` | Print current version as JSON |
| `fastplay check` | Validate Unity path, project path, and fastplay.json before running |
| `fastplay list` | Static source scan returning candidate test names (not guaranteed complete) |
| `fastplay run [--filter <name>] [--category <cat>] [--compare-run <run_id>] [--reset-shadow]` | Execute tests; streams progress to `fastplay-status.json` |
| `fastplay result [--last N]` | Re-read stored results; returns run_id history |

**`--reset-shadow`**: Delete and rebuild `.fastplay-shadow/` before running. Use after a Unity version upgrade or when the shadow Library cache appears stale.

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
- `.fastplay-shadow/` — shadow workspace created automatically when `Temp/UnityLockfile` is detected (Unity Editor open). Contains copied `Assets/`, `ProjectSettings/`, linked `Packages/`, and a persistent `Library/` cache. Excluded from git via `.gitignore` auto-patching. Delete manually or via `--reset-shadow` if corrupted. See Known Limitations for concurrency and isolation constraints.

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

> `version → check → list → run → result` — this five-command interface is the agent's entire surface. If this flow breaks, the project breaks.

**Shadow mode is transparent to agents.** When `Temp/UnityLockfile` is present, `fastplay run` automatically uses `.fastplay-shadow/` as the project path and remaps all `absolute_path` fields in the JSON output back to source project paths. Agents do not need to detect or handle shadow mode explicitly.

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
| Config path | Always loads `fastplay.json` from cwd; no `--config` flag — agents must `cd` to project root | Low |
| Unimplemented exit codes | Exit 6 (build failure), exit 7 (permission) are documented but never returned | Low |
| Shadow — concurrent run safety | Two simultaneous `fastplay run` invocations against the same project share a single `.fastplay-shadow/` directory. `Prepare` re-copies `Assets/` and `ProjectSettings/` and deletes `Temp/` on every call; a second run starting while the first is executing will overwrite those directories mid-flight. Running `fastplay run` in parallel against the same project path is **not currently safe** in shadow mode. | Medium |
| Shadow — `Packages/` not fully isolated | `Packages/` is linked (symlink on macOS/Linux, junction on Windows) rather than copied. If Unity or a package tool writes to the `Packages/` tree during batch execution (e.g. embedded packages), those changes propagate back to the original project. This is best-effort isolation. | Low |
| Shadow — editor-open detection is best-effort | Shadow mode activates when `Temp/UnityLockfile` exists. A stale lockfile after an unclean Unity exit causes unnecessary shadow overhead. The lockfile check is a heuristic, not a guaranteed signal. | Low |

## Roadmap

Tracked against [RELEASE-PLAN.md](RELEASE-PLAN.md).

### v0.1.0-beta ✅ — Foundation (current main)
Single-process Unity test runner with structured JSON output, phase-aware timeouts, artifact persistence, and run history.

### v0.2.0-beta 🚧 — The Editor Unlock (PR #15 in review)
Shadow Workspace: automatic fallback when the Unity Editor has the project open.
- ~~Shadow Workspace auto-fallback~~ ✅ — `Temp/UnityLockfile` detection → `.fastplay-shadow/` isolation
- ~~Path remapping~~ ✅ — all `absolute_path` fields in JSON output use source project paths
- ~~`--reset-shadow` flag~~ ✅ — force rebuild of shadow Library cache
- ~~`.gitignore` auto-patching~~ ✅ — `.fastplay-shadow/` excluded on first use

**Release gate (v0.2):** With the Unity Editor open, `fastplay run` completes without corrupting the source project and all `absolute_path` fields in the result JSON point to source project paths.

### Remaining P1 items (v0.3+)

1. **Exit 8 for signal interruption** — distinguish SIGINT/SIGTERM from timeout at exit code level
2. **Unique runID** — nanosecond or UUID-based to prevent concurrent-run collision
3. **`--config` flag** — allow specifying config path so agents do not need to `cd`
4. **Parallel `fastplay run` safety in shadow mode** — per-run shadow workspace (advisory lock or run-ID-scoped directory)
5. **Network test configuration** — multi-instance orchestration, NGO/Mirror harness (v0.3–v0.4 scope)
