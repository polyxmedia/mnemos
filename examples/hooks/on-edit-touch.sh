#!/usr/bin/env bash
# Claude Code PostToolUse hook: record a file touch in Mnemos.
# Wire in .claude/settings.json with matcher "Edit|Write".
#
# Mnemos must be running on the default HTTP port, or adjust MNEMOS_URL.

set -euo pipefail

MNEMOS_URL="${MNEMOS_URL:-http://localhost:8080}"
AUTH_HEADER=()
if [[ -n "${MNEMOS_API_KEY:-}" ]]; then
  AUTH_HEADER=(-H "Authorization: Bearer $MNEMOS_API_KEY")
fi

# Claude Code pipes tool call JSON on stdin; extract the file path.
INPUT="$(cat)"
PATH_ARG="$(printf '%s' "$INPUT" | python3 -c "
import json, sys
try:
  d = json.load(sys.stdin)
  # Edit/Write tools carry file_path; MultiEdit carries path.
  t = d.get('tool_input', {})
  print(t.get('file_path') or t.get('path') or '')
except Exception:
  pass
")"

[[ -n "$PATH_ARG" ]] || exit 0

PROJECT="${MNEMOS_PROJECT:-$(basename "$(git rev-parse --show-toplevel 2>/dev/null || pwd)")}"

curl -fsS -X POST "$MNEMOS_URL/v1/touch" \
  -H "Content-Type: application/json" \
  "${AUTH_HEADER[@]}" \
  -d "$(printf '{"path":"%s","project":"%s"}' "$PATH_ARG" "$PROJECT")" \
  >/dev/null 2>&1 || true
