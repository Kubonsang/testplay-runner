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

## Native HoneyBee v0.6 evidence

The real Windows E2E gate passed on 2026-08-19 from TestPlay feature commit
`8d41609` and HoneyBee commit
`2ce6087b9a81f33b604efcaaedc836347399ad66`:

- verdict: `HONEYBEE_PROTOCOL3_E2E_PASS`;
- TestPlay executable commit: `ef92a736a4743c22e9e5d956454db14e9d491bb1`;
- TestPlay executable SHA-256:
  `7A25823E75FF458D2353A371091F6982347BD928196CFB2E6D6406F05CE1A2FC`;
- bridge package tree SHA-256:
  `4746D75CBC485421C0C3F3EC580376CB5BB6888C6FC0BC4E0CD17ED02B0CF753`;
- HoneyBee runtime tree SHA-256:
  `23AC5FF5B6C5F2EDBA2DA91868A9FAEB32CF0F754572E528BFFD9B8344143F7C`;
- one owned Editor identity was preserved across `compile` and `warm-test`:
  protocol 3, one exact workspace ID, PID, and bridge session;
- compile passed with zero compile errors and did not execute tests;
- warm-test passed `1/1`, used the bridge backend, and did not fall back;
- the physical source trees and immutable parent VHDX SHA-256 were unchanged;
- the workspace release completed, the temporary broker uninstalled, and the
  previous broker identity was restored;
- active, retained, pending, and quarantined children were zero; file-backed
  disk and related-process residuals were zero; cleanup was `released`.

Artifact ZIP:

```text
C:\Users\user\AppData\Local\Temp\testplay-honeybee-protocol3-e2e-20260819-121300-676.zip
```

Artifact SHA-256:

```text
7C46F8F0E9EAB73EFF7F143D6F5799B428D82BE62D3F0C122CC566CAC590C2E4
```

This proves the scoped HoneyBee protocol-3 transaction on the tested Windows
host. Forced-termination recovery, broader hardware compatibility, and
production readiness remain separate v0.14 gates.
