# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

FastPlay Runner (`fastplay`) is a thin Go CLI wrapper around Unity's Play Mode testing. It solves eight specific problems that make Unity's raw CLI unusable for AI agents: unreliable exit codes, XML-only results, ambiguous compile vs. test failures, no progress visibility, no pre-validation, and platform path differences.

Agents interact via four commands: `check`, `list`, `run`, `result`. All stdout is JSON; all human-readable logs go to stderr.

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

| Code | Meaning | Agent action |
|---|---|---|
| 0 | All tests passed | Proceed |
| 1 | Dependency error (Unity/project not found) | Fix env, check `hint` field |
| 2 | Compile failure | Fix source code, see `errors[].absolute_path` + `line` |
| 3 | Test failure | Fix test logic, see `tests[].absolute_path` + `line` |
| 4 | Timeout (sub-type in `timeout_type`: `compile`/`test`/`total`) | Adjust timeout or investigate |
| 5 | Config error (fastplay.json missing/invalid) | Fix config file |
| 6 | Build failure (missing build target, license) | Fix build environment |
| 7 | Permission error | Fix path/permissions |
| 8 | Interrupted by signal | Retry without code changes |

Exit 4 always has `timeout_type` in JSON to distinguish 4a/4b/4c sub-cases.

## fastplay.json (project config)

```json
{
  "schema_version": "1",
  "unity_path": "/Applications/Unity/Hub/Editor/2022.3.0f1/Unity.app/Contents/MacOS/Unity",
  "project_path": "/Users/user/MyProject",
  "timeout": {
    "compile_ms": 120000,
    "test_ms": 30000,
    "total_ms": 300000
  },
  "result_dir": ".fastplay/results"
}
```

`unity_path` falls back to `UNITY_PATH` env var if omitted. `project_path` defaults to the directory containing `fastplay.json`.

## Runtime Files

- `fastplay-status.json` — written atomically during `run`; poll this to observe progress. `phase` values: `waiting → compiling → running → done | timeout_* | interrupted`
- `.fastplay/results/<run_id>.json` — one file per run, never overwritten. `run_id` is a timestamp string (e.g. `20250301-102200`).

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
