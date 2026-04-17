# Mnemos

<p align="center">
  <strong>Persistent memory and skills for AI coding agents.</strong><br/>
  MCP-native · single Go binary · zero runtime dependencies.<br/>
  <sub>by <a href="https://x.com/voidmode">André Figueira</a> · <a href="https://polyxmedia.com">Polyxmedia</a></sub>
</p>

<p align="center">
  <a href="https://github.com/polyxmedia/mnemos/releases"><img src="https://img.shields.io/github/v/release/polyxmedia/mnemos?sort=semver" alt="release"/></a>
  <a href="https://github.com/polyxmedia/mnemos/actions/workflows/ci.yml"><img src="https://github.com/polyxmedia/mnemos/actions/workflows/ci.yml/badge.svg" alt="CI"/></a>
  <a href="https://codecov.io/gh/polyxmedia/mnemos"><img src="https://codecov.io/gh/polyxmedia/mnemos/branch/main/graph/badge.svg" alt="coverage"/></a>
  <a href="https://pkg.go.dev/github.com/polyxmedia/mnemos"><img src="https://pkg.go.dev/badge/github.com/polyxmedia/mnemos.svg" alt="go reference"/></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT"/></a>
</p>

---

Mnemos introduces the **correction journal**: a structured record of what the agent tried, why it was wrong, and the fix. It surfaces before the same mistake repeats.

```json
{
  "type": "correction",
  "tried": "retry on 401",
  "wrong_because": "401 is auth failure, not transient",
  "fix": "refresh token, then retry once"
}
```

Two more primitives we haven't seen in other memory layers: **compaction recovery** (rebuild session state after the agent's context was compacted) and **retrospective replay** (query a past session with everything learned since). Delivered on a single Go binary with no Python, no Docker, and no vector DB required.

```bash
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh | bash
mnemos init
# that's it. restart your agent.
```

## Novel primitives

**Correction journal.** `tried / wrong_because / fix` as a first-class observation type with retrieval boosting. The agent records a mistake once; next session, the correction surfaces before the same path is taken again. Compounds over weeks of use.

**Compaction recovery.** When Claude Code (or any agent) compacts its context mid-session, one call to `mnemos_context` in recovery mode restores the goal, decisions, and in-session observations. A dedicated API surface, built for this case.

**Retrospective replay.** `mnemos replay <session_id>` generates a markdown recap of a past session with everything learned since: corrections recorded after, conventions added after, skills promoted after, and observations that have been superseded. Paste it back into your agent and ask what you'd do differently now.

## Also in the box

- **Dynamic composed prewarm.** `mnemos_session_start` returns a ranked, token-budgeted context block (conventions + recent sessions + matching skills + corrections + hot files) at the one moment LLMs are guaranteed to look. `mnemos init` wires a Claude Code `SessionStart` hook so the push fires automatically on every launch, not only when the agent thinks to call the tool.
- **Hybrid retrieval.** BM25 (exact terms) plus cosine similarity (paraphrases) via Reciprocal Rank Fusion. Auto-enables if Ollama is running, falls back to pure FTS5 silently.
- **Bi-temporal store.** Facts carry valid/invalid timestamps so history stays queryable. "We used to use X, now Y" works without context poisoning. (Zep/Graphiti does this too.)
- **Prompt-injection scanner.** Memory stores are a new attack surface. Mnemos scans at the injection boundary for instruction-override, role-spoof, and zero-width unicode patterns, and flags high-risk content before the agent sees it.
- **Portable skill packs.** Export any skill (or all of them) as a JSON pack, share via file or URL, install with `mnemos skill import https://...`. Runtime stats stripped, pack versioning strict.
- **Obsidian vault export.** Full markdown graph with wikilinks.
- **Pure Go, zero CGO.** One static binary for Linux / macOS / Windows, amd64 + arm64. 15 MB. Docker-free, Python-free, vector-DB-free.

## Install

| Method | Command |
| --- | --- |
| One-liner | `curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh \| bash` |
| Homebrew | `brew install polyxmedia/tap/mnemos` |
| Go | `go install github.com/polyxmedia/mnemos/cmd/mnemos@latest` |
| Manual | [Download a release binary](https://github.com/polyxmedia/mnemos/releases) |

All paths end with `mnemos init`, which auto-wires Claude Code, Claude Desktop, Cursor, Windsurf, and OpenAI Codex CLI. Then restart your agent.

**Updating:** `mnemos update` downloads the latest release, verifies its sha256 against the published `checksums.txt`, and replaces the running binary in place. Add `--yes` to skip the confirmation prompt.

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

`~/.claude.json` (user-global) with an entry under `mcpServers`:

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

`mnemos init` also writes a `SessionStart` hook to `~/.claude/settings.json` (honours `CLAUDE_CONFIG_DIR`) that calls `mnemos prewarm` at session startup. Claude Code injects the prewarm block as additional context, so conventions + recent sessions + matching corrections land in front of the agent on every launch without the agent having to call `mnemos_session_start` first. Manual shape:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup",
        "hooks": [
          { "type": "command", "command": "/full/path/to/mnemos prewarm", "timeout": 10 }
        ]
      }
    ]
  }
}
```

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

### Claude Desktop

Auto-wired by `mnemos init`. Config path: `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows). Same `mcpServers.mnemos` shape as Claude Code.

### OpenAI Codex CLI

Auto-wired by `mnemos init` at `~/.codex/config.toml`:

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

Mnemos is new (v0.1.x, early adoption). Table based on public documentation as of April 2026. If anything's wrong, [open an issue](https://github.com/polyxmedia/mnemos/issues) and we'll fix it.

|  | Mnemos | Mem0 | Zep | MemPalace |
| --- | :---: | :---: | :---: | :---: |
| Language / runtime | Go (single binary) | Python service | Go server + Postgres/Neo4j | Python + ChromaDB |
| MCP-native | ✓ | via bridge | via bridge | ✓ |
| Bi-temporal model | ✓ (facts + system time) | temporal extraction | ✓ (Graphiti) | validity windows |
| Hybrid retrieval | BM25 + vectors (RRF) | vectors + LLM rerank | hybrid graph + vectors | vectors |
| Local-first (no API required) | ✓ | — (SaaS primary) | ✓ (self-host) | ✓ |
| Auto-enables Ollama if present | ✓ | — | — | — |

**What Mnemos adds** (we haven't found these in the others' public docs; if we missed something, let us know):

- Correction journal as a first-class observation type with retrieval boosting
- Compaction recovery as a dedicated API surface for restoring session state post-compaction
- Retrospective session replay with post-session learnings layered in
- Dynamic composed prewarm on session start
- Prompt-injection scanning at the memory-write boundary
- Portable JSON skill packs with URL install
- Obsidian vault export (markdown graph with wikilinks)

**What others do better:**

- **Mem0** has the largest community (48k+ GitHub stars, rich integrations library). Mnemos is new.
- **Zep/Graphiti** has a more sophisticated knowledge graph with entity extraction. Mnemos keeps the graph simple by design (typed links between observations).
- **MemPalace** has verbatim conversation mining. Mnemos is agent-curated: higher signal, but requires the agent to actively save.

**Not included in the table** because it's a different category: [Hermes Agent](https://github.com/NousResearch/hermes-agent) is an end-to-end agent runtime (terminals, messaging, model routing). Mnemos is only the memory layer, so it plugs into whatever agent you already use. Complementary.

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
| `mnemos skill list` | Show installed skills |
| `mnemos skill export [names...] [--out file]` | Build a shareable skill pack |
| `mnemos skill import <file-or-url>` | Install a pack from disk or an `https://` URL |
| `mnemos replay <session_id>` | Markdown recap of a past session + what you've learned since |
| `mnemos prewarm [flags]` | Print the session_start prewarm block (used by the Claude Code hook) |
| `mnemos update [--yes]` | Download the latest release, verify sha256, and replace this binary |
| `mnemos config` | Print current config |
| `mnemos version` | Print version |

## MCP tools (14)

`mnemos_save` · `mnemos_search` · `mnemos_get` · `mnemos_delete` · `mnemos_link` · `mnemos_session_start` · `mnemos_session_end` · `mnemos_context` · `mnemos_correct` · `mnemos_convention` · `mnemos_touch` · `mnemos_skill_match` · `mnemos_skill_save` · `mnemos_stats`

See [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md) for parameter details.

## FAQ

### Do I need embeddings?

No. Mnemos runs pure FTS5 (BM25) by default and works great. If Ollama is running on your machine, vector search auto-enables and retrieval improves on paraphrased queries (~10pp recall bump on LongMemEval-style benchmarks). Zero config either way.

### Will this slow Claude Code down?

No. Session start returns in <10 ms with a 500-token prewarm block. Every search is a single SQLite query with BM25 ranking, typically sub-millisecond. The whole tool surface is designed so the agent gets more useful context *for fewer tokens*.

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

Hermes is an end-to-end agent runtime (terminals, messaging platforms, model routing). Mnemos is only the memory layer, designed to plug into whatever agent you already use (Claude Code, Cursor, Windsurf, or any MCP client). Complementary.

### How is this different from Mem0 / Zep / MemPalace?

See the [comparison table above](#how-it-compares). Short version: Mem0 and Zep are Python/infra-heavy services; MemPalace is Python + ChromaDB and mines verbatim conversations. Mnemos is one Go binary, agent-curated, and ships with a correction journal, compaction recovery, and prompt-injection scanning. None of those appear in the others' public docs.

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

- `internal/storage`: SQLite + FTS5, pure Go (`modernc.org/sqlite`), bi-temporal schema, embedded migrations
- `internal/memory`: observations, hybrid ranker (BM25 + cosine via RRF), decay
- `internal/session` / `internal/skills`: session and procedural memory services
- `internal/prewarm`: composes the session_start + compaction-recovery blocks
- `internal/safety`: prompt-injection pattern scanner
- `internal/dream`: consolidation daemon
- `internal/vault`: Obsidian export + watcher (gopkg.in/yaml.v3)
- `internal/embedding`: Ollama / OpenAI / Noop providers, auto-probe
- `internal/mcp`: wraps the [official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- `internal/api`: HTTP REST transport (generic jsonIn/pathOnly helpers)
- `internal/installer`: idempotent agent client wire-up
- `pkg/client`: typed Go client for the HTTP API

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

Created by [André Figueira](https://x.com/voidmode) at [Polyxmedia](https://polyxmedia.com). See [AUTHORS.md](AUTHORS.md) and [ROADMAP.md](ROADMAP.md).

If Mnemos helps you ship, a star is the fastest way to tell us. Issues and PRs welcome.
