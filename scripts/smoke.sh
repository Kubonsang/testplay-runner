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
#   1. EditMode smoke: fastplay run → exit 0, results.xml, summary.json, manifest.json, events.ndjson
#   2. PlayMode smoke: fastplay run → same artifacts, test_platform=play_mode
#
# The script exits non-zero if any check fails.

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

# ── Helper: generate fastplay.json ───────────────────────────────────────────

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

# ── Helper: verify artifact files exist ──────────────────────────────────────

check_artifacts() {
  local run_id="$1"
  local artifact_dir="$SMOKE_DIR/.fastplay/runs/$run_id"

  local ok=true
  for f in results.xml summary.json manifest.json stdout.log stderr.log events.ndjson; do
    if [[ ! -f "$artifact_dir/$f" ]]; then
      echo "  MISSING: $artifact_dir/$f" >&2
      ok=false
    fi
  done
  if [[ ! -f "$SMOKE_DIR/fastplay-status.json" ]]; then
    echo "  MISSING: $SMOKE_DIR/fastplay-status.json" >&2
    ok=false
  fi
  $ok
}

# ── Smoke 1: EditMode ────────────────────────────────────────────────────────

echo "==> Smoke 1: EditMode"
write_config "edit_mode"

cd "$SMOKE_DIR"

echo "  fastplay check..."
"$FASTPLAY" check

echo "  fastplay run (edit_mode)..."
output=$("$FASTPLAY" run)
echo "$output" | python3 -m json.tool > /dev/null || { echo "  ERROR: run output is not valid JSON" >&2; exit 1; }

run_id=$(echo "$output" | python3 -c "import sys,json; print(json.load(sys.stdin)['run_id'])")
exit_code=$(echo "$output" | python3 -c "import sys,json; print(json.load(sys.stdin)['exit_code'])")

echo "  run_id:    $run_id"
echo "  exit_code: $exit_code"

if [[ "$exit_code" != "0" ]]; then
  echo "  ERROR: expected exit_code 0, got $exit_code" >&2
  exit 1
fi

echo "  Checking artifacts..."
check_artifacts "$run_id"
echo "  OK"
echo ""

# ── Smoke 2: PlayMode ────────────────────────────────────────────────────────

echo "==> Smoke 2: PlayMode"
write_config "play_mode"

echo "  fastplay check..."
"$FASTPLAY" check

echo "  fastplay run (play_mode)..."
output=$("$FASTPLAY" run)
echo "$output" | python3 -m json.tool > /dev/null || { echo "  ERROR: run output is not valid JSON" >&2; exit 1; }

run_id=$(echo "$output" | python3 -c "import sys,json; print(json.load(sys.stdin)['run_id'])")
exit_code=$(echo "$output" | python3 -c "import sys,json; print(json.load(sys.stdin)['exit_code'])")

echo "  run_id:    $run_id"
echo "  exit_code: $exit_code"

if [[ "$exit_code" != "0" ]]; then
  echo "  ERROR: expected exit_code 0, got $exit_code" >&2
  exit 1
fi

echo "  Checking artifacts..."
check_artifacts "$run_id"
echo "  OK"
echo ""

# ── Done ─────────────────────────────────────────────────────────────────────

echo "==> All smoke checks passed."
