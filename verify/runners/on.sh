#!/usr/bin/env bash
# On-arm: full mnemos surface.
#
#  - Per-run scratch cwd whose basename is "mnemos" so the global
#    SessionStart prewarm hook scopes to the mnemos project (and surfaces
#    conventions/corrections/recent-sessions).
#  - Auto-search memory block from `mnemos hook user-prompt` injected via
#    --append-system-prompt. We call the hook ourselves because claude -p
#    does NOT fire UserPromptSubmit hooks (only SessionStart). Without this,
#    the on-arm only gets static prewarm — no per-prompt relevance.
#  - --strict-mcp-config + mcp_on.json keeps MCP isolated from the user's
#    other servers.
set -euo pipefail

trigger="$1"
sandbox=$(mktemp -d -t mnemos-on)
mkdir "$sandbox/mnemos"
cd "$sandbox/mnemos"
trap 'rm -rf "$sandbox"' EXIT

# Pull a memory block from the same hook that runs in interactive mode.
# Empty when nothing scores ≥ promptMemoryMinScore.
memory_block=$(printf '{"hook_event_name":"UserPromptSubmit","prompt":%s}' \
  "$(printf '%s' "$trigger" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')" \
  | /Users/andrefigueira/.local/bin/mnemos hook user-prompt 2>/dev/null || true)

if [[ -n "$memory_block" ]]; then
  exec claude -p "$trigger" \
    --append-system-prompt "$memory_block" \
    --mcp-config /Users/andrefigueira/Code/mnemos/verify/mcp_on.json \
    --strict-mcp-config \
    --dangerously-skip-permissions \
    --output-format stream-json \
    --verbose
else
  exec claude -p "$trigger" \
    --mcp-config /Users/andrefigueira/Code/mnemos/verify/mcp_on.json \
    --strict-mcp-config \
    --dangerously-skip-permissions \
    --output-format stream-json \
    --verbose
fi
