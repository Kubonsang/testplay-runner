# FastPlay Runner v0.2.0-beta — Release Notes

Target Release: v0.2.0-beta
Blueprint: [Core v0.2.2 Architecture Blueprint](blueprint-v0.2.2.md)
Validation Evidence: [GNF_ Shadow PlayMode Smoke Validation](v0.2.0-beta-gnf-shadow-validation.md)

---

## What's New

### Shadow Workspace — Run Tests While the Editor Is Open

v0.1 required the Unity Editor to be closed before running `fastplay run`. Opening the same project in batch mode while the Editor held the lock caused Unity to exit immediately with no useful output.

v0.2 eliminates this restriction.

`fastplay run` now automatically detects whether the Editor is open and selects the appropriate execution backend:

| Condition | Backend | Behavior |
|---|---|---|
| Editor closed (`Temp/UnityLockfile` absent) | **Direct Batch** | Unchanged from v0.1. Zero overhead. |
| Editor open (`Temp/UnityLockfile` present) | **Shadow Workspace** | Isolated copy of the project built under `.fastplay-shadow/`, Unity batch runs there. |

The selection is fully automatic. No flags, no config changes. The command is still:

```bash
fastplay run
```

All output — exit codes, stdout JSON, `fastplay-status.json`, artifacts — is identical regardless of which backend ran. Shadow is an implementation detail.

---

## How Shadow Workspace Works

When the Editor lock is detected, fastplay constructs an isolated workspace at `.fastplay-shadow/` before invoking Unity:

| Directory | Strategy | Reason |
|---|---|---|
| `Assets/` | Physical copy | Prevents Unity from writing `.meta` files back to the source |
| `ProjectSettings/` | Physical copy | Prevents Unity from modifying source project settings |
| `Packages/` | Symlink / Junction | Write probability is negligible; link to avoid copy cost |
| `Library/` | Shadow-only, **persisted** | Reused across runs; only incremental recompile on subsequent runs |
| `Temp/` | Shadow-only, deleted each run | Recreated fresh every run |
| `UserSettings/` | Excluded | Editor personal settings; not needed for test execution |

Artifacts (`results.xml`, `stdout.log`, etc.) and `fastplay-status.json` are written directly to the source project path — not the shadow — so your project layout is unchanged after the run.

All shadow paths in stdout JSON are remapped to source paths before output. The shadow directory is never visible in any fastplay output.

### Persistent Library Cache

The `Library/` directory inside `.fastplay-shadow/` is preserved between runs. The first Shadow run reimports all assets (slow). Subsequent runs reuse the cache and only recompile changed scripts.

### .gitignore

`.fastplay-shadow/` is automatically appended to your project's `.gitignore` on first Shadow run if not already present.

---

## New in This Release

- **Auto backend selection** — Direct Batch or Shadow Workspace chosen automatically based on lock state
- **`fastplay run --reset-shadow`** — force-deletes `.fastplay-shadow/` and rebuilds from scratch; use when the Unity version changes or the cache becomes stale
- **Shadow-only Unity flags** — `-nographics` and `-disable-assembly-updater` injected for Shadow runs to reduce RAM/CPU overhead

---

## What Is NOT Supported in This Release

> These are hard boundaries. Do not assume they work — they do not.

- **NGO (Netcode for GameObjects) orchestration** — not supported
- **Multi-process host/client/server test harness** — not supported
- **Full network harness** — not supported; network-dependent tests require manual orchestration outside fastplay
- **Exit code 8 (signal interruption)** — SIGINT/SIGTERM returns exit 4 with no `timeout_type`
- **Exit code 6 (build/license failure)** and **exit code 7 (permission error)** — documented but not yet returned
- **`--config` flag** — `fastplay.json` always loaded from cwd
- **Unsaved editor changes reflected in Shadow** — Shadow copies saved files only; uncommitted or unsaved editor state is not included

---

## Known Limitations

| Area | Issue |
|---|---|
| `list` scanner | Detects `[Test]` and `[UnityTest]` only; `[TestCase]`, `[Theory]` missed |
| Phase detection | `running` written after Unity exits, not when tests start |
| runID collision | 1-second timestamp granularity; concurrent runs may collide |
| Signal exit code | SIGINT/SIGTERM returns exit 4, not exit 8 |
| Config path | Always reads `fastplay.json` from cwd |
| Shadow first run | Full asset reimport on first Shadow invocation; may be slow for large projects |
| `Packages/` write-through | Junction/symlink; if Unity modifies a package manifest, the source is updated |

---

## What Is Stable

The Direct Batch path — used in all v0.1 workflows and all CI environments where the Editor is not open — is **unchanged**. No behavior, output format, or exit code has been modified for the Direct Batch backend.

If you do not use Shadow Workspace, v0.2 is a drop-in replacement for v0.1.

---

## Installation

```bash
# macOS/Linux
go build -o fastplay ./cmd/fastplay

# Windows
GOOS=windows GOARCH=amd64 go build -o fastplay.exe ./cmd/fastplay

# With version metadata
go build -ldflags="-X main.version=v0.2.0-beta -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%Y-%m-%d)" -o fastplay ./cmd/fastplay
```

Verify:

```bash
fastplay version
# {"schema_version":"1","version":"v0.2.0-beta"}
```

---

## Quick Start

```bash
fastplay check         # validate environment (reports lock state)
fastplay list          # scan for test names
fastplay run           # runs Direct Batch or Shadow automatically
fastplay result        # review run history

# If Shadow cache becomes stale:
fastplay run --reset-shadow
```

---

## Feedback

Shadow Workspace is the primary new surface in this beta. Reports of interest:

- Shadow runs that produce different results than Direct Batch on the same code
- `.fastplay-shadow/` growing unexpectedly large
- `--reset-shadow` not fully resolving a stale cache
- Artifact paths appearing as shadow paths in any output field

File issues at: https://github.com/Kubonsang/testplay-runner/issues
