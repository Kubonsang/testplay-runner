# testplay-runner

**Go CLI that wraps Unity's broken test runner in a stable contract for AI agents — distinct exit codes, JSON output, no silent failures.**

[한국어](README.ko.md) | English

---

Unity's raw CLI is broken for automation: exit code 0 even on compile failure, XML-only output, no progress visibility, ambiguous error types. `testplay` fixes all of that with a six-command interface designed for AI agents and CI pipelines.

## Who is testplay for?

testplay is a **contract layer**, not a speed layer. Two distinct users:

- **AI agents and CI pipelines** — automated callers that need unambiguous exit codes, structured JSON, and progress files they can poll. testplay is built for them.
- **Human developers in daily TDD** — keep using Unity's Test Runner window. testplay does not compete with sub-second iteration; it makes the *automated* path reliable.

If your AI agent is iterating on Unity tests, testplay's whole job is making each iteration's outcome legible. Speed of any individual test run is not what testplay optimizes for — model latency dominates the loop.

## Problems Solved

| Problem | Solution |
|---|---|
| Exit code 0 on compile failure | Exit 2 on compile error, exit 3 on test failure — always distinct |
| XML-only output | All stdout is JSON with `schema_version` |
| No pre-run validation | `testplay check` validates environment before touching Unity |
| No progress visibility | `testplay-status.json` updated atomically during run |
| Ambiguous timeout | `timeout_type: compile / test / total` in JSON; two-phase execution separates compile and test deadlines |
| No regression tracking | `--compare-run` populates `new_failures` |
| Platform path differences | Absolute + relative paths in every response |
| No test discovery without running | `testplay list` static-scans known attributes — incomplete for custom attributes (see Known Limitations) |
| Unity Editor holds project lock | Shadow Workspace runs tests in `.testplay-shadow/` while editor stays open |

## Installation

**Pre-built binaries (recommended):**

Download from [GitHub Releases](https://github.com/Kubonsang/testplay-runner/releases) — darwin/linux/windows, amd64/arm64.

**From source:**

```bash
git clone https://github.com/Kubonsang/testplay-runner.git
cd testplay-runner
go build -o testplay ./cmd/testplay
```

Or cross-compile:

```bash
GOOS=windows GOARCH=amd64 go build -o testplay.exe ./cmd/testplay
```

## Configuration

Generate `testplay.json` with `testplay init`:

```bash
testplay init --unity-path /path/to/Unity
```

Or create it manually in your project root:

```json
{
  "schema_version": "1",
  "unity_path": "/Applications/Unity/Hub/Editor/2022.3.0f1/Unity.app/Contents/MacOS/Unity",
  "project_path": "/path/to/your/UnityProject",
  "test_platform": "edit_mode",
  "timeout": {
    "total_ms": 300000,
    "compile_ms": 60000,
    "test_ms": 240000
  },
  "result_dir": ".testplay/results",
  "retention": {
    "max_runs": 30
  }
}
```

`unity_path` falls back to the `UNITY_PATH` environment variable if omitted.
`project_path` defaults to the directory containing `testplay.json`.
`test_platform` accepts `"edit_mode"` (default) or `"play_mode"`. This is passed as `-testPlatform EditMode|PlayMode` to Unity.
`result_dir` controls the persisted history JSON used by `testplay result`.
Per-run artifacts (`results.xml`, `summary.json`, `manifest.json`, `stdout.log`,
`stderr.log`, `events.ndjson`) are always written under
`<project_path>/.testplay/runs/<run_id>/`.
`retention.max_runs` controls automatic cleanup of old runs (default 30). Set to `0` to disable pruning.

**Timeout configuration:**
- `total_ms` (default 300000): outer safety-net deadline for the entire run.
- `compile_ms` + `test_ms`: **both must be set together** to enable two-phase execution — Unity runs compile-only first (`compile_ms` deadline), then runs tests (`test_ms` deadline). Phase-specific timeouts emit `timeout_type: "compile"` or `"test"`, while the outer `total_ms` may still emit `"total"`. Setting only one of the two is a config validation error.
- When neither `compile_ms` nor `test_ms` is set, single-phase execution is used (compile + test in one Unity invocation, governed by `total_ms`).

> **Note:** PlayMode network harness and NGO orchestration are not yet supported.

## Commands

### `testplay version`

Prints the current testplay version as JSON.

```bash
testplay version
```

```json
{
  "schema_version": "1",
  "version": "v0.8.0"
}
```

---

### `testplay init`

Generates a `testplay.json` configuration file with sensible defaults. Run this once to bootstrap a new project.

```bash
testplay init --unity-path /path/to/Unity
testplay init --test-platform play_mode
testplay init --force  # overwrite existing testplay.json
```

```json
{
  "created": "testplay.json",
  "unity_path": "/path/to/Unity",
  "project_path": "/current/directory"
}
```

Unity path resolution: `--unity-path` flag > `UNITY_PATH` env var > empty (with warning).
Exit 5 if `testplay.json` already exists (use `--force` to overwrite) or if `--test-platform` is invalid.

---

### `testplay check`

Validates Unity path, project path, and config before running. Run this first.

```bash
testplay check
```

```json
{
  "schema_version": "1",
  "ready": true
}
```

On failure:

```json
{
  "schema_version": "1",
  "ready": false,
  "hint": "set UNITY_PATH or add unity_path to testplay.json"
}
```

Exit 0 = ready. Exit 1 = dependency missing (fix per `hint`). Exit 5 = config invalid.

---

### `testplay list`

Static scan of `*.cs` files for `[Test]`, `[UnityTest]`, `[TestCase]`, `[TestCaseSource]`, and `[Theory]` attributes. Returns candidate test names without running Unity.

**This is a best-effort hint, not a complete inventory.** Custom test attributes (`[NetworkTest]`, `[IntegrationTest]`, project-specific bases, etc.) are silently skipped. The output has no way to tell you what it missed.

Practical guidance:
- Use `list` to generate `--filter` candidates for tests you already know exist.
- When full coverage matters, run `testplay run` without `--filter`. Unity discovers all tests itself; `testplay list` does not.
- A test absent from `list` output may still exist and run.

```bash
testplay list
```

After a successful `testplay run` (exit 0 or 3), the complete test list is cached. Subsequent `list` calls return `complete: true` from that cache:

```json
{
  "schema_version": "1",
  "complete": true,
  "source": "run_cache",
  "cached_run_id": "20250325-143000-a3f8b2c1",
  "tests": ["MyTests.PlayerTests.TestJump", "MyTests.PlayerTests.TestRun"]
}
```

Before the first successful run, `list` falls back to a static scan:

```json
{
  "schema_version": "1",
  "complete": false,
  "source": "static_scan",
  "tests": ["MyTests.PlayerTests.TestJump", "MyTests.PlayerTests.TestRun"]
}
```

---

### `testplay run`

Runs Unity tests using the configured `test_platform` (`edit_mode` or `play_mode`). Streams progress to `testplay-status.json`.

```bash
testplay run
testplay run --filter TestJump
testplay run --category Smoke
testplay run --compare-run 20250301-102200-a3f8b2c1
testplay run --config path/to/testplay.json
testplay run --shadow              # force shadow workspace even without editor lock
testplay run --clear-cache         # remove cached Library before shadow workspace creation
testplay run --scenario scenario.json  # multi-instance concurrent execution
```

**All tests pass (exit 0):**

```json
{
  "schema_version": "1",
  "run_id": "20250325-143000-a3f8b2c1",
  "exit_code": 0,
  "total": 2,
  "passed": 2,
  "failed": 0,
  "skipped": 0,
  "tests": [
    {
      "name": "MyTests.PlayerTests.TestJump",
      "result": "Passed",
      "duration_s": 0.006
    },
    {
      "name": "MyTests.PlayerTests.TestRun",
      "result": "Passed",
      "duration_s": 0.004
    }
  ],
  "new_failures": null
}
```

**Test failure (exit 3):**

```json
{
  "schema_version": "1",
  "run_id": "20250325-143000-a3f8b2c1",
  "total": 10,
  "passed": 9,
  "failed": 1,
  "skipped": 0,
  "tests": [
    {
      "name": "MyTests.PlayerTests.TestJump",
      "result": "Failed",
      "message": "Expected 1 but was 0",
      "excerpt": "Expected 1 but was 0 (at PlayerTests.cs:42)",
      "file": "Assets/Tests/PlayerTests.cs",
      "absolute_path": "/path/to/UnityProject/Assets/Tests/PlayerTests.cs",
      "line": 42
    }
  ],
  "new_failures": null
}
```

**Compile failure (exit 2):**

```json
{
  "schema_version": "1",
  "run_id": "20250325-143000-a3f8b2c1",
  "exit_code": 2,
  "total": 0,
  "passed": 0,
  "failed": 0,
  "skipped": 0,
  "tests": [],
  "errors": [
    {
      "file": "Assets/Scripts/Player.cs",
      "absolute_path": "/path/to/UnityProject/Assets/Scripts/Player.cs",
      "line": 17,
      "message": "CS0103: The name 'speed' does not exist in the current context"
    }
  ],
  "new_failures": null
}
```

---

### `testplay result`

Lists stored run history. Never re-runs Unity.

```bash
testplay result
testplay result --last 3
```

```json
{
  "schema_version": "1",
  "runs": [
    {"run_id": "20250325-143000-a3f8b2c1", "exit_code": 0, "total": 10, "passed": 10, "failed": 0},
    {"run_id": "20250324-091500-b7d2e4f0", "exit_code": 3, "total": 10, "passed": 9, "failed": 1}
  ]
}
```

## Shadow Workspace

When the Unity Editor has the project open, `Temp/UnityLockfile` exists and Unity's batch mode cannot run against the same project directory. `testplay run` detects this automatically and creates a per-run shadow workspace at `.testplay-shadow-<run_id>/` inside your project root:

| Directory | Strategy |
|---|---|
| `Assets/` | Copied fresh on every run |
| `ProjectSettings/` | Copied fresh on every run |
| `Packages/` | Symlinked (junction on Windows) |
| `Library/` | Seeded from `.testplay/cache/Library/` when available; cold-starts otherwise |
| `Temp/` | Deleted before each run; Unity recreates it |

Each run gets its own isolated shadow directory, making parallel `testplay run` invocations safe. The shadow directory is removed automatically after the run via `ws.Cleanup()`.

**Library warm cache:** The first successful run populates `.testplay/cache/Library/`. Subsequent shadow runs seed `Library/` from this cache, avoiding cold-start reimport. The cache is invalidated when `ProjectVersion.txt` or `Packages/manifest.json` changes. Use `--clear-cache` to force a cold start.

**Shadow mode is transparent to agents.** All `absolute_path` fields in the JSON output are remapped to source project paths — agents never see shadow paths.

**Flags:**
- `--shadow` — force shadow workspace even when the editor is not open (useful for testing shadow behaviour)
- `--reset-shadow` — equivalent to `--shadow` (with per-run isolation every run already starts fresh; kept for API compatibility)
- `--clear-cache` — remove `.testplay/cache/` before shadow workspace creation, forcing Unity to reimport from scratch

**`.gitignore` is patched automatically** to exclude `.testplay-shadow-*/` on first use.

## Exit Codes

| Code | Meaning | Action |
|---|---|---|
| 0 | All tests passed | Proceed |
| 1 | Unity / project not found | Fix env, check `hint` field |
| 2 | Compile failure | Fix source, see `errors[].absolute_path` + `line` |
| 3 | Test failure | Fix test, see `tests[].absolute_path` + `line` |
| 4 | Timeout | Check `timeout_type` in the JSON result — see table below |
| 5 | Config error | Fix or create `testplay.json` |
| 6 | Build failure (license / build target) | Check Unity license activation and installed build modules |
| 7 | Permission error (shadow workspace) | Fix permissions on project directory |
| 8 | Interrupted by signal | SIGINT/SIGTERM received — retry without code changes |
| 9 | Runner system error | Result/artifact save failed — check disk space/permissions, see `warnings` field |

### Exit 4 — timeout_type values

| `timeout_type` | `phase` in status | Cause |
|---|---|---|
| `"compile"` | `timeout_compile` | Compile-only phase exceeded `compile_ms` deadline |
| `"test"` | `timeout_test` | Test phase exceeded `test_ms` deadline |
| `"total"` | `timeout_total` | Outer `total_ms` deadline expired (fires in either phase) |

Example JSON for a compile-phase timeout:

```json
{
  "schema_version": "1",
  "exit_code": 4,
  "timeout_type": "compile",
  "tests": [],
  "errors": []
}
```

## Progress Monitoring

**Polling is the only mechanism.** There is no push notification, webhook, or SSE endpoint. Your agent must read `testplay-status.json` on an interval. Use the `seq` field (increments on every write) to detect whether the file changed since the last read — no need to parse `updated_at` for change detection.

During `testplay run`, poll `testplay-status.json` to track progress:

```json
{
  "schema_version": "1",
  "phase": "running",
  "run_id": "20250325-143000-a3f8b2c1",
  "total": 10,
  "passed": 3,
  "failed": 0,
  "updated_at": "2025-03-25T14:30:05Z",
  "started_at": "2025-03-25T14:29:58Z",
  "last_heartbeat_at": "2025-03-25T14:30:03Z",
  "artifact_root": "/Users/user/MyProject/.testplay/runs/20250325-143000-a3f8b2c1",
  "pid": 12345
}
```

Phase progression (single-phase): `compiling → done`
Phase progression (two-phase): `compiling → running → done`
Failure phases: `timeout_compile`, `timeout_test`, `timeout_total`, `interrupted`

## Recommended Agent Flow

```
0. testplay init           # Generate testplay.json (first time only)
1. testplay check          # Validate environment
2. testplay list           # Discover test names
3. testplay run            # Execute (poll testplay-status.json for progress)
4. testplay result --last 3  # Review run history
```

## Development

```bash
# Run all tests with race detector
go test -race ./...

# Run integration tests
go test -tags=integration ./cmd/testplay/...

# Build for current platform
go build ./cmd/testplay
```

## Unity Smoke Verification

A minimal Unity project (`fixtures/smoke-project/`) is included to verify that
`testplay run` works end-to-end with a real Unity installation. It contains one
EditMode test and one PlayMode (`[UnityTest]`) test.

**Local reproduction:**

```bash
# Prerequisites: Unity installed, UNITY_PATH set
export UNITY_PATH=/Applications/Unity/Hub/Editor/2022.3.0f1/Unity.app/Contents/MacOS/Unity
./scripts/smoke.sh
```

The script:
1. Writes a `testplay.json` for each platform (EditMode then PlayMode)
2. Runs `testplay check` + `testplay run`
3. Verifies all 6 run artifacts are present in `.testplay/runs/<run_id>/`:
   `results.xml`, `summary.json`, `manifest.json`, `stdout.log`, `stderr.log`, `events.ndjson`
4. Verifies `testplay-status.json` exists in the project root (status snapshot, outside the run artifact directory)
5. Runs a shadow-mode smoke stage using `--shadow` and verifies the shadow workspace is created with expected subdirectories

**CI (opt-in):**

```bash
gh workflow run smoke.yml
```

See `.github/workflows/smoke.yml`. Requires a self-hosted runner with Unity
and `UNITY_PATH` set in the runner environment.

For a reusable real-project pattern, see
[`docs/05_v0.2.0_playmode_smoke_example.md`](docs/05_v0.2.0_playmode_smoke_example.md). It shows a
scene-free PlayMode smoke test that creates its fixture in code and runs
cleanly through `testplay run`.

## Known Limitations

These are current gaps, documented honestly. Each has a planned fix.

**`testplay list` static scan may be incomplete.**
The static scanner only detects `[Test]`, `[UnityTest]`, `[TestCase]`, `[TestCaseSource]`, and `[Theory]`. Tests using custom attributes or abstract base-class patterns are invisible to it. The output includes `complete` and `source` fields so agents can tell whether the list is exhaustive. After the first `testplay run` completes (exit 0 or 3), a run cache is written to `.testplay/cache/list.json` and subsequent `testplay list` calls return `complete: true, source: "run_cache"` with the full inventory from the actual run.

**Progress monitoring requires polling.**
`testplay-status.json` is the only channel for in-flight status. No SSE, no websocket, no named pipe. An agent that polls too slowly will miss rapid phase transitions; one that polls too fast wastes cycles. Use `seq` (increments on every write) to detect whether the file changed since the last read, and `updated_at` as a human-readable timestamp. Planned fix: optional SSE endpoint when PlayMode network testing is introduced.

## License

Apache 2.0 — see [LICENSE](LICENSE).
Third-party notices — see [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES).
