#!/usr/bin/env bash
# smoke.sh — local smoke verification for FastPlay Runner
#
# Usage:
#   UNITY_PATH=/path/to/Unity ./scripts/smoke.sh
#
# Options (env vars):
#   UNITY_PATH   Required. Path to Unity binary.
#   FASTPLAY     Path to fastplay binary (default: ./fastplay built from source)
#   SMOKE_DIR    Path to the smoke Unity project (default: ./fixtures/smoke-project)
#
# What this tests:
#   1. EditMode smoke: fastplay run → exit 0, all 6 run artifacts present
#   2. PlayMode smoke: fastplay run → same artifacts, test_platform=play_mode
#
# Artifacts verified per run (inside .fastplay/runs/<run_id>/):
#   results.xml, summary.json, manifest.json, stdout.log, stderr.log, events.ndjson
# Snapshot (in smoke project root, outside run artifact dir):
#   fastplay-status.json
#
# The script exits non-zero if any check fails.
# Dependencies: bash, grep, sed, go — no python3 or jq required.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SMOKE_DIR="${SMOKE_DIR:-$REPO_ROOT/fixtures/smoke-project}"
FASTPLAY="${FASTPLAY:-}"

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

# Build fastplay if not provided.
if [[ -z "$FASTPLAY" ]]; then
  echo "==> Building fastplay..."
  go build -o "$REPO_ROOT/fastplay" "$REPO_ROOT/cmd/fastplay"
  FASTPLAY="$REPO_ROOT/fastplay"
fi

if [[ ! -x "$FASTPLAY" ]]; then
  echo "ERROR: fastplay binary not found or not executable: $FASTPLAY" >&2
  exit 1
fi

echo "==> Using fastplay:   $FASTPLAY"
echo "==> Using Unity:      $UNITY_PATH"
echo "==> Smoke project:    $SMOKE_DIR"
echo ""

# ── Helper: extract a string field from pretty or compact JSON ────────────────
# Usage: json_str <json_text> <field>
# Handles both compact  ("field":"value")
#     and pretty output ("field": "value") from json.MarshalIndent.
# Always returns 0; returns empty string if the field is not found.
# (|| true prevents grep no-match from triggering set -e / set -o pipefail)
json_str() {
  printf '%s' "$1" \
    | grep -o "\"$2\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" \
    | sed 's/.*"[[:space:]]*:[[:space:]]*"//' \
    | tr -d '"' || true
}

# ── Helper: extract a numeric field from pretty or compact JSON ───────────────
# Usage: json_num <json_text> <field>
# Handles both compact  ("field":0)
#     and pretty output ("field": 0) from json.MarshalIndent.
# Always returns 0; returns empty string if the field is not found.
# (|| true prevents grep no-match from triggering set -e / set -o pipefail)
json_num() {
  printf '%s' "$1" \
    | grep -o "\"$2\"[[:space:]]*:[[:space:]]*[0-9-][0-9]*" \
    | sed 's/.*:[[:space:]]*//' || true
}

# ── Helper: assert a parsed field is non-empty ────────────────────────────────
# Usage: assert_field <stage> <field_name> <value> <raw_json>
# Exits immediately with a diagnostic if the field is empty.
assert_field() {
  local stage="$1" field="$2" value="$3" raw="$4"
  if [[ -z "$value" ]]; then
    echo "  ERROR [$stage]: field '$field' is empty — JSON parsing failed." >&2
    echo "  Raw fastplay output:" >&2
    printf '%s\n' "$raw" | sed 's/^/    /' >&2
    exit 1
  fi
}

# ── Helper: generate fastplay.json for smoke project ─────────────────────────

write_config() {
  local platform="$1"
  cat > "$SMOKE_DIR/fastplay.json" <<EOF
{
  "schema_version": "1",
  "unity_path": "$UNITY_PATH",
  "project_path": "$SMOKE_DIR",
  "test_platform": "$platform",
  "timeout": {
    "total_ms": 300000
  },
  "result_dir": ".fastplay/results"
}
EOF
}

# ── Helper: verify all expected artifact files are present ───────────────────
# Usage: check_artifacts <stage> <run_id>
# Exits immediately if the artifact directory or any expected file is missing.
check_artifacts() {
  local stage="$1" run_id="$2"
  local artifact_dir="$SMOKE_DIR/.fastplay/runs/$run_id"

  if [[ ! -d "$artifact_dir" ]]; then
    echo "  ERROR [$stage]: artifact directory not found: $artifact_dir" >&2
    echo "  Possible cause: run_id extraction failed or fastplay did not create the run directory." >&2
    exit 1
  fi

  local missing=false
  # Run artifacts (inside .fastplay/runs/<run_id>/)
  for f in results.xml summary.json manifest.json stdout.log stderr.log events.ndjson; do
    if [[ ! -f "$artifact_dir/$f" ]]; then
      echo "  MISSING [$stage]: $artifact_dir/$f" >&2
      missing=true
    fi
  done
  # Status snapshot (in project root, outside the run artifact dir)
  if [[ ! -f "$SMOKE_DIR/fastplay-status.json" ]]; then
    echo "  MISSING [$stage]: $SMOKE_DIR/fastplay-status.json (status snapshot)" >&2
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

  echo "  fastplay check..."
  "$FASTPLAY" check

  echo "  fastplay run ($platform)..."
  local output cmd_status=0
  output=$("$FASTPLAY" run) || cmd_status=$?

  local run_id exit_code
  run_id=$(json_str "$output" "run_id")
  exit_code=$(json_num "$output" "exit_code")

  if [[ "$cmd_status" -ne 0 ]]; then
    echo "  ERROR [$stage]: fastplay run exited with status $cmd_status" >&2
    echo "  run_id:    ${run_id:-(unparsed)}" >&2
    echo "  exit_code: ${exit_code:-(unparsed)}" >&2
    echo "  Raw fastplay output:" >&2
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

# ── Run both smoke stages ─────────────────────────────────────────────────────

cd "$SMOKE_DIR"

run_smoke "Smoke 1: EditMode" "edit_mode"
run_smoke "Smoke 2: PlayMode" "play_mode"

# ── Done ─────────────────────────────────────────────────────────────────────

echo "==> All smoke checks passed."
