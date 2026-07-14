# ADR 0002 — Warm-editor bridge ownership: prove, cancel, then publish

- **Status:** Accepted (v0.11.0)
- **Date:** 2026-07-14
- **Supersedes:** [ADR 0001](0001-warm-bridge-cancellation.md)

## Context

Unity Test Framework callbacks are global to the Editor. Registration time and
callback order do not prove that a `RunFinished` result belongs to the request
that testplay submitted. Domain reload can also erase in-memory observers while
Unity resumes serialized test work. Finally, `RunFinished` is followed by Test
Framework cleanup and scene restoration, where a later error can still occur.

The unsafe failure mode is duplicate side effects: if a warm request might have
started and transport or Editor state becomes ambiguous, falling back to a new
cold run can execute the same tests twice. For agent automation, an explicit
unknown result is safer than a plausible but unproven success.

## Decision

1. **Protocol 2 binds each request to one Editor session.** The handshake has a
   `bridge_session_id`, and every request must carry that exact ID. A request
   left by an older Editor session is sealed with a durable tombstone instead
   of being executed.
2. **The Test Framework run GUID is the ownership proof.** The controller keeps
   the GUID returned by `TestRunnerApi.Execute` and accepts terminal callbacks
   only for that owned run. Foreign human or package-initiated runs cannot
   complete a testplay request.
3. **Cancellation is scoped to the owned run.** Go may return on deadline or
   signal, but the C# side retains ownership until the Test Framework reports
   the run inactive. It does not start a second run while the first may exist.
4. **`RunFinished` begins completion; it does not publish it.** The response is
   first written to a private pending file. The bridge waits for two
   authoritative inactive observations, covering the cleanup frame, before it
   exposes the terminal response and advertises idle.
5. **Durable markers carry execution state.** Terminal failures and cancellation
   markers record `not_started` or `possibly_started`. Missing, malformed, and
   legacy markers are treated conservatively as `possibly_started`.
6. **Only `not_started` may cold-fallback.** `possibly_started` is terminal exit
   9 (`runner system error`) and is never replayed. A completed response whose
   XML is missing or invalid follows the same rule.
7. **There is one terminal status.** Indeterminate paths write exactly one
   `done`/`run_finished` event with `exit_code: 9`; intermediate transport errors
   cannot create a second terminal event.
8. **Domain reload is fail-closed.** Session state and durable pending/cancel
   markers restore enough ownership state to resume safely. If authoritative
   completion cannot be reconstructed, the request becomes indeterminate rather
   than successful or replayable.

## Consequences

**Positive**

- A foreign Test Runner run cannot be captured as a testplay result.
- Editor restart cannot replay an old-session request.
- Timeout, cancellation, cleanup errors, missing XML, and publish failures do
  not cause duplicate test execution.
- Warm and cold selection remains deterministic and machine-readable.

**Negative**

- Some infrastructure failures now return exit 9 even when tests may have
  completed successfully; this uncertainty is intentional.
- Protocol 2 requires the v0.11 CLI and Unity package to be upgraded together.
- A genuinely hung Unity test can keep the warm bridge busy until the Editor is
  restarted; v0.11 still does not hard-abort undocumented Test Framework state.

## Rejected alternatives

| Alternative | Reason rejected |
|---|---|
| Accept the first global `RunFinished` callback | Registration and callback order do not prove ownership. |
| Publish immediately from `RunFinished` | Cleanup and scene restore can still report a later failure. |
| Treat lost transport as `not_started` | It can duplicate side effects through cold fallback. |
| Clear active state on cancel and start another run | Two concurrent Test Framework runs in one Editor are unsupported. |
| Let AI infer whether the run probably passed | The runner contract requires deterministic evidence, not semantic guessing. |
