# ADR 0001 — Warm-editor bridge cancellation: abandon-and-timeout, not bridge-side hard cancel

- **Status:** Accepted (v0.10.0)
- **Date:** 2026-06-23
- **Context source:** code review of PR #30 (warm-editor bridge), finding "#4 — a hung warm test pins the bridge".

> 한 줄 요약: warm 브릿지의 취소는 **Go가 abandon-and-timeout으로 소유**하고, C#은 실행 중인 TestRunnerApi run을 강제 중단하지 **않는다**. 대신 active run이 있으면 handshake가 busy로 보고되어, orphan/hung run이 레이스 없이 **안전하게 cold로 degrade**된다. 브릿지측 hard cancellation은 의도적으로 deferred.

## Context

`TestRunnerApi` exposes no documented way to cleanly cancel an EditMode run mid-flight. When `testplay run --bridge` is interrupted (SIGINT) or hits `total_ms`, the Go side must return promptly with the correct exit code, but the warm Editor keeps executing the run it already started.

The code review raised a real consequence: if a test **hangs** (never fires `RunFinished`), `active_run_id` stays set, the Editor reports `running_tests` forever, and every subsequent `testplay run` falls back to cold for the rest of the Editor session.

The obvious "fix" — have the C# server force-abandon the run when it sees the Go-written `.cancel` marker (clear `active`, null the API/controller) — was considered and **rejected**: clearing `active` lets the next request start a *second* `TestRunnerApi.Execute` on the same Editor while the hung run is still executing. Concurrent runs on one Editor are unsupported/undefined and would make the wedge worse, not better.

## Decision

1. **Go owns cancellation (abandon-and-timeout).** On `ctx` cancel / deadline, Go returns the same exit `8` (signal) / `4` (timeout) a cold run returns, writes a best-effort `requests/<run>.cancel` marker, and stops waiting. The exit code and status are byte-identical to cold regardless of whether the Editor actually stopped. (Unit-tested; validated end-to-end on Unity 6 — see `docs/25`.)
2. **The C# bridge does NOT force-abandon a running run.** `AdvanceRunning` does not act on `.cancel` to tear down a live `TestRunnerApi` run, because that would risk a concurrent second run.
3. **Safety comes from honest state, not from stopping the run.** The handshake reports the Editor as **busy whenever `SessionState` holds an `active_run_id`** (even after a domain reload reset the in-memory flag), and the Go `Probe` rejects an idle handshake that still carries a non-empty `active_run_id`. So an orphaned/hung run cleanly **degrades subsequent runs to cold** (no race), instead of being raced onto.
4. **Bridge-side hard (mid-test) cancellation is deferred** to the PlayMode-warm / scenario-warm work, where a domain-reload-based reset can be designed safely.

## Consequences

**Positive**
- No concurrent-run hazard on a single Editor.
- Exit codes / status are always correct, identical to cold, on cancel and timeout.
- A hung or long-running warm test degrades **safely** — the next run falls back to cold rather than racing or producing a wrong result.

**Negative (accepted limitation)**
- A genuinely **hung** warm test disables the warm path for that Editor session until the Editor is restarted.
- A new run issued *while a long-but-finishing* warm test is still executing falls back to cold (the warm path is briefly unavailable, not wrong).

**Future / tracked**
- A running-phase deadline that, after a generous bound, force-resets `active` *and* requests a fresh domain reload could un-wedge a hung run safely. Tracked in the bridge-hardening issue (do not implement as a naive `.cancel` force-abandon — see Decision #2).

## Alternatives considered

| Alternative | Verdict |
|---|---|
| Force-abandon the run on `.cancel` (clear active, null api) | **Rejected** — reintroduces concurrent `TestRunnerApi.Execute` on one Editor for the hung case. |
| Hard-kill / restart the Editor on cancel | **Rejected** — too destructive; the Editor is a shared, user-owned GUI. |
| Poll `TestRunnerApi` for a cancellation API | **Not available** — no documented clean cancel. |
| Do nothing (pre-review behavior) | **Rejected** — `editor_state` could read idle while a run was active, allowing a new run to race onto a busy bridge. Fixed by Decision #3. |
