# TestPlay Bridge (`com.testplay.bridge`)

Warm-editor backend for the [TestPlay Runner](../../README.md) CLI (`testplay`).

When your Unity Editor is open, `testplay run` normally falls back to a **shadow
workspace**: it byte-copies `Assets/` + `ProjectSettings/` + the cached
`Library/` into `.testplay-shadow-<run_id>/` and cold-starts batchmode there
(disk + time cost). This package lets `testplay` instead run tests **in the
already-warm Editor** via the `TestRunnerApi`, writing the *same* NUnit
`results.xml` — so the CLI's JSON/exit-code contract is unchanged while the copy
and the cold domain reload are eliminated.

It is a **transparent backend**: agents and CI see identical output, plus a
`backend: "bridge"` field disclosing which engine ran.

## Requirements

- Unity 2021.3+ with the Test Framework package (`com.unity.test-framework`).
- TestPlay Runner v0.10.0+.

## Install (in-repo UPM)

Add to your project's `Packages/manifest.json` (path or git dependency):

```json
{
  "dependencies": {
    "com.testplay.bridge": "file:../path/to/FastPlay_Runner/unity/com.testplay.bridge"
  }
}
```

The C# protocol version is kept in lockstep with the Go CLI in this repo; install
the package version matching your `testplay` version.

## Opt-in (dormant by default)

Installing the package alone changes **nothing**. The bridge activates only when
opted in, and **never** in batchmode:

- **Environment variable** (developer dogfood): launch the Editor with
  `TESTPLAY_BRIDGE_ENABLE=1`.
- **Project sentinel** (declarative): create an empty file at
  `<project>/.testplay/bridge/ENABLE` (e.g. `mkdir -p .testplay/bridge && touch .testplay/bridge/ENABLE`).

When active, the Editor writes a heartbeat to `<project>/.testplay/bridge/handshake.json`.
`testplay run` probes it and, if a live, compatible, idle bridge is present and
the **Pristine Gate** passes, routes the run through the Editor. Otherwise it
falls back to the cold shadow/process path automatically — correctness wins by
default.

## Scope (v0.10.0)

- **EditMode** tests only. PlayMode requests are refused (run cold).
- One run at a time; concurrent `testplay run`s degrade gracefully (one warm, one
  shadow).
- `compile_ms` + `test_ms` (two-phase) configs always run cold.

## Correctness (Pristine Gate)

A warm result is returned only when the warm domain is equivalent to a fresh cold
domain for the code under test. The bridge:

- **refuses** (→ cold) in Play Mode or for PlayMode requests;
- **waits** for compilation/import to settle (bounded), running compile then;
- reports compile errors as the same `exit 2` + `errors[]` a cold run would;
- **discloses** non-result-changing states (e.g. unsaved scenes) via `warnings`,
  and never auto-saves your editor.

See the repo `RELEASE-PLAN.md` (v0.10.0) for the validation spikes that gate this
behavior.
