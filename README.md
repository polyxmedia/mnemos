# Mnemos

<p align="center">
  <strong>Persistent memory and skills for AI coding agents.</strong><br/>
  MCP-native · single Go binary · zero runtime dependencies.
</p>

<p align="center">
  <a href="https://github.com/polyxmedia/mnemos/releases"><img src="https://img.shields.io/github/v/release/polyxmedia/mnemos?sort=semver" alt="release"/></a>
  <a href="https://github.com/polyxmedia/mnemos/actions/workflows/ci.yml"><img src="https://github.com/polyxmedia/mnemos/actions/workflows/ci.yml/badge.svg" alt="CI"/></a>
  <a href="https://codecov.io/gh/polyxmedia/mnemos"><img src="https://codecov.io/gh/polyxmedia/mnemos/branch/main/graph/badge.svg" alt="coverage"/></a>
  <a href="https://pkg.go.dev/github.com/polyxmedia/mnemos"><img src="https://pkg.go.dev/badge/github.com/polyxmedia/mnemos.svg" alt="go reference"/></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT"/></a>
</p>

---

Mnemos is not another memory dump. It's a structured cognitive substrate: agents **save what matters, skip repeat mistakes via a correction journal, inherit project conventions across sessions, and recover from compaction** without losing their train of thought.

```bash
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh | bash
mnemos init
# that's it. restart your agent.
```

## Why Mnemos

Most AI memory tools treat memory as a bucket: dump conversations in, hope retrieval finds the right one. Mnemos is different:

- **Push, not pull.** `mnemos_session_start` **returns** a pre-warmed context block (conventions + recent sessions + matching skills + corrections + hot files). LLMs don't reliably call memory tools on their own — so we push at the one moment they're guaranteed to look.
- **Correction journal.** Agents record what was tried, why it was wrong, and the fix. Next session, the correction surfaces **before** the same mistake is made again. Compounds over weeks.
- **Compaction recovery.** When the agent's context gets compacted mid-session, one call to `mnemos_context` in recovery mode restores the goal, decisions, and in-session observations. Nobody else has this.
- **Hybrid retrieval.** BM25 (exact terms) + cosine similarity (paraphrases) via Reciprocal Rank Fusion. Auto-enables if Ollama is running, falls back to pure FTS5 silently.
- **Bi-temporal truth.** Facts are invalidated, not deleted. "We used to use X, now Y" queries work. No context poisoning from stale facts.
- **Promptware sanitisation.** Memory stores are a new attack surface. Mnemos scans at the injection boundary for injection patterns and flags high-risk content before the agent sees it. First in class.
- **Pure Go, zero CGO.** One static binary for Linux / macOS / Windows, amd64 + arm64. No Docker, no Python, no vector DB. 15 MB.

## Install

| Method | Command |
| --- | --- |
| One-liner | `curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh \| bash` |
| Homebrew | `brew install polyxmedia/tap/mnemos` |
| Go | `go install github.com/polyxmedia/mnemos/cmd/mnemos@latest` |
| Manual | [Download a release binary](https://github.com/polyxmedia/mnemos/releases) |

All paths end with `mnemos init`, which auto-wires Claude Code, Cursor, and Windsurf MCP configs. Then restart your agent.

## Quick start

```
$ mnemos init
  ✓ Claude Code (user) registered at ~/.claude.json
  restart your agent. the mnemos_* tools will appear next session.

$ mnemos doctor
  ✓ binary path: /usr/local/bin/mnemos
  ✓ config: ~/.mnemos/config.toml
  ✓ storage: ~/.mnemos/mnemos.db (0 observations)
  ✓ Claude Code (user) ~/.claude.json
  all checks passed.
```

From your agent (first session on a project):

```
mnemos_session_start(project="my-repo", goal="fix the login bug")
→ session_id + a ~500-token prewarm block with any declared
   conventions, recent sessions, matching skills, hot files.
```

Declare a convention once (surfaces in every future session on this project):

```
mnemos_convention(
  title="error wrapping",
  rule="use fmt.Errorf with %w",
  rationale="preserves the chain for errors.Is",
  project="my-repo"
)
```

Record a correction when something goes wrong:

```
mnemos_correct(
  title="oauth retry without backoff",
  tried="retry on 401",
  wrong_because="401 is auth failure, not transient",
  fix="refresh token, then retry once",
  project="my-repo"
)
```

## Agent setup

`mnemos init` auto-detects your agent and wires the MCP config idempotently. If you prefer to set it up by hand (or you're on a client we don't auto-detect yet), here's what goes where.

### Claude Code

`~/.claude.json` (user-global) — add an entry under `mcpServers`:

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

Restart Claude Code. The `mnemos_*` tools appear on next session.

### Cursor

`~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "mnemos": { "command": "/full/path/to/mnemos", "args": ["serve"] }
  }
}
```

### Windsurf

`~/.codeium/windsurf/mcp_config.json`:

```json
{
  "mcpServers": {
    "mnemos": { "command": "/full/path/to/mnemos", "args": ["serve"] }
  }
}
```

### OpenAI Codex CLI

Codex reads MCP servers from `~/.codex/config.toml`:

```toml
[mcp_servers.mnemos]
command = "/full/path/to/mnemos"
args    = ["serve"]
```

### Zed / Continue / any MCP-compatible client

Anything that speaks MCP over stdio can talk to Mnemos. Point the client's tool config at the `mnemos serve` binary. The server advertises 14 tools + 3 resources on the `initialize` handshake.

### Remote / team setup (HTTP)

For multi-agent, remote, or team setups, run the HTTP transport:

```bash
MNEMOS_API_KEY=$(openssl rand -hex 32) mnemos serve --http :8080
```

Then use `pkg/client` from Go, or call `POST /v1/observations` and friends directly. Full reference in [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md).

## How it compares

Based on public documentation as of April 2026. If anything's wrong, [open an issue](https://github.com/polyxmedia/mnemos/issues) — we'll fix it.

|  | Mnemos | Mem0 | Zep | MemPalace |
| --- | :---: | :---: | :---: | :---: |
| Language / runtime | Go (single binary) | Python service | Go server + Postgres/Neo4j | Python + ChromaDB |
| MCP-native | ✓ | via bridge | via bridge | ✓ |
| Bi-temporal model | ✓ (facts + system time) | temporal extraction | ✓ (Graphiti) | validity windows |
| Hybrid retrieval | BM25 + vectors (RRF) | vectors + LLM rerank | hybrid graph + vectors | vectors |
| Local-first (no API required) | ✓ | — (SaaS primary) | ✓ (self-host) | ✓ |
| Auto-enables Ollama if present | ✓ | — | — | — |

**Things we think are unique to Mnemos** (we haven't found them in the others' public docs; if we missed something, let us know):

- **Push-based session prewarm** — `mnemos_session_start` returns a composed context block instead of just an ID.
- **Compaction recovery mode** — dedicated API for restoring session state after the agent's context was compacted.
- **Structured correction journal** — `tried / wrong_because / fix` as a first-class observation type with retrieval boosting.
- **Prompt-injection scanner at the injection boundary** — flags instruction-override / role-spoof / zero-width-unicode patterns before content reaches the agent.
- **Obsidian vault export** — full markdown graph with wikilinks.

**What others do better than us:**

- **Mem0** has the largest community (48k+ GitHub stars, rich integrations library). Mnemos is new.
- **Zep/Graphiti** has a more sophisticated knowledge graph with entity extraction. Mnemos deliberately keeps the graph simple (typed links between observations).
- **MemPalace** has verbatim conversation mining. Mnemos is agent-curated by design — higher signal but requires the agent to actively save.

**Not included in the table** because they're a different category: [Hermes Agent](https://github.com/NousResearch/hermes-agent) is an end-to-end agent runtime (terminals, messaging, model routing), not a memory layer. Mnemos plugs into whatever agent you already use; Hermes is the agent. Complementary, not competing.

## CLI

| Command | Purpose |
| --- | --- |
| `mnemos serve` | Start the MCP stdio server (default) |
| `mnemos serve --http :8080` | Start the HTTP API |
| `mnemos init` | Auto-wire agent clients |
| `mnemos doctor` | Verify install, DB, and registrations |
| `mnemos search <query>` | Search from the terminal |
| `mnemos stats` | Counts, top tags, recent sessions |
| `mnemos sessions` | List recent sessions |
| `mnemos export [file]` | JSON dump |
| `mnemos import <file>` | Restore from JSON |
| `mnemos prune` | Remove expired observations |
| `mnemos dream [--watch]` | Consolidation pass (or daemon) |
| `mnemos vault export\|watch\|status` | Obsidian vault sync |
| `mnemos embed status\|backfill` | Embedding provider tools |
| `mnemos config` | Print current config |
| `mnemos version` | Print version |

## MCP tools (14)

`mnemos_save` · `mnemos_search` · `mnemos_get` · `mnemos_delete` · `mnemos_link` · `mnemos_session_start` · `mnemos_session_end` · `mnemos_context` · `mnemos_correct` · `mnemos_convention` · `mnemos_touch` · `mnemos_skill_match` · `mnemos_skill_save` · `mnemos_stats`

See [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md) for parameter details.

## FAQ

### Do I need embeddings?

No. Mnemos runs pure FTS5 (BM25) by default and works great. If Ollama is running on your machine, vector search auto-enables and retrieval improves on paraphrased queries (~10pp recall bump on LongMemEval-style benchmarks). Zero config either way.

### Will this slow Claude Code down?

No. Session start returns in <10 ms with a 500-token prewarm block. Every search is a single SQLite query with BM25 ranking — typically sub-millisecond. The whole tool surface is designed so the agent gets more useful context *for fewer tokens*.

### How does memory not pollute my agent's context?

Three guardrails:
1. Strict token budgets on every inject path (prewarm ≤500, context tool ≤2000 by default).
2. Importance weighting + recency decay so stale stuff gets buried in ranking.
3. A prompt-injection scanner at the injection boundary that flags or sanitises high-risk content before the agent sees it.

### What happens after I do `git commit` or close my terminal?

Nothing changes. Mnemos stores everything in `~/.mnemos/mnemos.db` (a SQLite file). Starts when your agent calls `mnemos_session_start`, runs while your agent is live, idles otherwise. No daemon needed.

### Is my data sent anywhere?

Only if you explicitly configure an OpenAI-compatible embedder. By default Mnemos uses local FTS5 or local Ollama. Nothing leaves your machine. The HTTP API is optional and off by default.

### Why Go?

Single static binary, cross-compiles to Linux/macOS/Windows × amd64/arm64. No CGO (we use `modernc.org/sqlite`), so no compiler toolchain on the install path. Docker-free, Python-free, Node-free.

### How is this different from Hermes Agent?

Hermes is an end-to-end agent runtime (terminals, messaging platforms, model routing). Mnemos is only the memory layer — designed to plug into whatever agent you already use (Claude Code, Cursor, Windsurf, or any MCP client). Complementary, not competing.

### How is this different from Mem0 / Zep / MemPalace?

See the [comparison table above](#how-it-compares). Short version: Mem0 and Zep are Python/infra-heavy services; MemPalace is Python + ChromaDB and mines verbatim conversations. Mnemos is one Go binary, agent-curated, and ships with compaction recovery + correction journal + promptware sanitisation — none of which we've found in the others' public docs.

### Is Mnemos production-ready?

v0.1.x is stable API but early in adoption. Schema is bi-temporal so migrations are non-breaking. 70% test coverage (80-95% on core domain packages). Every feature end-to-end tested. Issues + contributions welcome.

## Configuration

Zero config required. `~/.mnemos/config.toml` is auto-created on first run. Every field is optional.

```toml
[storage]
path = "~/.mnemos/mnemos.db"

[search]
decay_rate         = 0.05   # recency decay rate
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

[server]
transport = "stdio"          # stdio | http
http_addr = ":8080"
api_key   = ""               # bearer token when http
```

## Architecture

- `internal/storage` — SQLite + FTS5, pure Go (`modernc.org/sqlite`), bi-temporal schema, embedded migrations
- `internal/memory` — Observations, hybrid ranker (BM25 + cosine via RRF), decay
- `internal/session` / `internal/skills` — Session and procedural memory services
- `internal/prewarm` — Composes the session_start + compaction-recovery blocks
- `internal/safety` — Promptware pattern scanner
- `internal/dream` — Consolidation daemon
- `internal/vault` — Obsidian export + watcher (gopkg.in/yaml.v3)
- `internal/embedding` — Ollama / OpenAI / Noop providers, auto-probe
- `internal/mcp` — Wraps the [official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- `internal/api` — HTTP REST transport (generic jsonIn/pathOnly helpers)
- `internal/installer` — Idempotent agent client wire-up
- `pkg/client` — Typed Go client for the HTTP API

More in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Testing + release

```bash
make test           # -race, full suite across 15 packages
make cover          # coverage.html report
make lint           # golangci-lint
make release V=v0.2.0   # tag + push → GH Actions runs goreleaser
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Short version: tests with every change, wrap every error, no globals, no CGO, no LLM calls inside the memory layer.

## License

MIT. See [LICENSE](LICENSE).

## About

Built by [Polyxmedia](https://polyxmedia.com). Follow [@voidmode](https://x.com/voidmode) for Mnemos updates, agent research, and build-in-public notes.

If Mnemos helps you ship, a star is the fastest way to tell us. Issues and PRs welcome.
