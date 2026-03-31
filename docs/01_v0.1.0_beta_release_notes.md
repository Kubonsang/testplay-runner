# FastPlay Runner v0.1.0-beta — Release Notes

Released: 2026-03-27

---

## What Is This

FastPlay Runner (`fastplay`) is a thin Go CLI wrapper around Unity's test runner.
It makes Unity tests usable from AI agents by solving eight concrete problems:
unreliable exit codes, XML-only results, ambiguous compile vs. test failures,
no progress visibility, no pre-validation, and platform path differences.

Agents interact through five commands: `version`, `check`, `list`, `run`, `result`.
All stdout is JSON. All human-readable output goes to stderr.

---

## What Is Supported in This Release

- **EditMode and PlayMode test execution** — single-process Unity invocation
- **Structured JSON output** for all five commands (`version`, `check`, `list`, `run`, `result`)
- **Compile vs. test failure disambiguation** — exit code 2 (compile) vs. exit code 3 (test)
- **Phase-aware timeouts** — separate `compile_ms` / `test_ms` deadlines in two-phase mode; emits `timeout_compile`, `timeout_test`, `timeout_total`
- **Artifact persistence** — per-run `results.xml`, `stdout.log`, `stderr.log`, `summary.json`, `manifest.json`, `events.ndjson` under `.fastplay/runs/<run_id>/`
- **Run status streaming** — atomic writes to `fastplay-status.json` during `run`; includes `started_at`, `last_heartbeat_at`, `artifact_root`
- **Regression detection** — `--compare-run` flag on `fastplay run` populates `new_failures`
- **Orphan process prevention** — Unity child processes killed as a group on timeout/signal (Linux/macOS)

---

## What Is NOT Supported in This Release

> These are hard boundaries. Do not assume they work — they do not.

- **NGO (Netcode for GameObjects) orchestration** — not supported
- **Multi-process host/client/server test harness** — not supported; `Execute` starts a single Unity process per invocation
- **Full network harness** — not supported; network-dependent tests require manual orchestration outside fastplay
- **Exit code 8 (signal interruption)** — SIGINT/SIGTERM currently returns exit 4 with no `timeout_type`; agents cannot distinguish timeout from signal at the exit code level
- **Exit code 6 (build/license failure)** and **exit code 7 (permission error)** — documented but not yet returned
- **`--config` flag** — `fastplay.json` is always loaded from cwd; agents must `cd` to the project root

---

## Known Limitations

| Area | Issue |
|---|---|
| `list` scanner | Detects `[Test]` and `[UnityTest]` only; `[TestCase]`, `[Theory]` missed |
| Phase detection | `running` written after Unity exits, not when tests start |
| runID collision | 1-second timestamp granularity; concurrent runs may collide |
| Signal exit code | SIGINT/SIGTERM returns exit 4, not exit 8 |
| Config path | Always reads `fastplay.json` from cwd |

---

## Installation

```bash
# macOS/Linux
go build -o fastplay ./cmd/fastplay

# Windows
GOOS=windows GOARCH=amd64 go build -o fastplay.exe ./cmd/fastplay

# With version metadata
go build -ldflags="-X main.version=v0.1.0-beta -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%Y-%m-%d)" -o fastplay ./cmd/fastplay
```

Verify installation:

```bash
fastplay version
# {"schema_version":"1","version":"v0.1.0-beta"}
```

---

## Quick Start

```bash
fastplay version       # print version
fastplay check         # validate environment
fastplay list          # scan for test names
fastplay run           # run tests
fastplay result        # review run history
```

See README for the full agent flow.
