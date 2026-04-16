#!/usr/bin/env bash
# Claude Code PreCompact hook: flush in-session memory to Mnemos before
# the agent's context gets compacted. Pairs with mnemos_context in
# recovery mode after the compaction completes.
#
# Wire in .claude/settings.json:
#   "PreCompact": [{"command": "/usr/local/bin/mnemos-hook-precompact.sh"}]

set -euo pipefail

MNEMOS_URL="${MNEMOS_URL:-http://localhost:8080}"
AUTH_HEADER=()
if [[ -n "${MNEMOS_API_KEY:-}" ]]; then
  AUTH_HEADER=(-H "Authorization: Bearer $MNEMOS_API_KEY")
fi

# We expect the caller to have stashed the current session_id in
# $MNEMOS_SESSION_ID (e.g. via SessionStart hook).
SESSION_ID="${MNEMOS_SESSION_ID:-}"
[[ -n "$SESSION_ID" ]] || exit 0

PROJECT="${MNEMOS_PROJECT:-$(basename "$(git rev-parse --show-toplevel 2>/dev/null || pwd)")}"

# Ask Mnemos for a recovery block now — the agent's response to the
# compaction will include this in its re-hydrated context.
curl -fsS -X POST "$MNEMOS_URL/v1/context" \
  -H "Content-Type: application/json" \
  "${AUTH_HEADER[@]}" \
  -d "$(cat <<EOF
{
  "Query":     "",
  "Mode":      "recovery",
  "SessionID": "$SESSION_ID",
  "Project":   "$PROJECT"
}
EOF
)" >/tmp/mnemos-recovery.json 2>/dev/null || true

# Optional: print the recovery block so the agent sees it.
cat /tmp/mnemos-recovery.json 2>/dev/null || true
