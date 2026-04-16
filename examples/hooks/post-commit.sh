#!/usr/bin/env bash
# Claude Code PostBashToolUse hook: on `git commit`, save the commit
# message + short diff summary as a Mnemos observation.
#
# Wire in .claude/settings.json with matcher "git commit".

set -euo pipefail

MNEMOS_URL="${MNEMOS_URL:-http://localhost:8080}"
AUTH_HEADER=()
if [[ -n "${MNEMOS_API_KEY:-}" ]]; then
  AUTH_HEADER=(-H "Authorization: Bearer $MNEMOS_API_KEY")
fi

# Only fire on successful commits.
cd "$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

SHA="$(git rev-parse --short HEAD 2>/dev/null || echo '')"
[[ -n "$SHA" ]] || exit 0

SUBJECT="$(git log -1 --pretty=%s)"
BODY="$(git log -1 --pretty=%B)"
FILES="$(git show --stat --name-only --oneline HEAD | tail -n +2 | head -20)"

PROJECT="${MNEMOS_PROJECT:-$(basename "$(pwd)")}"

# Escape JSON via python for safety.
BODY_JSON="$(printf '%s\n\nFiles:\n%s' "$BODY" "$FILES" | python3 -c '
import json, sys
print(json.dumps(sys.stdin.read()))
')"

curl -fsS -X POST "$MNEMOS_URL/v1/observations" \
  -H "Content-Type: application/json" \
  "${AUTH_HEADER[@]}" \
  -d "$(cat <<EOF
{
  "Title": "commit $SHA: $SUBJECT",
  "Content": $BODY_JSON,
  "Type": "episodic",
  "Tags": ["commit", "git"],
  "Project": "$PROJECT",
  "Importance": 6
}
EOF
)" >/dev/null 2>&1 || true
