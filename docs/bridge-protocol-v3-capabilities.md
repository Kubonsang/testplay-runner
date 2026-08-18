# Bridge protocol 3 capabilities (v0.14 development)

This feature is developed independently from the frozen `v0.13.0-rc.1`
release. It does not change the v0.13 release candidate, its observation
window, or the stable v0.13 promotion path.

## Compatibility

- Ordinary `testplay run --bridge` accepts both protocol 2 and protocol 3
  handshakes. It preserves the existing cold fallback contract.
- `testplay capability compile` and `testplay capability warm-test` require
  protocol 3 and never fall back to a cold Unity process.
- A capability request is bound to one exact `workspace_id`,
  `bridge_session_id`, and `editor_pid`. The request, response, cancellation,
  and tombstone documents carry that identity.
- The Unity package captures `HONEYBEE_WORKSPACE_ID` when its editor domain
  starts. A non-empty HoneyBee workspace identity activates the otherwise
  dormant bridge for that owned Editor session. HoneyBee capability commands
  reject an empty workspace identity.

## Commands

```text
testplay capability compile \
  --config C:\absolute\testplay.json \
  --require-bridge-session SESSION \
  --require-editor-pid PID \
  --workspace-id WORKSPACE \
  --no-fallback

testplay capability warm-test \
  --config C:\absolute\testplay.json \
  --require-bridge-session SESSION \
  --require-editor-pid PID \
  --workspace-id WORKSPACE \
  --filter FULL_NAME \
  --no-fallback
```

`compile` performs a synchronous asset refresh/import and waits for domain
compilation to settle. It never invokes Unity TestRunner. `warm-test` uses the
owned TestRunner bridge and fails when the frozen selection executes zero
tests.

Exit codes remain compatible with the public CLI contract: `0` success, `2`
compile failure, `3` test failure, `6` infrastructure/build/identity failure,
and `9` indeterminate execution. Capability results and their manifest are
written below `<project>/.testplay/runs/<run-id>`.

## Promotion gate

This branch is not production-ready until a real HoneyBee v0.6 transaction
passes workspace acquisition, owned editor launch, exact protocol-3 binding,
compile, warm test with `total >= 1`, immutable-source/parent checks, release,
and zero broker/disk/mount/process residual. Forced-termination and broker
recovery remain additional v0.14 gates.

The native gate is implemented by
`scripts/run-honeybee-protocol-v3-e2e.ps1`. It requires an elevated
PowerShell and an explicit `-InstallApproved` switch. When another TestPlay
broker is already installed, the harness will proceed only when that broker
has no active, retained, pending, or quarantined child. It then:

1. records the exact old receipt, executable, parents, and SHA-256 values;
2. performs `storage uninstall --preserve-data` and moves only the exact
   install receipt aside;
3. installs the pinned development binary into a new unique store;
4. provisions a new parent containing the protocol-3 Unity package;
5. runs a real HoneyBee v0.6 transaction and verifies both capability
   `summary.json` objects through HoneyBee's content-addressed artifact store;
6. removes the new store through the broker CLI; and
7. restores the old receipt and broker with the exact old executable.

The harness never manually deletes a VHDX. If the temporary broker has
protected or uncertain state, it preserves that state and does not replace it
with the old broker identity. A successful run requires the original parent
hashes to remain unchanged and the final disk, drive-letter, workspace, and
non-service process residual to be zero.
