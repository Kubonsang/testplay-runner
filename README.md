# testplay-runner

**Go CLI that makes Unity tests reliable for AI agents**

[한국어](README.ko.md) | English

---

Unity's raw CLI is broken for automation: exit code 0 even on compile failure, XML-only output, no progress visibility, ambiguous error types. `fastplay` fixes all of that with a four-command interface designed for AI agents and CI pipelines.

## Problems Solved

| Problem | Solution |
|---|---|
| Exit code 0 on compile failure | Exit 2 on compile error, exit 3 on test failure — always distinct |
| XML-only output | All stdout is JSON with `schema_version` |
| No pre-run validation | `fastplay check` validates environment before touching Unity |
| No progress visibility | `fastplay-status.json` updated atomically during run |
| Ambiguous timeout | `timeout_type: compile / test / total` in JSON; two-phase execution separates compile and test deadlines |
| No regression tracking | `--compare-run` populates `new_failures` |
| Platform path differences | Absolute + relative paths in every response |
| No test discovery without running | `fastplay list` static-scans `[Test]` and `[UnityTest]` attributes |

## Installation

```bash
git clone https://github.com/Kubonsang/testplay-runner.git
cd testplay-runner
go build -o fastplay ./cmd/fastplay
```

Or cross-compile:

```bash
GOOS=windows GOARCH=amd64 go build -o fastplay.exe ./cmd/fastplay
```

## Configuration

Create `fastplay.json` in your project root:

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
  "result_dir": ".fastplay/results"
}
```

`unity_path` falls back to the `UNITY_PATH` environment variable if omitted.
`project_path` defaults to the directory containing `fastplay.json`.
`test_platform` accepts `"edit_mode"` (default) or `"play_mode"`. This is passed as `-testPlatform EditMode|PlayMode` to Unity.

**Timeout configuration:**
- `total_ms` (default 300000): outer safety-net deadline for the entire run.
- `compile_ms` + `test_ms`: when both are set, enables two-phase execution — Unity runs compile-only first (`compile_ms` deadline), then runs tests (`test_ms` deadline). Timeouts emit `timeout_type: "compile"` or `"test"` instead of `"total"`.
- When only `total_ms` is set, single-phase execution is used.

> **Note:** PlayMode network harness and NGO orchestration are not yet supported.

## Commands

### `fastplay check`

Validates Unity path, project path, and config before running. Run this first.

```bash
fastplay check
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
  "hint": "set UNITY_PATH or add unity_path to fastplay.json"
}
```

Exit 0 = ready. Exit 1 = dependency missing (fix per `hint`). Exit 5 = config invalid.

---

### `fastplay list`

Static scan of `*.cs` files for `[Test]` and `[UnityTest]` attributes. Returns candidate test names without running Unity. The list may be incomplete (e.g. `[TestCase]`, `[Theory]` are not detected).

```bash
fastplay list
```

```json
{
  "schema_version": "1",
  "tests": ["MyTests.PlayerTests.TestJump", "MyTests.PlayerTests.TestRun"]
}
```

---

### `fastplay run`

Runs Unity tests using the configured `test_platform` (`edit_mode` or `play_mode`). Streams progress to `fastplay-status.json`.

```bash
fastplay run
fastplay run --filter TestJump
fastplay run --category Smoke
fastplay run --compare-run 20250301-102200
```

**All tests pass (exit 0):**

```json
{
  "schema_version": "1",
  "run_id": "20250325-143000",
  "total": 10,
  "passed": 10,
  "failed": 0,
  "tests": [],
  "new_failures": null
}
```

**Test failure (exit 3):**

```json
{
  "schema_version": "1",
  "run_id": "20250325-143000",
  "total": 10,
  "passed": 9,
  "failed": 1,
  "tests": [
    {
      "name": "MyTests.PlayerTests.TestJump",
      "result": "Failed",
      "message": "Expected 1 but was 0",
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
  "run_id": "20250325-143000",
  "errors": [
    {
      "file": "Assets/Scripts/Player.cs",
      "absolute_path": "/path/to/UnityProject/Assets/Scripts/Player.cs",
      "line": 17,
      "message": "CS0103: The name 'speed' does not exist in the current context"
    }
  ]
}
```

---

### `fastplay result`

Lists stored run history. Never re-runs Unity.

```bash
fastplay result
fastplay result --last 3
```

```json
{
  "schema_version": "1",
  "runs": [
    {"run_id": "20250325-143000", "exit_code": 0, "total": 10, "passed": 10, "failed": 0},
    {"run_id": "20250324-091500", "exit_code": 3, "total": 10, "passed": 9, "failed": 1}
  ]
}
```

## Exit Codes

| Code | Meaning | Action |
|---|---|---|
| 0 | All tests passed | Proceed |
| 1 | Unity / project not found | Fix env, check `hint` field |
| 2 | Compile failure | Fix source, see `errors[].absolute_path` + `line` |
| 3 | Test failure | Fix test, see `tests[].absolute_path` + `line` |
| 4 | Timeout or signal interruption | Check `timeout_type: "total"`; signal interruption (`fastplay-status.json` phase `interrupted`) also returns exit 4 |
| 5 | Config error | Fix or create `fastplay.json` |
| 6 | Build failure (not yet returned) | Check Unity license / build target |
| 7 | Permission error (not yet returned) | Fix path permissions |

## Progress Monitoring

During `fastplay run`, poll `fastplay-status.json` to track progress:

```json
{
  "schema_version": "1",
  "phase": "running",
  "run_id": "20250325-143000",
  "current_test": "MyTests.PlayerTests.TestJump",
  "total": 10,
  "passed": 3,
  "failed": 0,
  "updated_at": "2025-03-25T14:30:05Z"
}
```

Phase progression: `waiting → compiling → running → done`
Failure phases: `timeout_total`, `interrupted`

## Recommended Agent Flow

```
1. fastplay check          # Validate environment
2. fastplay list           # Discover test names
3. fastplay run            # Execute (poll fastplay-status.json for progress)
4. fastplay result --last 3  # Review run history
```

## Development

```bash
# Run all tests with race detector
go test -race ./...

# Run integration tests
go test -tags=integration ./cmd/fastplay/...

# Build for current platform
go build ./cmd/fastplay
```

## License

Apache 2.0 — see [LICENSE](LICENSE).
Third-party notices — see [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES).
