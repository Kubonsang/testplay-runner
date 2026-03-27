# Beta Smoke Evidence

> **Note:** This file must be filled in by a team member with access to a Unity installation
> before the beta tag is created. AI agents cannot perform this step.

## Purpose

Records that `fastplay run` was executed against a real Unity project at least once
before the beta release. Provides a traceable reference for users who ask "was this actually tested?"

---

## How to Fill This In

1. Install fastplay: `go build -o fastplay ./cmd/fastplay`
2. `cd` to a Unity project directory containing `fastplay.json`
3. Run `fastplay check` — confirm `ready: true`
4. Run `fastplay run` — wait for completion
5. Fill in the table below, then commit this file

---

## Run Record

| Field | Value |
|---|---|
| Date | _(e.g. 2026-03-27)_ |
| Tester | _(name or handle)_ |
| fastplay version | _(output of `fastplay version`)_ |
| Unity version | _(e.g. 2022.3.14f1)_ |
| OS | _(e.g. macOS 14.4 arm64)_ |
| Test platform | _(edit\_mode / play\_mode)_ |
| Filter used | _(empty if none)_ |
| Exit code | _(0 / 2 / 3)_ |
| Total / Passed / Failed | _(e.g. 42 / 42 / 0)_ |

## Artifacts Produced

List the files present under `.fastplay/runs/<run_id>/` after the run:

```
<run_id>/
  results.xml      — ✅ / ❌
  summary.json     — ✅ / ❌
  manifest.json    — ✅ / ❌
  stdout.log       — ✅ / ❌
  stderr.log       — ✅ / ❌
```

## Notes

_(Any unexpected behaviour, environment quirks, or deviations from the documented flow.)_

---

*Template created 2026-03-27. Fill in before tagging v0.1.0-beta.*
