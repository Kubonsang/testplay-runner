#!/usr/bin/env bash
# smoke.sh — local smoke verification for TestPlay Runner
#
# Usage:
#   UNITY_PATH=/path/to/Unity ./scripts/smoke.sh
#
# Options (env vars):
#   UNITY_PATH   Required. Path to Unity binary.
#   TESTPLAY     Path to testplay binary (default: ./testplay built from source)
#   SMOKE_DIR    Path to the smoke Unity project (default: ./fixtures/smoke-project)
#
# What this tests:
#   1. EditMode smoke: testplay run → exit 0, all 6 run artifacts present
#   2. PlayMode smoke: testplay run → same artifacts, test_platform=play_mode
#
# Artifacts verified per run (inside .testplay/runs/<run_id>/):
#   results.xml, summary.json, manifest.json, stdout.log, stderr.log, events.ndjson
# Snapshot (in smoke project root, outside run artifact dir):
#   testplay-status.json
#
# The script exits non-zero if any check fails.
# Dependencies: bash, grep, sed, go — no python3 or jq required.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SMOKE_DIR="${SMOKE_DIR:-$REPO_ROOT/fixtures/smoke-project}"
TESTPLAY="${TESTPLAY:-}"

# ── Prerequisites ────────────────────────────────────────────────────────────

if [[ -z "${UNITY_PATH:-}" ]]; then
  echo "ERROR: UNITY_PATH must be set to the Unity binary path." >&2
  echo "       Example: UNITY_PATH=/Applications/Unity/Hub/Editor/2022.3.0f1/Unity.app/Contents/MacOS/Unity" >&2
  exit 1
fi

if [[ ! -x "$UNITY_PATH" ]]; then
  echo "ERROR: Unity binary not executable: $UNITY_PATH" >&2
  exit 1
fi

# Verify shell helpers before doing any real work.
echo "==> Running smoke helper self-check..."
bash "$SCRIPT_DIR/smoke_selfcheck.sh"
echo ""

# Build testplay if not provided.
if [[ -z "$TESTPLAY" ]]; then
  echo "==> Building testplay..."
  go build -o "$REPO_ROOT/testplay" "$REPO_ROOT/cmd/testplay"
  TESTPLAY="$REPO_ROOT/testplay"
fi

if [[ ! -x "$TESTPLAY" ]]; then
  echo "ERROR: testplay binary not found or not executable: $TESTPLAY" >&2
  exit 1
fi

echo "==> Using testplay:   $TESTPLAY"
echo "==> Using Unity:      $UNITY_PATH"
echo "==> Smoke project:    $SMOKE_DIR"
echo ""

# ── Shared helpers (json_str, json_num) ──────────────────────────────────────
# shellcheck source=smoke_helpers.sh
source "$SCRIPT_DIR/smoke_helpers.sh"

# ── Helper: assert a parsed field is non-empty ────────────────────────────────
# Usage: assert_field <stage> <field_name> <value> <raw_json>
# Exits immediately with a diagnostic if the field is empty.
assert_field() {
  local stage="$1" field="$2" value="$3" raw="$4"
  if [[ -z "$value" ]]; then
    echo "  ERROR [$stage]: field '$field' is empty — JSON parsing failed." >&2
    echo "  Raw testplay output:" >&2
    printf '%s\n' "$raw" | sed 's/^/    /' >&2
    exit 1
  fi
}

# ── Helper: generate testplay.json for smoke project ─────────────────────────

write_config() {
  local platform="$1"
  cat > "$SMOKE_DIR/testplay.json" <<EOF
{
  "schema_version": "1",
  "unity_path": "$UNITY_PATH",
  "project_path": "$SMOKE_DIR",
  "test_platform": "$platform",
  "timeout": {
    "total_ms": 300000
  },
  "result_dir": ".testplay/results"
}
EOF
}

# ── Helper: verify all expected artifact files are present ───────────────────
# Usage: check_artifacts <stage> <run_id>
# Exits immediately if the artifact directory or any expected file is missing.
check_artifacts() {
  local stage="$1" run_id="$2"
  local artifact_dir="$SMOKE_DIR/.testplay/runs/$run_id"

  if [[ ! -d "$artifact_dir" ]]; then
    echo "  ERROR [$stage]: artifact directory not found: $artifact_dir" >&2
    echo "  Possible cause: run_id extraction failed or testplay did not create the run directory." >&2
    exit 1
  fi

  local missing=false
  # Run artifacts (inside .testplay/runs/<run_id>/)
  for f in results.xml summary.json manifest.json stdout.log stderr.log events.ndjson; do
    if [[ ! -f "$artifact_dir/$f" ]]; then
      echo "  MISSING [$stage]: $artifact_dir/$f" >&2
      missing=true
    fi
  done
  # Status snapshot (in project root, outside the run artifact dir)
  if [[ ! -f "$SMOKE_DIR/testplay-status.json" ]]; then
    echo "  MISSING [$stage]: $SMOKE_DIR/testplay-status.json (status snapshot)" >&2
    missing=true
  fi
  if [[ "$missing" == "true" ]]; then
    exit 1
  fi
}

# ── Smoke runner ──────────────────────────────────────────────────────────────
# Usage: run_smoke <stage_label> <platform>
run_smoke() {
  local stage="$1" platform="$2"

  echo "==> $stage ($platform)"
  write_config "$platform"

  echo "  testplay check..."
  "$TESTPLAY" check

  echo "  testplay run ($platform)..."
  local output cmd_status=0
  output=$("$TESTPLAY" run) || cmd_status=$?

  local run_id exit_code
  run_id=$(json_str "$output" "run_id")
  exit_code=$(json_num "$output" "exit_code")

  if [[ "$cmd_status" -ne 0 ]]; then
    echo "  ERROR [$stage]: testplay run exited with status $cmd_status" >&2
    echo "  run_id:    ${run_id:-(unparsed)}" >&2
    echo "  exit_code: ${exit_code:-(unparsed)}" >&2
    echo "  Raw testplay output:" >&2
    printf '%s\n' "$output" | sed 's/^/    /' >&2
    exit 1
  fi

  assert_field "$stage" "run_id"    "$run_id"    "$output"
  assert_field "$stage" "exit_code" "$exit_code" "$output"

  echo "  run_id:    $run_id"
  echo "  exit_code: $exit_code"

  echo "  Checking artifacts..."
  check_artifacts "$stage" "$run_id"
  echo "  OK"
  echo ""
}

# ── Shadow smoke runner ───────────────────────────────────────────────────────
# Usage: run_smoke_shadow <stage_label> <platform>
# Runs testplay run --shadow and verifies:
#   1. Exit 0 and standard run artifacts.
#   2. .testplay-shadow/ was created with expected subdirectories.
run_smoke_shadow() {
  local stage="$1" platform="$2"

  echo "==> $stage ($platform --shadow)"
  write_config "$platform"

  # Clean any pre-existing shadow workspace so the test is deterministic.
  rm -rf "$SMOKE_DIR/.testplay-shadow"

  echo "  testplay check..."
  "$TESTPLAY" check

  echo "  testplay run --shadow ($platform)..."
  local output cmd_status=0
  output=$("$TESTPLAY" run --shadow) || cmd_status=$?

  local run_id exit_code
  run_id=$(json_str "$output" "run_id")
  exit_code=$(json_num "$output" "exit_code")

  if [[ "$cmd_status" -ne 0 ]]; then
    echo "  ERROR [$stage]: testplay run --shadow exited with status $cmd_status" >&2
    echo "  run_id:    ${run_id:-(unparsed)}" >&2
    echo "  exit_code: ${exit_code:-(unparsed)}" >&2
    echo "  Raw testplay output:" >&2
    printf '%s\n' "$output" | sed 's/^/    /' >&2
    exit 1
  fi

  assert_field "$stage" "run_id"    "$run_id"    "$output"
  assert_field "$stage" "exit_code" "$exit_code" "$output"

  echo "  run_id:    $run_id"
  echo "  exit_code: $exit_code"

  echo "  Checking run artifacts..."
  check_artifacts "$stage" "$run_id"

  echo "  Checking shadow workspace structure..."
  local shadow_dir="$SMOKE_DIR/.testplay-shadow"
  if [[ ! -d "$shadow_dir" ]]; then
    echo "  ERROR [$stage]: shadow workspace not created: $shadow_dir" >&2
    exit 1
  fi
  local shadow_missing=false
  for d in Assets ProjectSettings Library; do
    if [[ ! -d "$shadow_dir/$d" ]]; then
      echo "  MISSING [$stage]: $shadow_dir/$d" >&2
      shadow_missing=true
    fi
  done
  if [[ "$shadow_missing" == "true" ]]; then
    exit 1
  fi

  echo "  OK"
  echo ""
}

# ── Run all smoke stages ──────────────────────────────────────────────────────

cd "$SMOKE_DIR"

run_smoke "Smoke 1: EditMode" "edit_mode"
run_smoke "Smoke 2: PlayMode" "play_mode"
run_smoke_shadow "Smoke 3: Shadow (EditMode)" "edit_mode"

# ── Done ─────────────────────────────────────────────────────────────────────

echo "==> All smoke checks passed."
