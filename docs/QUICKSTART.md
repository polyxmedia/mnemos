# Quickstart

Under 60 seconds from nothing to working.

## 1. Install

```bash
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh | bash
```

The script detects your OS and architecture, downloads the binary to `~/.local/bin` (or `/usr/local/bin`), adds it to your PATH if needed, and runs `mnemos init` automatically.

## 2. Verify

```bash
mnemos doctor
```

You should see green checkmarks for binary, config, storage, and any agent clients it detected (Claude Code, Cursor, Windsurf).

## 3. Restart your agent

Close and reopen Claude Code (or Cursor, or whichever MCP-capable agent you use). On next launch, 14 new `mnemos_*` tools become available.

## 4. First session

From your agent:

```
Call mnemos_session_start with project="my-repo" goal="fix the login bug".
```

The response includes a pre-warmed context block: any conventions you've declared for this project, recent sessions, matching skills, and hot files. Push, not pull — you don't have to ask for memory, you already have it.

## 5. Declare a convention (optional, once per project)

```
Call mnemos_convention with:
  title: "error wrapping"
  rule: "All errors wrapped with fmt.Errorf(..., %w, err)"
  rationale: "preserves the chain for errors.Is"
  project: "my-repo"
```

Every future session on `my-repo` starts with that convention already in context.

## 6. Record a correction when something goes wrong

```
Call mnemos_correct with:
  title: "oauth retry without backoff"
  tried: "retry immediately on 401"
  wrong_because: "401 is auth failure, not transient — retrying burns quota"
  fix: "refresh token first, then retry once"
  project: "my-repo"
```

Next session that touches oauth in this project, the correction surfaces before the agent tries the wrong approach again.

## Troubleshooting

**`mnemos init` didn't find my agent client.**
The script looks for `~/.claude.json`, `~/.cursor/mcp.json`, and `~/.codeium/windsurf/mcp_config.json`. If your client uses a different path, you can wire mnemos manually:

```json
{
  "mcpServers": {
    "mnemos": {
      "command": "/full/path/to/mnemos",
      "args": ["serve"]
    }
  }
}
```

**`mnemos doctor` reports a missing registration.**
Run `mnemos init` again. It's idempotent — it won't overwrite unrelated entries in your config.

**The binary isn't on my PATH.**
Open a new terminal (the installer adds to shell rc files, which don't affect the current session), or run `mnemos` with its full path from `mnemos doctor`.
