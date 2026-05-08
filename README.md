# Mnemos

Persistent memory for AI coding agents. MCP server, single Go binary, no Python, no CGO.

[![release](https://img.shields.io/github/v/release/polyxmedia/mnemos?sort=semver)](https://github.com/polyxmedia/mnemos/releases)
[![CI](https://github.com/polyxmedia/mnemos/actions/workflows/ci.yml/badge.svg)](https://github.com/polyxmedia/mnemos/actions/workflows/ci.yml)
[![coverage](https://codecov.io/gh/polyxmedia/mnemos/branch/main/graph/badge.svg)](https://codecov.io/gh/polyxmedia/mnemos)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

You correct your agent for retrying blindly on a 401. Mnemos stores:

```json
{
  "type": "correction",
  "tried": "retry on 401",
  "wrong_because": "401 is auth failure, not transient",
  "fix": "refresh token, then retry once"
}
```

Next session, the entry surfaces in the agent's prewarm context before it reaches that code path. Three corrections that cluster on the same `(agent, project, topic)` get promoted into a skill by the consolidation pass. No LLM runs inside the memory layer; promotion is deterministic pattern-mining over structured records.

`mnemos replay <session_id>` regenerates a past session as markdown with everything recorded since layered in.

State lives in `~/.mnemos/mnemos.db` (SQLite). One static Go binary, ~15 MB, Linux/macOS/Windows × amd64/arm64.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh | bash
mnemos doctor
```

The installer runs `mnemos init`, which auto-wires Claude Code, Claude Desktop, Cursor, Windsurf, and OpenAI Codex CLI. Restart your agent.

For Claude Code, also install the agent skill so it records back to the store:

```bash
mkdir -p ~/.claude/skills/mnemos
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/.claude/skills/mnemos/SKILL.md \
  -o ~/.claude/skills/mnemos/SKILL.md
```

Other paths: `go install github.com/polyxmedia/mnemos/cmd/mnemos@latest` or [release binaries](https://github.com/polyxmedia/mnemos/releases). `mnemos update` self-updates with sha256 verification.

## Verify

```bash
mnemos verify retrieval   # do memories surface for their trigger queries?
mnemos verify behavior    # does the agent behave differently on vs off?
mnemos verify capture     # does the agent record corrections handed to it?
```

Behaviour A/B against Claude Code, n=5 paired runs:

```
scenario                        on    off   lift
session_start_on_edit           5/5   0/5   +100%
oss_first_for_protocol          5/5   0/5   +100%
no_ai_attribution_in_commit     5/5   5/5    +0%
no_cgo_proposal                 5/5   5/5    +0%
migration_locked_refused        5/5   5/5    +0%
overall                         25/25 15/25  +40%
```

Lift concentrates on contrarian/project-specific conventions and the recursive case where the agent forgets to call its own memory tools. On widely-known practices the model already gets right, on-arm and off-arm match exactly.

Capture rate, n=3 per scenario, after two rounds of lever tuning (baseline was 7%):

```
scenario                        captured  rate
explicit_save_request           3/3       100%
inline_correction               2/3        67%
quiet_convention_mention        2/3        67%
silent_correction_mid_work      1/3        33%
architectural_decision          0/3         0%
overall                         8/15       53%
```

The 7→53% jump came from a `UserPromptSubmit` hook that emits a `[mnemos: capture required]` directive when the user's message matches correction-shaped phrasing. Trigger-phrase examples in MCP tool descriptions added a few points; the hook did the rest. Architectural decisions buried in larger task prompts still sit at 0/3. Open problem.

Fixtures and runner: [`verify/`](verify/).

## Design

### Correction journal

`tried / wrong_because / fix` is a first-class observation type with retrieval boosting. The agent records a mistake once; the entry surfaces in future sessions on the same topic.

### Skill promotion

The consolidation pass clusters corrections by `(agent, project, topic)`. Three or more in the same cluster mint a skill with `## When this applies`, `## Avoid`, and `## Do` sections, synthesised from the underlying records. Idempotent via stable origin hash; a second pass extends the existing skill and bumps the version.

### Rumination

Threshold monitors flag stale, low-effectiveness, or contradicted skills as candidates for review. Resolution requires a falsifiable `why_better` field naming a prediction the revision makes that the prior version did not. Revisions invalidate the old entry through the bi-temporal store; the dream pass auto-closes candidates whose target carries a `ruminated-from:<id>` tag.

### Bi-temporal store

Facts carry valid and invalid timestamps. Default retrieval surfaces only currently-valid facts; superseded knowledge remains queryable for replay and audit.

### Prompt-injection scanner

Runs at the memory-write boundary. Sanitises low-risk content; wraps high-risk content (instruction overrides, zero-width unicode, bidi overrides, fake tool-call syntax, MCP spoofing) in a `[MNEMOS: FLAGGED]` banner before it reaches the model.

### Hybrid retrieval

BM25 + cosine similarity via Reciprocal Rank Fusion. Auto-enables when Ollama is reachable. Falls back to pure FTS5.

### Composed prewarm

`mnemos_session_start` and the `SessionStart` hook return a ranked, token-budgeted block (conventions + recent sessions + matching skills + corrections + hot files) capped at 500 tokens by default. Fires once per session. No per-turn cost.

### Compaction recovery

`mnemos_context` in `recovery` mode reconstructs goal, decisions, and in-session observations after a context compaction.

### Skill packs

Export any skill as a JSON pack, share by file or URL, install with `mnemos skill import <file-or-url>`. Runtime stats stripped, pack versioning strict.

### Obsidian export

`mnemos vault export|watch` writes a markdown graph with wikilinks.

## Setup per agent

`mnemos init` is idempotent and handles all of the below. Manual configurations:

### Claude Code

`~/.claude.json`:

```json
{
  "mcpServers": {
    "mnemos": { "command": "/full/path/to/mnemos", "args": ["serve"] }
  }
}
```

`mnemos init` also writes a `SessionStart` hook to `~/.claude/settings.json` calling `mnemos prewarm`. Honours `CLAUDE_CONFIG_DIR`. Manual hook shape:

```json
{
  "hooks": {
    "SessionStart": [{
      "matcher": "startup",
      "hooks": [{ "type": "command", "command": "/full/path/to/mnemos prewarm", "timeout": 10 }]
    }]
  }
}
```

### Cursor / Windsurf / Claude Desktop / Codex CLI

Same `mcpServers.mnemos` shape. Paths:

| Client | Config path |
| --- | --- |
| Cursor | `~/.cursor/mcp.json` |
| Windsurf | `~/.codeium/windsurf/mcp_config.json` |
| Claude Desktop (macOS) | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Claude Desktop (Windows) | `%APPDATA%\Claude\claude_desktop_config.json` |

Codex CLI uses TOML at `~/.codex/config.toml`:

```toml
[mcp_servers.mnemos]
command = "/full/path/to/mnemos"
args    = ["serve"]
```

### Any MCP-compatible client

Stdio: point the client at `mnemos serve`. Server advertises 16 tools and 3 resources on the `initialize` handshake (20 tools with rumination enabled).

### HTTP transport

```bash
MNEMOS_API_KEY=$(openssl rand -hex 32) mnemos serve --http :8080
```

Use `pkg/client` from Go or `POST /v1/observations` directly. Reference: [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md).

## CLI

| Command | |
| --- | --- |
| `mnemos serve [--http :PORT]` | MCP stdio (default) or HTTP API |
| `mnemos init` | Wire agent clients |
| `mnemos doctor` | Verify install |
| `mnemos prewarm` | Print session-start prewarm block |
| `mnemos search <query>` | Search store |
| `mnemos stats` | Counts and tags |
| `mnemos sessions` | Recent sessions |
| `mnemos replay <session_id>` | Markdown recap of a past session |
| `mnemos export [file]` | JSON dump |
| `mnemos import <file>` | Restore from JSON |
| `mnemos prune` | Remove expired observations |
| `mnemos dream [--watch]` | Consolidation pass |
| `mnemos vault export\|watch\|status` | Obsidian sync |
| `mnemos embed status\|backfill` | Embedding tools |
| `mnemos skill list\|export\|import` | Manage skill packs |
| `mnemos verify retrieval\|behavior\|capture\|all` | Run verification harness |
| `mnemos update [--yes]` | Self-update with sha256 verify |
| `mnemos config` / `mnemos version` | Print config / version |

## MCP tools

`mnemos_save` `mnemos_search` `mnemos_get` `mnemos_delete` `mnemos_link` `mnemos_session_start` `mnemos_session_end` `mnemos_context` `mnemos_promote` `mnemos_correct` `mnemos_convention` `mnemos_touch` `mnemos_skill_match` `mnemos_skill_save` `mnemos_skill_score` `mnemos_stats`

With `[rumination].enabled = true`: `mnemos_ruminate_list` `mnemos_ruminate_pack` `mnemos_ruminate_resolve` `mnemos_ruminate_dismiss`

Parameters: [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md).

## Configuration

`~/.mnemos/config.toml`, auto-created on first run. All fields optional.

```toml
[storage]
path = "~/.mnemos/mnemos.db"

[search]
decay_rate         = 0.05
default_limit      = 20
max_context_tokens = 2000
hybrid_alpha       = 0.5    # 1.0 = pure BM25, 0.0 = pure vector

[embedding]
provider  = "auto"          # auto | ollama | openai | none
model     = "nomic-embed-text"
dimension = 768

[vault]
enabled        = false
path           = "~/.mnemos/vault"
watch_interval = "5m"

[dream]
interval     = ""           # e.g. "6h"
stale_days   = 30
decay_amount = 1

[rumination]
enabled                   = true
skill_effectiveness_floor = 0.3
skill_min_uses            = 10
stale_skill_days          = 90
stale_skill_floor         = 0.5
correction_repeat_n       = 3
contradiction_threshold   = 1

[server]
transport = "stdio"
http_addr = ":8080"
api_key   = ""
```

By default no data leaves the machine. Network calls happen only when `[embedding].provider = "openai"` or `[server].transport = "http"`.

## Architecture

```
internal/storage      SQLite + FTS5 (modernc.org/sqlite), bi-temporal schema
internal/memory       observations, hybrid ranker (BM25 + cosine via RRF)
internal/session      session service
internal/skills       procedural memory service
internal/prewarm      session_start and compaction-recovery composers
internal/safety       prompt-injection scanner
internal/dream        consolidation daemon
internal/rumination   threshold monitors, hostile-review packaging
internal/vault        Obsidian export and watcher
internal/embedding    Ollama / OpenAI / Noop providers
internal/mcp          official MCP Go SDK wrapper
internal/api          HTTP REST transport
internal/installer    agent client wire-up
pkg/client            typed Go HTTP client
```

Details in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Build

```bash
make test           # -race, full suite
make cover          # coverage.html
make lint           # golangci-lint
make release V=v0.X.Y
```

Coverage 70% overall, 80 to 95% on core domain packages. API stable at v0.1.x; bi-temporal schema means migrations are non-breaking.

## License

MIT. By [André Figueira](https://x.com/voidmode) at [Polyxmedia](https://polyxmedia.com). [AUTHORS.md](AUTHORS.md), [ROADMAP.md](ROADMAP.md), [CONTRIBUTING.md](CONTRIBUTING.md).
