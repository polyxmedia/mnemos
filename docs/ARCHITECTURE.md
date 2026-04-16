# Architecture

Mnemos is a layered Go service with strict boundaries. This document walks the layers from the bottom up.

## Principles

- **Domain packages own interfaces. Storage implements them.** `memory`, `session`, and `skills` declare `Store` interfaces. `internal/storage` satisfies all three, sharing a single `*sql.DB`.
- **Transports are thin adapters.** `internal/mcp` translates JSON-RPC into service calls. A future `internal/api` will do the same for HTTP. No business logic in transports.
- **No global state. No `init()` side effects. No reflection wiring.** All dependencies are constructor-injected.
- **Errors wrapped at every boundary.** `fmt.Errorf("context: %w", err)`. Nothing escapes naked.
- **Context propagation.** Every public method takes `context.Context` first.
- **Tests live next to code.** Table-driven where patterns repeat; end-to-end at each layer's public API.

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

Procedural memory. `(agent_id, name)` is unique; saving the same name again bumps `version` instead of duplicating. `effectiveness` is `success_count / use_count`, nudges ranking. No LLM calls from inside the memory layer — the agent authors skill content; we store and rank.

## MCP

Minimal JSON-RPC 2.0 over newline-delimited JSON on stdio. Handlers are a `map[string]toolHandler`; schemas are inline JSON Schema. No generated code, no reflection. Resources (`mnemos://session/current`, `mnemos://skills/index`, `mnemos://stats`) are read-only snapshots.

## Installer / doctor

`internal/installer` auto-detects `~/.claude.json`, `~/.cursor/mcp.json`, `~/.codeium/windsurf/mcp_config.json`. Writes idempotently — unrelated keys preserved, atomic temp-file rename. `doctor` is the inverse: probes each target, reports green/red for binary path, config load, storage open, and each agent registration.

## What we deliberately don't do

- **No LLM calls from inside Mnemos.** Reflection, skill extraction, summaries — all agent-authored. We store and rank. The agent thinks.
- **No push of context mid-session by default.** Only on session_start and explicit recovery-mode calls. Otherwise it's too easy to flood the agent's context.
- **No vector DB dependency.** Optional embeddings stored as BLOBs in the same SQLite file. Zero infrastructure.
- **No multi-tenant SaaS wiring.** Local-first. Team deploy uses the HTTP transport; auth and ACL live downstream.
