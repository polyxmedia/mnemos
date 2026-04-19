# Architecture

Mnemos is a layered Go service with strict boundaries. This document walks the layers from the bottom up.

## Principles

- **Interfaces declared where they're consumed.** `memory.Reader/Writer/Maintenance/Exportable/Vectorable` live in the memory package because services depend on them; the `storage` package implicitly satisfies them. This is the stdlib `io.Reader` idiom — segregated, composable, easy to mock.
- **Transports are thin adapters.** `internal/mcp` wraps the official Model Context Protocol Go SDK; `internal/api` is a stdlib `net/http` adapter. Neither contains business logic.
- **No global state. No `init()` side effects. No reflection wiring.** All dependencies are constructor-injected.
- **Errors wrapped at every boundary** with `fmt.Errorf("context: %w", err)`. Nothing escapes naked.
- **Context propagation.** Every public method takes `context.Context` first.
- **Tests live next to code.** Table-driven where patterns repeat; end-to-end at each layer's public API.
- **Don't reinvent the wheel.** We use the official MCP SDK (protocol compliance + schema inference), `gopkg.in/yaml.v3` (robust YAML for vault frontmatter), `golang.org/x/sync/errgroup` (clean daemon lifecycle), `BurntSushi/toml` (config), `modernc.org/sqlite` (pure-Go driver), `oklog/ulid/v2` (sortable IDs). Everything else is stdlib.

## Layer map

```
┌──────────────────────────────────────────────────────────┐
│  cmd/mnemos                (CLI: serve, init, doctor, …) │
├──────────────────────────────────────────────────────────┤
│  internal/mcp              (JSON-RPC stdio server)       │
│  internal/api              (HTTP — planned)              │
├──────────────────────────────────────────────────────────┤
│  internal/prewarm          (push-based context assembly) │
│  internal/safety           (promptware scanner)          │
│  internal/dream            (consolidation + promotion)   │
├──────────────────────────────────────────────────────────┤
│  internal/memory           (Observation service)         │
│  internal/session          (Session service)             │
│  internal/skills           (Skill service)               │
├──────────────────────────────────────────────────────────┤
│  internal/storage          (SQLite + FTS5, migrations)   │
└──────────────────────────────────────────────────────────┘
```

## Storage

`modernc.org/sqlite` (pure Go, no CGO). One file at `~/.mnemos/mnemos.db`. WAL mode, foreign keys on, 5s busy timeout. Schema versioned via `schema_migrations` + numbered SQL files embedded via `//go:embed`.

Key design calls:

- **Bi-temporal**. Observations carry `valid_from`/`valid_until` (fact time) and `created_at`/`invalidated_at` (system time). Supersession invalidates rather than deletes. Historical queries work via `SearchInput.AsOf`.
- **FTS5 external-content virtual table** mirrors observations for BM25 search. Triggers keep it in sync on insert/update/delete.
- **Content hash on insert** powers dedup-on-save: identical content in the same `(agent, project)` bumps access count instead of duplicating.
- **Embeddings as BLOB**. `sqlite-vec` is a C extension that modernc.org/sqlite can't load; cosine similarity runs in pure Go over top-N BM25 candidates. Zero-CGO, zero dependency cost.

## Memory service

Ranking formula:

```
score = bm25 × importance_weight × recency_factor × access_factor

importance_weight = 0.5 + 0.5 × (importance / 10)       # 0.55..1.0
recency_factor    = (1 + age_days)^(-decay_rate)        # default 0.05
access_factor     = 1 + 0.1 × ln(1 + access_count)      # ACT-R base-level activation
```

`Save` returns a `SaveResult` indicating fresh insert vs dedup. Search pulls `limit × 3` raw hits, re-ranks, truncates to `limit`. Default filters to live-now; `IncludeStale` and `AsOf` opt into historical queries.

## Pre-warm

The big one. Agents don't self-invoke memory tools reliably, so `mnemos_session_start` doesn't hand back just an ID — it returns a pre-warmed block assembled from:

1. **Conventions** for the project (title + one-line rationale)
2. **Recent sessions** (3 most recent, summarised)
3. **Matching skills** (top 3 by FTS relevance on `goal`)
4. **Corrections** (top 3 on `goal` or `project`)
5. **Hot files** (top 5 by touch count for the project)

Each section is scanned by `safety.Scanner`. High-risk content gets a visible `[MNEMOS: FLAGGED]` banner; low-risk content is sanitised (zero-width + control chars stripped). The whole block is token-budgeted to ~500 tokens (curate aggressively; bloated context hurts more than no context).

Compaction-recovery mode reuses the pipeline but prioritises the current session's state: goal + in-session observations come before conventions.

## Skills

Procedural memory. `(agent_id, name)` is unique; saving the same name again bumps `version` instead of duplicating. `effectiveness` is `success_count / use_count`, nudges ranking. No LLM calls from inside the memory layer.

Skills arrive two ways. The primary path is agent-authored: the agent calls `mnemos_skill_save` and we store + rank. The secondary path is pattern-mined: the dream pass (see below) clusters correction observations and promotes them into skills when a cluster crosses a threshold. Promotion is pure string assembly over the structured correction records, so the "no LLM in the memory layer" invariant holds in both paths.

## Dream consolidation

`internal/dream` runs the sleep-time-compute pass, either one-shot (`mnemos dream`) or as a daemon (`mnemos dream --watch`). Each pass produces a `Journal` mirrored into memory as a `TypeDream` observation so the agent can query its own history via normal search.

Pipeline, in order:

1. **Prune** expired observations (`valid_until` passed or `expires_at` reached).
2. **Decay** importance on rows idle past `StaleDays`.
3. **Promote** skills from correction clusters. When three or more live corrections share an `(agent_id, project, label)` key — label = first tag, falling back to a normalised title prefix — the pass synthesises a skill with `## When this applies / ## Avoid / ## Do` sections. Idempotency is keyed on a 12-char sha256 of the group, carried as a `promoted-origin:<hash>` tag on the resulting skill. Repeat passes either no-op (nothing new) or version-bump (new corrections joined the group).
4. **Journal** — write the counts as a dream observation if any step changed state.

`dream.Config` takes the `memory.Reader` + `*skills.Service` for step 3; when either is nil the step silently skips so existing callers that predate promotion still work.

## MCP

We use the [official Model Context Protocol Go SDK](https://github.com/modelcontextprotocol/go-sdk) (v1.5.x, maintained in collaboration with Google). Each tool is registered via `mcp.AddTool[In, Out]` with a typed argument struct — JSON schemas are inferred from `json` and `jsonschema` struct tags, and the SDK validates inputs before invoking the handler. Our `internal/mcp` package is a thin wrapping layer (~750 LOC) that owns (a) the Config + NewServer wiring, (b) the 14 tool handlers, and (c) the 3 resource handlers. Protocol framing, JSON-RPC, version negotiation, cancellation, and transport plumbing are all the SDK's job.

Resources (`mnemos://session/current`, `mnemos://skills/index`, `mnemos://stats`) are read-only JSON snapshots.

## Installer / doctor

`internal/installer` auto-detects `~/.claude.json`, `~/.cursor/mcp.json`, `~/.codeium/windsurf/mcp_config.json`. Writes idempotently — unrelated keys preserved, atomic temp-file rename. `doctor` is the inverse: probes each target, reports green/red for binary path, config load, storage open, and each agent registration.

## What we deliberately don't do

- **No LLM calls from inside Mnemos.** Reflection and summaries are agent-authored. Skill promotion is the one place we go beyond pure storage, and even there it is deterministic pattern-mining over structured correction records — no model inference, no prompts.
- **No push of context mid-session by default.** Only on session_start and explicit recovery-mode calls. Otherwise it's too easy to flood the agent's context.
- **No vector DB dependency.** Optional embeddings stored as BLOBs in the same SQLite file. Zero infrastructure.
- **No multi-tenant SaaS wiring.** Local-first. Team deploy uses the HTTP transport; auth and ACL live downstream.
