# Mnemos

Persistent memory and skills for AI coding agents. MCP-native, single Go binary, zero runtime dependencies.

[![release](https://img.shields.io/github/v/release/polyxmedia/mnemos?sort=semver)](https://github.com/polyxmedia/mnemos/releases)
[![CI](https://github.com/polyxmedia/mnemos/actions/workflows/ci.yml/badge.svg)](https://github.com/polyxmedia/mnemos/actions/workflows/ci.yml)
[![coverage](https://codecov.io/gh/polyxmedia/mnemos/branch/main/graph/badge.svg)](https://codecov.io/gh/polyxmedia/mnemos)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

Your agent retries on a 401, decides the failure is transient, loops. You correct it. Next session, before it walks the same path, this lands in its prewarm context:

```json
{
  "type": "correction",
  "tried": "retry on 401",
  "wrong_because": "401 is auth failure, not transient",
  "fix": "refresh token, then retry once"
}
```

That's the atomic unit. Record three corrections that cluster around the same `(agent, project, topic)` and Mnemos promotes them into a skill with `## When this applies`, `## Avoid`, and `## Do` sections, synthesised from the underlying records. Run `mnemos replay <session_id>` and any past session comes back as markdown with everything you've learned since layered in: corrections recorded after, conventions added after, skills promoted after, observations marked superseded by later facts.

The memory layer itself never calls an LLM. Promotion is deterministic pattern-mining over structured records, so the behaviour is reproducible, token-free, and auditable. Delivered on one static Go binary with no Python, no Docker, no vector DB, no CGO.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh | bash
mnemos doctor
```

The installer runs `mnemos init` for you and auto-wires Claude Code, Claude Desktop, Cursor, Windsurf, and OpenAI Codex CLI. Restart your agent and the `mnemos_*` tools appear next session.

For Claude Code, also install the skill so the agent records back to the store instead of silently editing:

```bash
mkdir -p ~/.claude/skills/mnemos
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/.claude/skills/mnemos/SKILL.md \
  -o ~/.claude/skills/mnemos/SKILL.md
```

Other paths:

| Method | Command |
| --- | --- |
| Go | `go install github.com/polyxmedia/mnemos/cmd/mnemos@latest` |
| Manual | [Download a release binary](https://github.com/polyxmedia/mnemos/releases) |

`mnemos update` swaps in the latest release in place after verifying its sha256 against the published `checksums.txt`. Add `--yes` to skip the confirmation prompt.

## Does it actually help?

Memory tools are easy to claim and hard to verify. Mnemos ships a self-test harness for both the read side (do memories surface and change behaviour?) and the write side (does the agent capture them in the first place?).

```bash
mnemos verify retrieval   # cheap: do memories surface for their trigger queries?
mnemos verify behavior    # expensive: does the agent behave differently on/off?
mnemos verify capture     # expensive: does the agent record corrections handed to it?
mnemos verify all         # all three
```

Behaviour A/B from a 5-scenario, n=5 paired run against the dev store:

```
scenario                        on    off   lift
session_start_on_edit           5/5   0/5   +100%
no_ai_attribution_in_commit     5/5   5/5   +0%
no_cgo_proposal                 5/5   5/5   +0%
migration_locked_refused        5/5   5/5   +0%
oss_first_for_protocol          5/5   0/5   +100%
                                ─────────   ─────
overall                         25/25 15/25 +40%
```

Read this honestly. Mnemos wins decisively where the convention is contrarian or project-specific. On `oss_first_for_protocol` the off-arm hand-rolls a JSON-RPC server every time; the on-arm reaches for the official SDK every time. On the recursive case (`session_start_on_edit`) the off-arm never calls a single mnemos tool when starting work; the on-arm reliably orients itself. On widely-known best practices (no AI attribution, pure-Go SQLite, no editing shipped migrations) Claude already gets it right from training, so Mnemos adds no measurable lift, and importantly does no harm.

The off arm runs with `MNEMOS_DISABLED=1` to no-op every globally-installed mnemos hook plus `--strict-mcp-config` and an empty MCP config to kill the tool surface. Auth and other settings stay intact. Off-arm transcripts are spot-checked for mnemos artefacts as a contamination canary.

Write-side capture from a 5-scenario, n=3 run after two rounds of lever tuning:

```
scenario                        captured  rate
explicit_save_request           3/3       100%
inline_correction               2/3        67%
quiet_convention_mention        2/3        67%
architectural_decision          0/3         0%
silent_correction_mid_work      1/3        33%
                                ────────  ─────
overall                         8/15       53%
```

Initial baseline was 7%. Most of the 7→53% jump came from a UserPromptSubmit hook that detects correction-shaped phrasing and emits a `[mnemos: capture required]` directive into context; a smaller chunk from trigger-phrase examples in MCP tool descriptions. Architectural decisions buried inside larger tasks ("we just decided X, now show me Y") still get skipped, and parenthetical corrections are spotty. The harness exists so the next change is evaluated against a real number. Fixtures and runner scripts at [`verify/`](verify/).

## The learning loop

Three primitives compose into a feedback cycle. Each one is useful on its own; together they turn a memory store into something that gets sharper over weeks of use.

### Correction journal

`tried / wrong_because / fix` is a first-class observation type with retrieval boosting. The agent records a mistake once; next session, the correction surfaces before the same path is taken again.

### Corrections to skills

The dream pass clusters accumulated corrections by `(agent, project, topic)`. When three or more land in the same cluster, Mnemos mints an auto-promoted skill synthesised from the underlying records. Promotion is deterministic pattern-mining (no LLM, no prompts, no drift) and idempotent via a stable origin hash, so a repeat pass extends the existing skill, bumping version and provenance, instead of duplicating it.

### Retrospective replay

`mnemos replay <session_id>` regenerates a past session as markdown with everything learned since layered in: corrections recorded after, conventions added after, skills promoted after, observations that have been superseded (flagged inline). Paste it back into your agent and ask what you would do differently now. The feedback signal that closes the loop.

### Rumination

The destructive counterpart to promotion. Four threshold monitors run in the dream pass and raise rumination candidates when stored knowledge stops holding up:

1. a skill whose effectiveness fell below the floor,
2. a skill untouched for months that never earned its slot,
3. a skill whose topic keeps accumulating corrections after it was promoted,
4. a convention explicitly contradicted via a `contradicts` link.

`mnemos_ruminate_list` surfaces pending candidates. `mnemos_ruminate_pack` returns a review block with the hypothesis verbatim, disconfirming evidence, a falsifiable restatement of the rule, and hostile review prompts the agent must answer before proposing a revision. Resolution requires a structured `why_better` field naming a new prediction the revision makes that the old version did not. Popper's falsifiability guard, enforced at the tool boundary. Revisions invalidate the old version through the bi-temporal store; the rumination's origin stays on the new version as a `ruminated-from:<id>` tag, and the dream pass auto-closes candidates whose target carries that tag. The memory store self-corrects through adversarial review.

## Also in the box

* **Prompt-injection scanner at the memory-write boundary.** Memory stores are a new attack surface: any tool that writes observations can plant instruction-overrides, zero-width unicode, bidi overrides, fake tool-call syntax, or MCP spoofing into what the agent reads next session. Mnemos scans at injection time, sanitises low-risk content, and wraps high-risk content in a visible `[MNEMOS: FLAGGED]` banner before it reaches the model.
* **Compaction recovery.** When Claude Code (or any agent) compacts its context mid-session, one call to `mnemos_context` in `recovery` mode restores the goal, decisions, and in-session observations. A dedicated API surface for the case.
* **Dynamic composed prewarm.** `mnemos_session_start` returns a ranked, token-budgeted block (conventions + recent sessions + matching skills + corrections + hot files) at the one moment LLMs are guaranteed to look. `mnemos init` wires a Claude Code `SessionStart` hook so the push fires automatically on every launch.
* **Hybrid retrieval.** BM25 (exact terms) plus cosine similarity (paraphrases) via Reciprocal Rank Fusion. Auto-enables if Ollama is running, falls back to pure FTS5 silently. No vector DB.
* **Bi-temporal store.** Facts carry valid/invalid timestamps so history stays queryable. "We used to use X, now Y" works without context poisoning. (Zep/Graphiti does this too.)
* **Portable skill packs.** Export any skill (or all of them) as a JSON pack, share via file or URL, install with `mnemos skill import https://...`. Runtime stats stripped, pack versioning strict.
* **Obsidian vault export.** Full markdown graph with wikilinks.
* **Pure Go, zero CGO.** One static binary for Linux / macOS / Windows, amd64 + arm64. 15 MB.

## Quick start

After `mnemos init` on Claude Code, the startup hook opens a session automatically and injects `mnemos_session_id` into context. For other MCP clients, or if hooks are disabled, start a session from your agent:

```
mnemos_session_start(project="my-repo", goal="fix the login bug")
→ session_id + a ~500-token prewarm block with any declared
   conventions, recent sessions, matching skills, hot files.
```

Declare a convention once and it surfaces in every future session on this project:

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

`mnemos doctor` verifies the install, db, and registrations:

```
$ mnemos doctor
  ✓ binary path: /usr/local/bin/mnemos
  ✓ config: ~/.mnemos/config.toml
  ✓ storage: ~/.mnemos/mnemos.db (0 observations)
  ✓ Claude Code (user) ~/.claude.json
  all checks passed.
```

## Agent setup

`mnemos init` auto-detects your agent and wires the MCP config idempotently. If you'd rather configure by hand, here's what goes where.

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

`mnemos init` also writes a `SessionStart` hook to `~/.claude/settings.json` (honours `CLAUDE_CONFIG_DIR`) that calls `mnemos prewarm` at session startup. `mnemos prewarm` opens a session by default and prints `mnemos_session_id`, and Claude Code injects the prewarm block as additional context. Conventions, recent sessions, and matching corrections land in front of the agent on every launch without the agent having to call `mnemos_session_start` first. Manual shape:

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

**Skill (recommended).** The prewarm hook pushes context in; the skill pushes the agent to record back out. Without it, agents tend to skip `mnemos_*` tool calls on plain editing tasks and the store goes empty:

```bash
mkdir -p ~/.claude/skills/mnemos
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/.claude/skills/mnemos/SKILL.md \
  -o ~/.claude/skills/mnemos/SKILL.md
```

Next session, Claude Code loads the skill and invokes it on triggers like "save this", "remember", "we were wrong about", on any correction, and at session start/end. Source: [`.claude/skills/mnemos/SKILL.md`](.claude/skills/mnemos/SKILL.md).

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

Anything that speaks MCP over stdio can talk to Mnemos. Point the client's tool config at the `mnemos serve` binary. The server advertises 16 baseline tools plus 3 resources on the `initialize` handshake, or 20 tools when rumination is enabled.

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
| Local-first (no API required) | ✓ | (SaaS primary) | ✓ (self-host) | ✓ |
| Auto-enables Ollama if present | ✓ | | | |

What Mnemos adds on top (we couldn't find these in the others' public docs; if we missed something, open an issue):

* The learning loop triad: correction journal, deterministic corrections-to-skills promotion, retrospective replay
* LLM-free consolidation: pattern-mining over structured records, reproducible and token-free by design
* Prompt-injection scanning at the memory-write boundary
* Compaction recovery as a dedicated API surface
* Dynamic composed prewarm on session start, auto-fired via a Claude Code `SessionStart` hook
* Portable JSON skill packs with URL install
* Obsidian vault export

What others do better:

* **Mem0** has the largest community (48k+ stars, rich integrations library). Mnemos is new.
* **Zep/Graphiti** has a more sophisticated knowledge graph with entity extraction. Mnemos keeps the graph simple by design (typed links between observations).
* **MemPalace** mines verbatim conversations. Mnemos is agent-curated: higher signal, requires the agent to actively save.

Different category: [Hermes Agent](https://github.com/NousResearch/hermes-agent) is an end-to-end agent runtime (terminals, messaging, model routing). Mnemos is only the memory layer, so it plugs into whatever agent you already use. Complementary.

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
| `mnemos replay <session_id>` | Markdown recap of a past session and what you've learned since |
| `mnemos prewarm [flags]` | Print the session_start prewarm block (used by the Claude Code hook) |
| `mnemos update [--yes]` | Download the latest release, verify sha256, replace this binary |
| `mnemos config` | Print current config |
| `mnemos version` | Print version |

## MCP tools (16 baseline, 20 with rumination)

`mnemos_save` · `mnemos_search` · `mnemos_get` · `mnemos_delete` · `mnemos_link` · `mnemos_session_start` · `mnemos_session_end` · `mnemos_context` · `mnemos_promote` · `mnemos_correct` · `mnemos_convention` · `mnemos_touch` · `mnemos_skill_match` · `mnemos_skill_save` · `mnemos_skill_score` · `mnemos_stats` · `mnemos_ruminate_list` · `mnemos_ruminate_pack` · `mnemos_ruminate_resolve` · `mnemos_ruminate_dismiss`

See [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md) for parameter details. The four `mnemos_ruminate_*` tools are exposed only when `[rumination].enabled = true` in config (the default).

## FAQ

### Do I need embeddings?

No. Mnemos runs pure FTS5 (BM25) by default and works great. If Ollama is running on your machine, vector search auto-enables and retrieval improves on paraphrased queries (~10pp recall bump on LongMemEval-style benchmarks). Zero config either way.

### Will this slow Claude Code down?

No. Session start returns in <10 ms with a 500-token prewarm block. Every search is a single SQLite query with BM25 ranking, typically sub-millisecond. The whole tool surface is designed so the agent gets more useful context for fewer tokens.

### How does memory not pollute my agent's context?

Three guardrails:

1. Strict token budgets on every inject path (prewarm ≤500, context tool ≤2000 by default).
2. Importance weighting plus recency decay so stale stuff gets buried in ranking.
3. A prompt-injection scanner at the injection boundary that flags or sanitises high-risk content before the agent sees it.

### What happens after I do `git commit` or close my terminal?

Nothing changes. Mnemos stores everything in `~/.mnemos/mnemos.db` (a SQLite file). With Claude Code hooks, `mnemos prewarm` opens the session automatically; other clients start one with `mnemos_session_start`. No daemon needed.

### Is my data sent anywhere?

Only if you explicitly configure an OpenAI-compatible embedder. By default Mnemos uses local FTS5 or local Ollama. Nothing leaves your machine. The HTTP API is optional and off by default.

### Why Go?

Single static binary, cross-compiles to Linux/macOS/Windows × amd64/arm64. No CGO (we use `modernc.org/sqlite`), so no compiler toolchain on the install path. Docker-free, Python-free, Node-free.

### Is Mnemos production-ready?

v0.1.x is stable API but early in adoption. Schema is bi-temporal so migrations are non-breaking. 70% test coverage (80 to 95% on core domain packages). Every feature end-to-end tested. Issues and contributions welcome.

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

[rumination]
enabled                     = true   # threshold-breach detection in the dream pass
skill_effectiveness_floor   = 0.3    # SkillEffectivenessMonitor: flag skills below this
skill_min_uses              = 10     # statistical floor before the effectiveness monitor fires
stale_skill_days            = 90     # StaleSkillMonitor: untouched + underperforming for this long
stale_skill_floor           = 0.5    # staleness triggers only when effectiveness is below this
correction_repeat_n         = 3      # CorrectionRepeatUnderSkillMonitor: corrections after skill exists
contradiction_threshold     = 1      # ContradictionDetectedMonitor: min contradicts-links before flagging

[server]
transport = "stdio"          # stdio | http
http_addr = ":8080"
api_key   = ""               # bearer token when http
```

## Architecture

* `internal/storage`: SQLite + FTS5, pure Go (`modernc.org/sqlite`), bi-temporal schema, embedded migrations
* `internal/memory`: observations, hybrid ranker (BM25 + cosine via RRF), decay
* `internal/session` / `internal/skills`: session and procedural memory services
* `internal/prewarm`: composes the session_start and compaction-recovery blocks
* `internal/safety`: prompt-injection pattern scanner
* `internal/dream`: consolidation daemon
* `internal/rumination`: threshold-breach detection and hostile-review packaging (LLM-free)
* `internal/vault`: Obsidian export and watcher (gopkg.in/yaml.v3)
* `internal/embedding`: Ollama / OpenAI / Noop providers, auto-probe
* `internal/mcp`: wraps the [official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
* `internal/api`: HTTP REST transport (generic jsonIn/pathOnly helpers)
* `internal/installer`: idempotent agent client wire-up
* `pkg/client`: typed Go client for the HTTP API

More in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Testing and release

```bash
make test           # -race, full suite
make cover          # coverage.html report
make lint           # golangci-lint
make release V=v0.2.0   # tag + push, GH Actions runs goreleaser
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Short version: tests with every change, wrap every error, no globals, no CGO, no LLM calls inside the memory layer.

## License

MIT. See [LICENSE](LICENSE).

By [André Figueira](https://x.com/voidmode) at [Polyxmedia](https://polyxmedia.com). See [AUTHORS.md](AUTHORS.md) and [ROADMAP.md](ROADMAP.md). Issues and PRs welcome.
