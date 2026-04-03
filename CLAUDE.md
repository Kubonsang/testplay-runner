# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

TestPlay Runner (`testplay`) is a thin Go CLI wrapper around Unity's test runner. It solves eight specific problems that make Unity's raw CLI unusable for AI agents: unreliable exit codes, XML-only results, ambiguous compile vs. test failures, no progress visibility, no pre-validation, and platform path differences.

Agents interact via six commands: `version`, `check`, `list`, `run`, `result`, `init`. All stdout is JSON; all human-readable logs go to stderr.

**Supported test platforms:** `"edit_mode"` (default) and `"play_mode"` — set via `test_platform` in `testplay.json`. The platform is passed as `-testPlatform EditMode|PlayMode` to Unity.

**Current version:** `v0.7.0-rc` (main). Release candidate — `testplay init` onboarding, GoReleaser pre-built binaries, cross-platform CI, scenario-mode artifact retention.

**Ultimate goal:** PlayMode + network environment testing.

## Build & Run

```bash
# Build for current platform
go build ./cmd/testplay

# Cross-compile
GOOS=darwin  GOARCH=amd64 go build -o testplay       ./cmd/testplay
GOOS=windows GOARCH=amd64 go build -o testplay.exe   ./cmd/testplay

# Run tests
go test ./...

# Run a single package's tests
go test ./internal/parser/...

# Run E2E tests (requires Unity; opt-in)
UNITY_PATH=/path/to/Unity go test -tags e2e ./e2e/...
```

External dependencies are kept minimal — `cobra` for CLI parsing, everything else uses stdlib.

## Release Rules

**절대로 원격에 푸시된 태그를 덮어쓰지 않는다 (`git tag -f` + `git push --force`).**

- **Go Module Proxy 캐싱:** `proxy.golang.org`에 이전 해시가 캐싱되면, 태그 덮어쓰기 후 다른 사용자가 `go get` 시 Checksum Mismatch 보안 에러로 빌드가 깨진다.
- **팀 싱크:** 로컬과 원격 태그 불일치로 혼란 발생.
- **올바른 방법:** 코드 수정 후에는 `v0.6.1-beta` (패치 버전업) 또는 `v0.6.0-beta.2` (프리릴리즈 증분) 등 새 버전을 발행한다.
- SemVer 원칙: 한 번 발행된 버전은 불변(immutable). 내용이 바뀌면 버전도 바뀌어야 한다.

## Package Structure

```
cmd/testplay/        # CLI entry points for init, version, check, list, run, result subcommands
internal/
  unity/             # Unity process execution and path discovery
  parser/            # NUnit XML → Go struct → JSON conversion
  status/            # Atomic testplay-status.json updates during run
  history/           # Result file persistence and history queries
  runsvc/            # Run orchestration service (backend selection, path remap)
  shadow/            # Shadow Workspace — lockfile detection, copy/link, path remap
  artifacts/         # Per-run artifact directory and file management
  scenario/          # Scenario JSON loading, validation, and multi-instance spec
  config/            # testplay.json loading and validation
  runid/             # Shared run-ID format validation (regex pattern)
```

## CLI Contract (stdout = JSON only)

Every command outputs a single JSON object to stdout with a `schema_version` field. Nothing else ever goes to stdout.

| Command | Purpose |
|---|---|
| `testplay version` | Print current version as JSON |
| `testplay init [--unity-path <path>] [--test-platform <platform>] [--force]` | Generate testplay.json with sensible defaults |
| `testplay check` | Validate Unity path, project path, and testplay.json before running |
| `testplay list` | Static source scan returning candidate test names (not guaranteed complete) |
| `testplay run [--filter <name>] [--category <cat>] [--compare-run <run_id>] [--shadow] [--reset-shadow] [--clear-cache] [--scenario <file>]` | Execute tests; streams progress to `testplay-status.json` (single mode) or `testplay-status-<role>.json` (scenario mode) |
| `testplay result [--last N]` | Re-read stored results; returns run_id history |

**`--reset-shadow`**: Activates shadow workspace mode. With per-run isolation (v0.3+), equivalent to `--shadow` — every run already starts with a fresh workspace. Kept for API compatibility.

**`--clear-cache`**: Removes the cached Library (`.testplay/cache/`) before shadow workspace creation, forcing Unity to reimport from scratch. Use when the cache might be corrupted or when troubleshooting import-related failures.

**Scenario JSON `env` field:** Each instance in a scenario file can specify an `env` map of environment variables injected into the Unity process. Keys must be non-empty and must not contain `=`. Values override inherited environment variables.

```json
{
  "instances": [
    {
      "role": "host",
      "config": "host/testplay.json",
      "env": { "NETWORK_ROLE": "server", "PORT": "7777" }
    },
    {
      "role": "client",
      "config": "client/testplay.json",
      "depends_on": "host",
      "env": { "NETWORK_ROLE": "client", "SERVER_PORT": "7777" }
    }
  ]
}
```

## Exit Code Semantics

| Code | Meaning | Agent action | Implemented |
|---|---|---|---|
| 0 | All tests passed | Proceed | ✅ |
| 1 | Dependency error (Unity/project not found) | Fix env, check `hint` field | ✅ |
| 2 | Compile failure | Fix source code, see `errors[].absolute_path` + `line` | ✅ |
| 3 | Test failure | Fix test logic, see `tests[].absolute_path` + `line` | ✅ |
| 4 | Timeout | Check `timeout_type` — `"compile"`, `"test"`, or `"total"` | ✅ |
| 5 | Config error (testplay.json missing/invalid) | Fix config file | ✅ |
| 6 | Build failure (missing build target, license) | Fix build environment | ❌ not yet returned |
| 7 | Permission error | Fix path/permissions | ❌ not yet returned |
| 8 | Interrupted by signal | Retry without code changes | ✅ |
| 9 | Runner system error (result/artifact save failed) | Check disk space/permissions; results may be lost, see `warnings` field | ✅ |

**timeout_type values for exit 4:**
- `"compile"` — compile-only phase exceeded `compile_ms` deadline (two-phase mode)
- `"test"` — test phase exceeded `test_ms` deadline (two-phase mode)
- `"total"` — outer `total_ms` deadline expired (either phase)

**Signal behavior:** SIGINT/SIGTERM calls `causeCancel(unity.ErrSignalInterrupt)` → executor checks `context.Cause(ctx)` → returns exit 8 with no `timeout_type`. Timeout returns exit 4.

## testplay.json (project config)

```json
{
  "schema_version": "1",
  "unity_path": "/Applications/Unity/Hub/Editor/2022.3.0f1/Unity.app/Contents/MacOS/Unity",
  "project_path": "/Users/user/MyProject",
  "test_platform": "edit_mode",
  "timeout": {
    "total_ms": 300000
  },
  "result_dir": ".testplay/results",
  "retention": {
    "max_runs": 30
  }
}
```

**Retention:** `retention.max_runs` controls how many recent run results and artifact directories to keep. After each run (single-mode and scenario mode), older entries are pruned automatically. In scenario mode, prune targets are deduplicated across instances sharing the same project. Defaults to 30 if omitted. Set to `0` to disable pruning entirely.

`test_platform` accepts `"edit_mode"` (default) or `"play_mode"`.

`unity_path` falls back to `UNITY_PATH` env var if omitted. `project_path` defaults to the directory containing `testplay.json`.

**Two-phase execution:** when both `compile_ms` and `test_ms` are set (both > 0), two-phase execution is enabled. Both fields must be set together — setting only one is a validation error. When neither is set, single-phase execution uses only `total_ms`.

**Config path:** Loaded from the path given by `--config <path>` (default: `"testplay.json"` in cwd). When `--config` is omitted, behaviour is unchanged from v0.2.

## Runtime Files

- `testplay-status.json` — written atomically during `run`; poll this to observe progress.
  - **Path:** hardcoded to `"testplay-status.json"` in cwd. No config option.
  - **Phase values actually emitted (single-phase):** `compiling → done | timeout_total | interrupted`
  - **Phase values actually emitted (two-phase):** `compiling → running → done | timeout_compile | timeout_test | timeout_total | interrupted`
  - `waiting` — defined but never written by the runner (pre-run initial state)
  - `timeout_compile`, `timeout_test` — written in two-phase mode when the respective phase deadline fires
  - `running` — written only in two-phase mode when compile phase completes and test phase begins. Not emitted in single-phase mode (v0.6.0+: removed misleading post-exit write)
  - `interrupted` — best-effort write on SIGINT/SIGTERM before context cancel; process exits 8
- `testplay-status-<role>.json` — written per instance in `--scenario` mode. Same schema as `testplay-status.json`. Path is in cwd, named after the instance's `role` field (e.g. `testplay-status-host.json`). Absent for instances that have not yet started.
- `.testplay/results/<run_id>.json` — one file per run, never overwritten. `run_id` format: `YYYYMMDD-HHMMSS-xxxxxxxx` where the 8-char hex suffix is 4 crypto-random bytes (e.g. `20250301-102200-a3f8b2c1`). Collision probability is negligible even under parallel runs.
- `.testplay-shadow-<run_id>/` — per-run shadow workspace created automatically when `Temp/UnityLockfile` is detected (Unity Editor open). Contains copied `Assets/`, `ProjectSettings/`, linked `Packages/`, and an empty `Library/` (Unity populates during the run). Removed automatically after each run via `ws.Cleanup()`. Excluded from git via `.gitignore` auto-patching (`testplay-shadow-*/`). Use `--reset-shadow` to force shadow mode (equivalent to `--shadow`; no persistent cache exists to reset).

## Agent Recommended Usage Flow

Standard flow for agents using TestPlay Runner:

**Step 0 — init (first time only)**
Run `testplay init --unity-path <path>` to generate `testplay.json`. Skip if config already exists.

**Step 1 — check**
Run `testplay check` to validate the environment. If `ready: false`, fix the environment per the `hint` field and re-check.

**Step 2 — list**
Run `testplay list` to get candidate test names. Collect names related to modified code as `--filter` candidates. Assume the list may be incomplete.

**Step 3 — run**
Run `testplay run`. Use `--compare-run` to enable regression analysis. Poll `testplay-status.json` to track progress.

**Step 4 — Evaluate result**
Branch on exit code. If exit 4, sub-branch on `timeout_type`. If exit 3 with `--compare-run` specified, check `new_failures` to determine regression.

**Step 5 — Fix or finish**
If exit 2, go to `errors[].absolute_path` + `line` to fix source. If exit 3, go directly to the failing test's `absolute_path`.

**Step 6 — result**
Run `testplay result` to review the `run_id` list and decide the `--compare-run` value for the next run.

> `init → version → check → list → run → result` — this six-command interface is the agent's entire surface. If this flow breaks, the project breaks.

**Shadow mode is transparent to agents.** When `Temp/UnityLockfile` is present, `testplay run` automatically uses a per-run `.testplay-shadow-<run_id>/` workspace and remaps all `absolute_path` fields in the JSON output back to source project paths. Agents do not need to detect or handle shadow mode explicitly.

## Output Design Rules

1. **stdout = JSON only.** No banners, progress lines, or mixed output ever.
2. **stderr = human logs.** Agents may ignore stderr entirely.
3. Every JSON response includes `schema_version`.
4. All file path fields include both `file` (relative) and `absolute_path`.
5. `hint` field is included only on exit 1 — the one case where an agent can auto-recover.
6. `new_failures` in exit 3 is only populated when `--compare-run` is specified; otherwise `null`.
7. `warnings` (string array) is included only when non-fatal infrastructure issues occur (e.g. result save failed, summary write failed). Absent when no warnings.
8. `orchestrator_errors` (string array) is included in scenario mode output only when a dependency wait fails (ready timeout or context cancellation). Absent when no orchestration errors occurred.
9. `parameterized_group` (string) on test entries is present only when the test-case is inside an NUnit `ParameterizedMethod` suite. Absent for non-parameterized tests.
10. `excerpt` (string) on test entries is present only when the test failed. Format: `"message (at filename.cs:line)"` or just `"message"` when no file info available. Absent for passing/skipped tests.

## Known Limitations & Risks

| Area | Issue | Severity |
|---|---|---|
| `list` scanner | Detects `[Test]`, `[UnityTest]`, `[TestCase]`, `[TestCaseSource]`, and `[Theory]` but may miss custom test attributes — list output may be incomplete. NUnit XML parsing (v0.5.0+) correctly handles parameterized test results with `parameterized_group` field. | Low |
| Phase detection (single-phase) | `running` phase not emitted in single-phase mode — agents see `compiling → done` only. Two-phase mode emits accurate `compiling → running → done` sequence. | Low |
| Unimplemented exit codes | Exit 6 (build failure), exit 7 (permission) are documented but never returned | Low |
| Shadow — `Packages/` not fully isolated | `Packages/` is linked (symlink on macOS/Linux, junction on Windows) rather than copied. If Unity or a package tool writes to the `Packages/` tree during batch execution (e.g. embedded packages), those changes propagate back to the original project. This is best-effort isolation. | Low |
| Shadow — editor-open detection is best-effort | Shadow mode activates when `Temp/UnityLockfile` exists. A stale lockfile after an unclean Unity exit causes unnecessary shadow overhead. The lockfile check is a heuristic, not a guaranteed signal. | Low |
| Shadow — Library cold-start per run | `Library/` is seeded from a project-local cache (`.testplay/cache/Library/`) when available. First run after a cache miss still cold-starts. Cache is invalidated when `ProjectVersion.txt` or `Packages/manifest.json` changes. Use `--clear-cache` to force a cold start. In `--scenario` mode, cache seeding (read) works per instance but cache write-back is skipped to avoid concurrent writes; a single-mode run is needed to populate the cache. | Low |
| Scenario — status polling (per-instance) | `testplay-status-<role>.json` is written for each instance in `--scenario` mode. No scenario-level aggregate status file exists; agents must poll per-role files. | Low |
| Scenario — host crash error detail | ~~Resolved in v0.5.0~~ — dependent instance error messages now include the dependency's exit code and failure type (e.g., "exited with exit 2 (compile error)"). | Resolved |
| Scenario — env key case on Windows | ~~Resolved in v0.6.0~~ — `MergeEnv` now uses case-insensitive key matching on Windows via `strings.EqualFold`. | Resolved |

## Roadmap

Tracked against [RELEASE-PLAN.md](RELEASE-PLAN.md).

### v0.1.0-beta ✅ — Foundation
Single-process Unity test runner with structured JSON output, phase-aware timeouts, artifact persistence, and run history.

### v0.2.0-beta ✅ — The Editor Unlock (shipped)
Shadow Workspace: automatic fallback when the Unity Editor has the project open.
- Shadow Workspace auto-fallback — `Temp/UnityLockfile` detection → `.testplay-shadow/` isolation
- Path remapping — all `absolute_path` fields in JSON output use source project paths
- `--shadow` flag (force) / `--reset-shadow` flag (rebuild Library cache)
- `.gitignore` auto-patching — `.testplay-shadow/` excluded on first use
- Production hardening: symlink preservation, FileMode copy, ctx-cancel mid-copy, rollback safety, ring-buffer stderr tail, Null Object StatusWriter

### v0.3.0-beta ✅ — The Multi-Instance Core (shipped)

**P1 backlog resolved as prerequisites:**
1. **Unique runID** — crypto-random 8-hex suffix; prevents concurrent-run result file collision
2. **`--config` flag** — config path as CLI arg; removes CWD dependency for multi-instance orchestration
3. **Per-run shadow isolation** — run-ID-scoped shadow dir (`.testplay-shadow-<run_id>/`); makes parallel `testplay run` safe
4. ~~**Exit 8 for signal interruption**~~ ✅ — SIGINT/SIGTERM → exit 8; timeout → exit 4

**New capability:**
5. **`testplay run --scenario <file>`** — Role-based (Host/Client) multi-instance concurrent execution; individual results aggregated into single scenario JSON

**CLI rename:** binary and all user-facing identifiers renamed `fastplay` → `testplay` to match repo name.

### v0.4.0-beta ✅ — The Orchestrator (shipped)
Host/Client startup ordering via Go channels (no disk polling).
- **Per-instance status polling** — `testplay-status-<role>.json` written per instance in scenario mode
- **Host ready gating** — `depends_on`/`ready_phase`/`ready_timeout_ms` fields in scenario JSON; clients wait for host's ready phase before starting
- **`orchestrator_errors`** — structured field in scenario output for dependency timeout/cancellation failures

### v0.4.2-beta ✅ — Library Warm Cache (shipped)
Shadow workspace Library/ seeded from project-local cache to eliminate cold-start latency.
- **Parallel copy** — `copyDir` parallelized with 8-goroutine worker pool
- **Cache infrastructure** — `.testplay/cache/Library/` with SHA256-based invalidation key
- **Cache lifecycle** — first run cold-starts → cache on success (exit 0/3) → seed subsequent runs → invalidate on project change
- **Scenario safety** — cache write-back skipped in `--scenario` mode to prevent concurrent write corruption
- **`--clear-cache` flag** — force cache removal before shadow workspace creation

### v0.5.0-beta ✅ — The AI Contract (shipped)
AI-facing output contract strengthening.
- **Host exit code propagation** — fast-fail error messages include dependency exit code and failure label (e.g., "exited with exit 2 (compile error)")
- **Parameterized test parsing** — `ParameterizedMethod` suite type → `parameterized_group` field on test entries
- **E2E test infrastructure** — opt-in Unity-based integration tests (`//go:build e2e` + `UNITY_PATH`)

### v0.5.1-beta ✅ — Hardening (shipped)
Security and contract completeness fixes.
- **Exit 9 (runner system error)** — result save, summary write, or manifest write failure overrides exit code to 9; distinguishes runner infrastructure failure from test results
- **List scanner expansion** — `[TestCase]`, `[TestCaseSource]`, `[Theory]` attributes now detected by `testplay list`

### v0.6.0-beta ✅ — The Network Ready (shipped)
Production readiness for network test scenarios.
- **Failure excerpt** — `excerpt` field on failed test entries: `"message (at filename.cs:line)"` for immediate agent triage
- **Phase accuracy** — single-phase mode no longer emits misleading `running` phase after Unity exits; emits `compiling → done` honestly
- **Env injection** — scenario instances support `env` map for per-instance environment variables (e.g., network role, port assignments)
- **Windows process kill** — `taskkill /F /T /PID` with `CREATE_NEW_PROCESS_GROUP` ensures Unity child processes are terminated on cancel
- **Artifact retention** — `retention.max_runs` config (default 30) auto-prunes old result files and artifact directories after each single-mode run

### v0.7.0-rc ✅ — Release Candidate (shipped)
Production readiness — onboarding and distribution.
- **`testplay init`** — generates `testplay.json` with sensible defaults; discovers Unity path from `UNITY_PATH` env var
- **GoReleaser** — cross-platform pre-built binaries (darwin/linux/windows, amd64/arm64) published to GitHub Releases on tag push
- **Cross-platform CI** — `go test ./...` on ubuntu, macos, and windows for every push/PR
- **Scenario-mode artifact retention** — auto-prune after multi-instance runs, deduplicating shared project paths

### Remaining items (v1.0+)

- **Network test configuration** — NGO/Mirror harness integration
