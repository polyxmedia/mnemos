# Mnemos

Persistent memory and skills for AI coding agents. MCP-native, single binary, zero runtime dependencies.

Mnemos is not another memory dump. It's a structured cognitive substrate: agents store what matters, skip repeat mistakes via a correction journal, inherit project conventions across sessions, and recover from compaction without losing their train of thought.

## What makes it different

- **Push, not pull.** `mnemos_session_start` returns a pre-warmed context block. Agents don't have to ask for memory — they already have it.
- **Correction journal.** Record what was tried, why it was wrong, and the fix. Next session surfaces the correction before the same mistake is made again.
- **Compaction recovery.** When the agent's context gets compacted mid-session, one call to `mnemos_context` in recovery mode restores the goal, in-session decisions, and active conventions.
- **Hybrid retrieval.** BM25 (exact terms) + cosine similarity (paraphrases) fused via Reciprocal Rank Fusion. Auto-enabled if Ollama is running, otherwise pure FTS5 and Mnemos still works.
- **Bi-temporal truth.** Facts are invalidated, not deleted. "We used to use X, now Y" queries work. No context poisoning from stale facts.
- **Promptware sanitisation.** Memory stores are a new attack surface. Mnemos scans at the injection boundary for prompt-injection patterns and flags high-risk content before the agent sees it.
- **Obsidian vault export.** Browse your agent's entire memory as a linked markdown graph. One file per observation, session, and skill with YAML frontmatter and wikilinks.
- **Dream cycle.** Offline consolidation: dedup, decay, prune. Writes a journal observation so the agent can query what changed while it was idle.
- **Pure Go, zero CGO.** One static binary for Linux / macOS / Windows, amd64 + arm64. No Docker, no Python, no vector DB to deploy.

## Install

### One-liner (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh | bash
```

The script detects your OS/arch, downloads the latest release binary (or falls back to `go install` pre-release), installs to `~/.local/bin`, adds to PATH, and runs `mnemos init` to auto-wire Claude Code / Cursor / Windsurf.

### Homebrew

```bash
brew install polyxmedia/tap/mnemos
mnemos init
```

### Go install (from source)

```bash
go install github.com/polyxmedia/mnemos/cmd/mnemos@latest
mnemos init
```

### Manual

Download the binary for your platform from [releases](https://github.com/polyxmedia/mnemos/releases), put it on your PATH, run `mnemos init`.

## Quick start

```bash
mnemos init      # wires Claude Code / Cursor / Windsurf MCP configs (idempotent)
mnemos doctor    # verifies install, storage, agent registration
```

Restart your agent. 14 `mnemos_*` tools are now available.

First session:

```
Agent: mnemos_session_start(project="my-repo", goal="fix the login bug")
→ Returns session_id + a ~500-token pre-warm block with conventions,
  recent sessions, matching skills, correction-journal entries, hot files.
```

Declare a convention once (surfaces in every future session on this project):

```
mnemos_convention(title="error wrapping", rule="use fmt.Errorf with %w",
                  rationale="preserves the chain for errors.Is",
                  project="my-repo")
```

Record a correction when something goes wrong:

```
mnemos_correct(title="oauth retry without backoff",
               tried="retry on 401",
               wrong_because="401 is auth failure, not transient",
               fix="refresh token, then retry once",
               project="my-repo")
```

## CLI

| Command | Purpose |
| --- | --- |
| `mnemos serve` | Start the MCP stdio server (default) |
| `mnemos serve --http :8080` | Start the HTTP API |
| `mnemos init` | Auto-wire agent client MCP configs |
| `mnemos doctor` | Verify install, DB, and agent registrations |
| `mnemos search <query>` | Search memories from the terminal |
| `mnemos stats` | Counts, top tags, recent sessions |
| `mnemos sessions` | List recent sessions |
| `mnemos export [file]` | JSON dump of all data |
| `mnemos import <file>` | Restore from JSON |
| `mnemos prune` | Remove expired observations |
| `mnemos dream` | Run one consolidation pass |
| `mnemos dream --watch` | Daemon mode on the configured interval |
| `mnemos vault export` | Export to an Obsidian vault |
| `mnemos vault watch` | Daemon: keep vault in sync |
| `mnemos vault status` | Show vault path + sync state |
| `mnemos embed status` | Show embedding provider |
| `mnemos embed backfill` | Generate embeddings for observations missing them |
| `mnemos config` | Print current configuration |
| `mnemos version` | Print binary version |

## MCP tools (14)

| Tool | Purpose |
| --- | --- |
| `mnemos_save` | Store an agent-curated observation |
| `mnemos_search` | Hybrid BM25 + vector ranked search |
| `mnemos_get` | Fetch full observation by ID |
| `mnemos_delete` | Hard-delete a mistaken save |
| `mnemos_link` | related / caused_by / supersedes / contradicts / refines |
| `mnemos_session_start` | **Returns pre-warmed context block** (push, not pull) |
| `mnemos_session_end` | Close with summary, status, reflection |
| `mnemos_context` | Query-based context OR `mode=recovery` after compaction |
| `mnemos_correct` | Record tried / wrong_because / fix |
| `mnemos_convention` | Declare a project convention (auto-injected at session start) |
| `mnemos_touch` | File heat map: which files matter this project |
| `mnemos_skill_match` | Find matching procedures |
| `mnemos_skill_save` | Save or version a reusable procedure |
| `mnemos_stats` | System statistics incl. embedding status + storage size |

See [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md) for parameter details.

## Configuration

Mnemos works with zero config. `~/.mnemos/config.toml` is auto-created on first run. Everything below is optional.

```toml
[storage]
path = "~/.mnemos/mnemos.db"

[search]
decay_rate         = 0.05   # recency decay (higher = faster)
default_limit      = 20
max_context_tokens = 2000
hybrid_alpha       = 0.5    # 1.0 = pure BM25, 0.0 = pure vector

[embedding]
provider  = "auto"          # auto | ollama | openai | none
model     = "nomic-embed-text"
dimension = 768
base_url  = ""
api_key   = ""

[vault]
enabled        = false
path           = "~/.mnemos/vault"
watch_interval = "5m"

[dream]
interval     = ""           # e.g. "6h"; empty = off (run manually)
stale_days   = 30
decay_amount = 1

[server]
transport = "stdio"          # stdio | http
http_addr = ":8080"
api_key   = ""               # bearer token for HTTP; empty = auth off
```

## Architecture

- `internal/storage` — SQLite + FTS5, pure Go (`modernc.org/sqlite`), bi-temporal schema, embedded migrations
- `internal/memory` — Observation service, hybrid ranker, decay
- `internal/session` — Session lifecycle with status/reflection
- `internal/skills` — Procedural memory with versioning and effectiveness
- `internal/prewarm` — Composes session_start + compaction-recovery context blocks
- `internal/safety` — Promptware pattern scanner
- `internal/dream` — Consolidation daemon
- `internal/vault` — Obsidian export + watcher
- `internal/embedding` — Ollama / OpenAI / Noop providers, auto-probe
- `internal/mcp` — JSON-RPC stdio server, 14 tools, 3 resources
- `internal/api` — HTTP REST transport mirroring MCP
- `internal/installer` — Idempotent agent client wire-up
- `pkg/client` — Typed Go client for the HTTP API

More in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Testing

```bash
make test      # -race, full suite
make cover     # coverage.html report
make lint      # golangci-lint
```

Every package has tests. Current coverage: 63% overall, 70-95% on the core domain packages.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Short version: tests with every change, wrap every error, no globals, no CGO, no LLM calls inside the memory layer.

## License

MIT. See [LICENSE](LICENSE).
