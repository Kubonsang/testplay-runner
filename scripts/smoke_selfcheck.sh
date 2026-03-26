#!/usr/bin/env bash
# smoke_selfcheck.sh — regression guard for smoke.sh shell helpers
#
# Verifies the three failure-path properties that have broken before:
#   1. json_str / json_num parse pretty JSON correctly.
#   2. json_str / json_num return empty string (not die) on no-match.
#   3. `cmd=$(...) || status=$?` captures non-zero exit without killing
#      the script, leaving diagnostic code reachable.
#
# Run standalone:
#   bash scripts/smoke_selfcheck.sh
#
# Add to CI by calling this script before smoke.sh.
# No external dependencies — bash and the helpers defined below are enough.

set -euo pipefail

PASS=0
FAIL=0

# ── Helpers under test ───────────────────────────────────────────────────────
# Source the shared helpers so selfcheck always tests the live implementation.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=smoke_helpers.sh
source "$SCRIPT_DIR/smoke_helpers.sh"

# ── Test runner ───────────────────────────────────────────────────────────────

ok() {
  echo "  PASS: $1"
  PASS=$(( PASS + 1 ))
}

fail() {
  echo "  FAIL: $1" >&2
  FAIL=$(( FAIL + 1 ))
}

assert_eq() {
  local desc="$1" got="$2" want="$3"
  if [[ "$got" == "$want" ]]; then
    ok "$desc"
  else
    fail "$desc — got [$got], want [$want]"
  fi
}

# ── Test cases ────────────────────────────────────────────────────────────────

PRETTY_JSON='{
  "schema_version": "1",
  "run_id": "20260326-120000",
  "total": 5,
  "passed": 5,
  "failed": 0,
  "exit_code": 0,
  "new_failures": null
}'

COMPACT_JSON='{"schema_version":"1","run_id":"20260326-130000","exit_code":3}'

echo "── json_str ─────────────────────────────────────────────────────────────"

assert_eq "pretty: run_id" \
  "$(json_str "$PRETTY_JSON" "run_id")" \
  "20260326-120000"

assert_eq "compact: run_id" \
  "$(json_str "$COMPACT_JSON" "run_id")" \
  "20260326-130000"

assert_eq "missing field returns empty (no crash)" \
  "$(json_str "$PRETTY_JSON" "nonexistent_field")" \
  ""

echo ""
echo "── json_num ─────────────────────────────────────────────────────────────"

assert_eq "pretty: exit_code 0" \
  "$(json_num "$PRETTY_JSON" "exit_code")" \
  "0"

assert_eq "pretty: total" \
  "$(json_num "$PRETTY_JSON" "total")" \
  "5"

assert_eq "compact: exit_code 3" \
  "$(json_num "$COMPACT_JSON" "exit_code")" \
  "3"

assert_eq "missing field returns empty (no crash)" \
  "$(json_num "$PRETTY_JSON" "nonexistent_field")" \
  ""

echo ""
echo "── non-zero command capture ──────────────────────────────────────────────"

# Simulate fastplay run failing: || cmd_status=$? must capture the status
# without set -e killing the script, leaving the diagnostic block reachable.
_fake_fail() { printf '{"error":"unity not found"}\n'; return 2; }

_captured_output=""
_captured_status=0
_captured_output=$(_fake_fail) || _captured_status=$?

assert_eq "non-zero exit captured in cmd_status" \
  "$_captured_status" \
  "2"

assert_eq "output still captured despite non-zero exit" \
  "$(json_str "$_captured_output" "error")" \
  "unity not found"

# ── Summary ───────────────────────────────────────────────────────────────────

echo ""
echo "Results: $PASS passed, $FAIL failed."

if [[ "$FAIL" -ne 0 ]]; then
  exit 1
fi
