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

## 3. Install the Claude Code skill (recommended)

The skill nudges the agent to actually call `mnemos_*` tools on save / remember / correct signals. Without it, agents tend to silently edit on plain tasks and the store goes empty. One-time copy:

```bash
mkdir -p ~/.claude/skills/mnemos
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/.claude/skills/mnemos/SKILL.md \
  -o ~/.claude/skills/mnemos/SKILL.md
```

(Cursor, Windsurf, and other MCP clients don't have an equivalent skill system yet. You can keep the same instructions in a project `CLAUDE.md`, `.cursorrules`, or a system prompt preset.)

## 4. Restart your agent

Close and reopen Claude Code (or Cursor, or whichever MCP-capable agent you use). On next launch, 14 new `mnemos_*` tools become available.

## 5. First session

From your agent:

```
Call mnemos_session_start with project="my-repo" goal="fix the login bug".
```

The response includes a pre-warmed context block: any conventions you've declared for this project, recent sessions, matching skills, and hot files. Push, not pull — you don't have to ask for memory, you already have it.

## 6. Declare a convention (optional, once per project)

```
Call mnemos_convention with:
  title: "error wrapping"
  rule: "All errors wrapped with fmt.Errorf(..., %w, err)"
  rationale: "preserves the chain for errors.Is"
  project: "my-repo"
```

Every future session on `my-repo` starts with that convention already in context.

## 7. Record a correction when something goes wrong

```
Call mnemos_correct with:
  title: "oauth retry without backoff"
  tried: "retry immediately on 401"
  wrong_because: "401 is auth failure, not transient — retrying burns quota"
  fix: "refresh token first, then retry once"
  project: "my-repo"
```

Next session that touches oauth in this project, the correction surfaces before the agent tries the wrong approach again.

## 8. When a stored rule stops holding up, ruminate

Stored skills whose effectiveness falls below the threshold get flagged in the next dream pass. From your agent:

```
Call mnemos_ruminate_list to see pending reviews.
```

Pick one:

```
Call mnemos_ruminate_pack with id="rumination-abc123…"
```

You get a review block with the rule verbatim, the evidence against it, a falsifiable restatement, and hostile-review prompts to answer before proposing a revision. Resolve with:

```
Call mnemos_ruminate_resolve with
  id="rumination-abc123…"
  resolved_by="<new skill id>"
  why_better="one sentence naming a concrete new prediction the revision makes"
```

Cosmetic rewording is rejected at the tool boundary; resolution must be epistemically honest. If the hostile review convinced you the rule stands, `mnemos_ruminate_dismiss(id, reason)` records that judgement so the next pass doesn't re-raise the flag without context.

## 9. Let corrections compound into skills

After three or more corrections cluster on the same project + topic, the consolidation pass promotes them into a skill with `When this applies / Avoid / Do` sections. Promotion runs on every dream pass (manual via `mnemos dream`, or continuously with `mnemos dream --watch`). Listing skills shows which ones were auto-promoted:

```
$ mnemos skill list
  auto: oauth (my-repo)            v1 [auto-promoted]
    Auto-promoted from 3 corrections in my-repo: oauth
```

`mnemos stats` surfaces the count: `skills: N (M auto-promoted from corrections)`.

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
