#!/usr/bin/env bash
# smoke_helpers.sh — shared JSON parsing helpers for smoke.sh and smoke_selfcheck.sh
#
# Source this file; do not execute it directly.
# Both smoke.sh and smoke_selfcheck.sh source this file so the implementations
# stay in one place and selfcheck always tests the live helpers.

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
