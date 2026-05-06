#!/usr/bin/env bash
# Off-arm: clean baseline. No mnemos hooks, no MCP server, no memory.
#
#  - MNEMOS_DISABLED=1 makes every globally-installed mnemos hook (prewarm,
#    user-prompt, post-tool, etc.) no-op. Auth and other claude settings
#    stay intact because we don't reassign CLAUDE_CONFIG_DIR.
#  - --strict-mcp-config + mcp_off.json (empty) kills the MCP surface.
#  - Per-run scratch cwd to match the on-arm structurally.
set -euo pipefail

trigger="$1"
sandbox=$(mktemp -d -t mnemos-off)
mkdir "$sandbox/mnemos"
cd "$sandbox/mnemos"
trap 'rm -rf "$sandbox"' EXIT

MNEMOS_DISABLED=1 exec claude -p "$trigger" \
  --mcp-config /Users/andrefigueira/Code/mnemos/verify/mcp_off.json \
  --strict-mcp-config \
  --dangerously-skip-permissions \
  --output-format stream-json \
  --verbose
