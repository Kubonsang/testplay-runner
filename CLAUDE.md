# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

FastPlay Runner (`fastplay`) is a thin Go CLI wrapper around Unity's test runner. It solves eight specific problems that make Unity's raw CLI unusable for AI agents: unreliable exit codes, XML-only results, ambiguous compile vs. test failures, no progress visibility, no pre-validation, and platform path differences.

Agents interact via five commands: `version`, `check`, `list`, `run`, `result`. All stdout is JSON; all human-readable logs go to stderr.

**Supported test platforms:** `"edit_mode"` (default) and `"play_mode"` — set via `test_platform` in `fastplay.json`. The platform is passed as `-testPlatform EditMode|PlayMode` to Unity.

**Current version:** `v0.2.0-beta` (main). Shadow Workspace shipped. Next: multi-instance core (v0.3.0-beta).

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
| `fastplay run [--filter <name>] [--category <cat>] [--compare-run <run_id>] [--shadow] [--reset-shadow]` | Execute tests; streams progress to `fastplay-status.json` |
| `fastplay result [--last N]` | Re-read stored results; returns run_id history |

**`--reset-shadow`**: Activates shadow workspace mode. With per-run isolation (v0.3+), equivalent to `--shadow` — every run already starts with a fresh workspace. Kept for API compatibility.

## Exit Code Semantics

| Code | Meaning | Agent action | Implemented |
|---|---|---|---|
| 0 | All tests passed | Proceed | ✅ |
| 1 | Dependency error (Unity/project not found) | Fix env, check `hint` field | ✅ |
| 2 | Compile failure | Fix source code, see `errors[].absolute_path` + `line` | ✅ |
| 3 | Test failure | Fix test logic, see `tests[].absolute_path` + `line` | ✅ |
| 4 | Timeout | Check `timeout_type` — `"compile"`, `"test"`, or `"total"` | ✅ |
| 5 | Config error (fastplay.json missing/invalid) | Fix config file | ✅ |
| 6 | Build failure (missing build target, license) | Fix build environment | ❌ not yet returned |
| 7 | Permission error | Fix path/permissions | ❌ not yet returned |
| 8 | Interrupted by signal | Retry without code changes | ✅ |

**timeout_type values for exit 4:**
- `"compile"` — compile-only phase exceeded `compile_ms` deadline (two-phase mode)
- `"test"` — test phase exceeded `test_ms` deadline (two-phase mode)
- `"total"` — outer `total_ms` deadline expired (either phase)

**Signal behavior:** SIGINT/SIGTERM calls `causeCancel(unity.ErrSignalInterrupt)` → executor checks `context.Cause(ctx)` → returns exit 8 with no `timeout_type`. Timeout returns exit 4.

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

**Config path:** Loaded from the path given by `--config <path>` (default: `"fastplay.json"` in cwd). When `--config` is omitted, behaviour is unchanged from v0.2.

## Runtime Files

- `fastplay-status.json` — written atomically during `run`; poll this to observe progress.
  - **Path:** hardcoded to `"fastplay-status.json"` in cwd. No config option.
  - **Phase values actually emitted:** `compiling → running → done | timeout_compile | timeout_test | timeout_total | interrupted`
  - `waiting` — defined but never written by the runner (pre-run initial state)
  - `timeout_compile`, `timeout_test` — written in two-phase mode when the respective phase deadline fires
  - `running` — written *after* Unity exits, not when tests actually start (phase detection is approximate)
  - `interrupted` — best-effort write on SIGINT/SIGTERM before context cancel; process exits 8
- `.fastplay/results/<run_id>.json` — one file per run, never overwritten. `run_id` format: `YYYYMMDD-HHMMSS-xxxxxxxx` where the 8-char hex suffix is 4 crypto-random bytes (e.g. `20250301-102200-a3f8b2c1`). Collision probability is negligible even under parallel runs.
- `.fastplay-shadow-<run_id>/` — per-run shadow workspace created automatically when `Temp/UnityLockfile` is detected (Unity Editor open). Contains copied `Assets/`, `ProjectSettings/`, linked `Packages/`, and an empty `Library/` (Unity populates during the run). Removed automatically after each run via `ws.Cleanup()`. Excluded from git via `.gitignore` auto-patching (`fastplay-shadow-*/`). Use `--reset-shadow` to force shadow mode (equivalent to `--shadow`; no persistent cache exists to reset).

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

**Shadow mode is transparent to agents.** When `Temp/UnityLockfile` is present, `fastplay run` automatically uses a per-run `.fastplay-shadow-<run_id>/` workspace and remaps all `absolute_path` fields in the JSON output back to source project paths. Agents do not need to detect or handle shadow mode explicitly.

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
| Unimplemented exit codes | Exit 6 (build failure), exit 7 (permission) are documented but never returned | Low |
| Shadow — `Packages/` not fully isolated | `Packages/` is linked (symlink on macOS/Linux, junction on Windows) rather than copied. If Unity or a package tool writes to the `Packages/` tree during batch execution (e.g. embedded packages), those changes propagate back to the original project. This is best-effort isolation. | Low |
| Shadow — editor-open detection is best-effort | Shadow mode activates when `Temp/UnityLockfile` exists. A stale lockfile after an unclean Unity exit causes unnecessary shadow overhead. The lockfile check is a heuristic, not a guaranteed signal. | Low |
| Shadow — Library cold-start per run | `Library/` starts empty each run (no cross-run cache reuse). Unity reimports on every invocation, which adds latency for sequential agent-driven runs. Accepted tradeoff for parallel correctness. | Low |

## Roadmap

Tracked against [RELEASE-PLAN.md](RELEASE-PLAN.md).

### v0.1.0-beta ✅ — Foundation
Single-process Unity test runner with structured JSON output, phase-aware timeouts, artifact persistence, and run history.

### v0.2.0-beta ✅ — The Editor Unlock (shipped)
Shadow Workspace: automatic fallback when the Unity Editor has the project open.
- Shadow Workspace auto-fallback — `Temp/UnityLockfile` detection → `.fastplay-shadow/` isolation
- Path remapping — all `absolute_path` fields in JSON output use source project paths
- `--shadow` flag (force) / `--reset-shadow` flag (rebuild Library cache)
- `.gitignore` auto-patching — `.fastplay-shadow/` excluded on first use
- Production hardening: symlink preservation, FileMode copy, ctx-cancel mid-copy, rollback safety, ring-buffer stderr tail, Null Object StatusWriter

### v0.3.0-beta 🔵 — The Multi-Instance Core (next)

**P1 backlog resolved as prerequisites:**
1. **Unique runID** — UUID/nanosecond-based; prevents concurrent-run result file collision
2. **`--config` flag** — config path as CLI arg; removes CWD dependency for multi-instance orchestration
3. **Per-run shadow isolation** — run-ID-scoped shadow dir (`.fastplay-shadow-<run_id>/`); makes parallel `fastplay run` safe
4. ~~**Exit 8 for signal interruption**~~ ✅ — SIGINT/SIGTERM → exit 8; timeout → exit 4

**New capability:**
5. **`fastplay run --scenario <file>`** — Role-based (Host/Client) multi-instance concurrent execution; individual results aggregated into single scenario JSON

### Remaining items (v0.4+)

- **Network test configuration** — multi-instance orchestration, NGO/Mirror harness; Host/Client ready gating, IPC-based readiness signaling
