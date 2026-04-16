# Mnemos

Persistent memory and skills for AI coding agents. MCP-native, single binary, zero dependencies.

Mnemos is not another memory dump. It is a structured cognitive substrate: agents store what matters, skip repeat mistakes via a correction journal, inherit project conventions across sessions, and recover from compaction without losing their train of thought.

## What makes it different

- **Push, not pull.** Session start returns a pre-warmed context block — agents don't have to ask for memory, they just get it.
- **Correction journal.** Agents record what they tried, why it was wrong, and the fix. The next session surfaces the correction before the same mistake is made again.
- **Compaction recovery.** When the agent's context gets compacted mid-session, one call to `mnemos_context` in recovery mode restores the goal, decisions, and in-session observations.
- **Bi-temporal truth.** Facts are invalidated, not deleted. "We used to use X, now we use Y" queries work. No context poisoning from stale facts.
- **Promptware sanitisation.** Memory stores are a new attack surface. Mnemos scans observation content at the injection boundary for prompt-injection patterns and flags high-risk content before the agent sees it.
- **Pure Go, zero CGO.** One static binary for Linux / macOS / Windows, amd64 + arm64. No Docker, no Python, no vector DB to deploy.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh | bash
```

Or with Homebrew:
```bash
brew install polyxmedia/tap/mnemos
```

Or with Go:
```bash
go install github.com/polyxmedia/mnemos/cmd/mnemos@latest
```

## Quick start

```bash
# Register with Claude Code / Cursor / Windsurf (idempotent)
mnemos init

# Verify everything is wired up
mnemos doctor

# That's it. Restart your agent.
```

Your agent now has 14 `mnemos_*` tools available.

## CLI

| Command | Purpose |
| --- | --- |
| `mnemos serve` | Start the MCP stdio server (default) |
| `mnemos serve --http :8080` | Start the HTTP API instead |
| `mnemos init` | Auto-wire Claude Code / Cursor / Windsurf MCP configs |
| `mnemos doctor` | Verify install, DB, and agent registrations |
| `mnemos search <query>` | Search memories from the terminal |
| `mnemos stats` | Memory counts, top tags, recent sessions |
| `mnemos sessions` | List recent sessions |
| `mnemos export [file]` | JSON dump of all data |
| `mnemos prune` | Remove expired observations |
| `mnemos config` | Print the current configuration |
| `mnemos version` | Print the binary version |

## MCP tools

| Tool | Purpose |
| --- | --- |
| `mnemos_save` | Store an agent-curated observation |
| `mnemos_search` | BM25 + recency + importance ranked search |
| `mnemos_get` | Fetch full observation by ID |
| `mnemos_delete` | Hard-delete a mistaken save |
| `mnemos_link` | Related / caused_by / supersedes / contradicts / refines |
| `mnemos_session_start` | **Returns pre-warmed context block** (push, not pull) |
| `mnemos_session_end` | Close with summary, status, reflection |
| `mnemos_context` | Query-based context OR `mode=recovery` after compaction |
| `mnemos_correct` | Record tried / wrong_because / fix — anti-repeat-mistakes |
| `mnemos_convention` | Declare a project convention (auto-injected at session start) |
| `mnemos_touch` | File heat map: which files matter this project |
| `mnemos_skill_match` | Find matching procedures |
| `mnemos_skill_save` | Save or version a reusable procedure |
| `mnemos_stats` | System statistics |

See [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md) for parameter details.

## Configuration

Mnemos works with zero config. `~/.mnemos/config.toml` is auto-created on first run. Everything is optional.

```toml
[storage]
path = "~/.mnemos/mnemos.db"

[search]
decay_rate         = 0.05   # recency half-life (higher = faster decay)
default_limit      = 20
max_context_tokens = 2000

[server]
transport = "stdio"         # "stdio" or "http"
http_addr = ":8080"
```

## Architecture

Mnemos is a clean-boundary Go service. Domain packages (`memory`, `session`, `skills`) declare interfaces. The `storage` package implements them. Transports (`mcp`, future `api`) are thin adapters that translate protocol shapes into service calls.

- **Storage**: SQLite + FTS5 (via pure-Go `modernc.org/sqlite`, no CGO). Bi-temporal schema: facts get invalidated with timestamps, never deleted.
- **Ranking**: BM25 base + importance multiplier + recency decay + ACT-R-style access-frequency boost.
- **Pre-warm**: composes conventions, recent sessions, matching skills, corrections, and hot files into a ≤500-token block.
- **Safety**: regex-based scanner for prompt-injection patterns at the injection boundary. Flags rather than silently drops.

More detail in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Short version: tests with every change, wrap every error, no globals, no CGO.

## License

MIT. See [LICENSE](LICENSE).
