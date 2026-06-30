# mcp-memory-spec (draft)

**Status:** Draft for discussion. Not submitted.
**Relationship to mnemos:** mnemos is the reference implementation. Every field and operation below already ships in mnemos today, so this document describes a real system rather than a proposal hoping for one.
**Target venue:** an MCP SEP (Server Extension Proposal) against `modelcontextprotocol/modelcontextprotocol`. See ROADMAP Bet 3.
**Author:** Polyxmedia / mnemos.

> This is a working draft. Sections marked **(open)** need André's call before the spec goes anywhere. The point of writing it now is that a competitor (Open Memory Protocol, first commit 2026-06-29) planted a flag on the word "protocol" with a flat schema that omits everything hard about memory. We answer by publishing the richer spec we already implement, not by aligning down to theirs.

---

## 1. Motivation

Agent memory is siloed. Claude remembers a correction you gave it; Cursor and your own agent start from zero. The common answer to a silo problem is an open protocol, and that framing is correct.

The framing is also where most attempts stop. A portable bag of `{content, type, tags, timestamp}` records moves text between tools and calls it interoperability. It does not survive the parts that make memory hard:

- A fact that was true in March and false in June has no way to say so.
- A memory the agent inferred, or scraped from a tool's output, looks identical to one the user stated.
- A correction that supersedes an older belief has no edge to the thing it replaces, so the stale belief keeps surfacing.
- Nothing records *why* a memory exists or where it came from, so nothing can audit it.

A memory protocol worth standardising has to carry those properties on the wire. This draft makes bi-temporality, provenance, trust, and invalidation first-class, because those are the fields a flat schema cannot retrofit without a breaking change.

## 2. Goals and non-goals

### Goals

- Define a memory object whose temporal and provenance semantics are precise enough to query "what did the store believe at time T" and "where did this come from."
- Define a small operation set over MCP that any host can implement.
- Make capture deterministic and auditable. No model call lives inside the memory layer.
- Let a minimal implementation be conformant at a low tier while richer implementations advertise more, with the advertised tier bound to checkable behavior.

### Non-goals

- This spec does not define how an agent *uses* retrieved memory, how it ranks context, or how it composes a prompt.
- It does not mandate an embedding model or a vector dimension. Embeddings stay implementation-local and are never assumed portable across stores.
- It does not define encryption at rest or transport security beyond pointing at MCP's existing transport guarantees.
- It does not broker LLM calls. Extraction and summarisation belong to the agent, not the memory server. See §8.

## 3. Prior art and the baseline

Open Memory Protocol (OMP) is the closest living attempt and a useful baseline. Its object is `{id, content, type ∈ {episodic,semantic,procedural}, source.tool, tags, namespace, created_at, updated_at, expires_at}` over a REST surface. It is honest about being simple.

What it does not carry, and therefore what a serious spec must add:

| Capability | OMP | This spec |
|---|---|---|
| Bi-temporal validity (fact time vs system time) | only `expires_at` | `valid_from` / `valid_until` / `invalidated_at` / `created_at` |
| Invalidation with history | hard `DELETE` | `memory/invalidate`, soft, queryable as-of |
| Provenance of origin | `source.tool` string | `source_kind` enum + `derived_from[]` DAG |
| Trust gating | none | `trust_tier` with quarantine of untrusted writes |
| Inter-memory edges | none | typed links (`supersedes`, `contradicts`, `refines`, `caused_by`, `related`) |
| Honest retrieval signal | `mode_used` hardcoded to `keyword` | `retrieval_mode` derived from the real per-call outcome |
| Deterministic capture | optional server-side LLM `extract`/`compress` | no LLM in the memory layer, by rule |

The `episodic / semantic / procedural` taxonomy is good and we keep it as a subset of the type enum (§4.2). Everything else here is additive over the baseline.

## 4. The Memory object

A memory is an agent-curated record. Field names below match mnemos's `Observation` so the reference implementation is a direct mapping, not a translation.

### 4.1 Identity and content

| Field | Type | Notes |
|---|---|---|
| `id` | string | Stable unique identifier. Format is implementation-defined; mnemos uses a sortable id. |
| `title` | string | Short scannable label. Required. |
| `content` | string | The memory text in natural language. Required. |
| `type` | enum | See §4.2. Required. |
| `rationale` | string? | The *why* for decisions, conventions, and architecture records. Surfaced separately so it can be injected without inflating context. |
| `structured` | object? | Type-specific fields as JSON. A `correction` carries `{tried, wrong_because, fix}`. Empty for free-text types. |
| `tags` | string[] | Filtering and grouping. |
| `importance` | int 1..10 | Default 5. Feeds ranking. |

### 4.2 Type taxonomy

The required `type` is one of: `decision`, `bugfix`, `pattern`, `preference`, `context`, `architecture`, `episodic`, `semantic`, `procedural`, `correction`, `convention`. The cognitive trio `episodic / semantic / procedural` is the interop floor every implementation understands. The remaining types are coding-agent specializations a conformant store MAY collapse onto the trio when exporting to a lower-tier peer (`correction` and `convention` project onto `procedural` or `semantic`; `decision` and `bugfix` onto `episodic`). **(open)** whether the projection table is normative or advisory.

### 4.3 Bi-temporal lifecycle

Two independent time axes. System time records when the store learned or unlearned something. Fact time records the window during which the memory holds true.

| Field | Type | Axis | Notes |
|---|---|---|---|
| `created_at` | timestamp | system | When the store recorded it. |
| `invalidated_at` | timestamp? | system | When the store stopped trusting it. Null while live. |
| `valid_from` | timestamp | fact | Start of the fact's validity window. Defaults to `created_at`. |
| `valid_until` | timestamp? | fact | End of the validity window. Null means open-ended. |
| `expires_at` | timestamp? | system | Hard TTL for housekeeping. Distinct from fact-time invalidity. |

A memory is **live** at time `t` when `invalidated_at` is null, `valid_until` is null or after `t`, and `expires_at` is null or after `t`. Default retrieval returns only live memories. A caller MAY pass an `as_of` time to query the store's belief at a past moment, which is the property a flat schema cannot express.

### 4.4 Provenance and trust

| Field | Type | Notes |
|---|---|---|
| `source_kind` | enum | `user` \| `tool` \| `agent_inference` \| `dream` \| `import`. Who produced the content. Defaults to `user`. |
| `trust_tier` | enum | `raw` \| `curated` \| `skill`. Retrieval's coarse gate. Defaults to `curated`. |
| `derived_from` | string[] | Parent memory ids. Chains a `raw → curated → skill` DAG and records what a derived memory was built from. |

`raw` is a quarantine. Tool-output and agent-inferred writes SHOULD enter at `raw`, are excluded from default search and from any prewarmed context, and move to `curated` only through `memory/promote` (§5.6). This is the defense against memory poisoning: anything that can write a memory cannot, by writing alone, get that memory surfaced as a trusted fact.

### 4.5 Scoping

| Field | Type | Notes |
|---|---|---|
| `agent_id` | string | Which agent authored it. Default `default`. |
| `project` | string? | Project scope. Enables convention injection and project-scoped retrieval. |
| `session_id` | string? | The session it was captured in. |

### 4.6 Retrieval support

| Field | Type | Notes |
|---|---|---|
| `embedding` | float[]? | Implementation-local vector. **Never assumed portable across stores.** A peer importing a memory ignores a foreign embedding and re-embeds if it wants one. |
| `embedding_model` | string? | The model that produced the vector, so a store can re-embed on model change. |

## 5. Operations

Operations are MCP tool calls under a `memory/` namespace. The reference implementation exposes them as `mnemos_*` tools (§10). Names are **(open)** pending MCP SEP naming conventions.

### 5.1 `memory/save`

Create a memory. Accepts the §4 fields except the server-assigned ones (`id`, `created_at`, `invalidated_at`, `access_count`). Returns the stored object plus a `deduped` flag.

Dedup is mandatory and deterministic: a save whose normalised content hash already exists live in the same `(agent_id, project)` scope returns the existing memory with `deduped: true` and bumps its access counter, rather than inserting a twin. This is the front-line defense against the re-ingestion blowups that flat stores suffer.

### 5.2 `memory/search`

Ranked retrieval over a query string with filters (`type`, `tags`, `min_importance`, `agent_id`, `project`, `as_of`, `include_stale`, `include_raw`). Returns ranked hits and a `retrieval_mode`.

`retrieval_mode` ∈ `{fts, hybrid}` reports which path actually decided the ranking on this call. A hybrid-capable store returns `fts` when its embedder failed at query time or no candidate carried a vector, so the signal never overstates what ran. (OMP defines an analogous `mode_used` field and then hardcodes it; this spec requires the value reflect reality, which §9 makes a conformance criterion.)

Default search excludes `raw`-tier and non-live memories. `include_raw` and `include_stale` opt back in.

### 5.3 `memory/get`

Fetch a full memory by id. Bumps the access counter.

### 5.4 `memory/link`

Create a typed edge between two memories: `related` | `caused_by` | `supersedes` | `contradicts` | `refines`. `supersedes` has temporal force: when A supersedes B, B's `valid_until` is set to A's `valid_from`, so default search stops surfacing the stale fact without deleting it.

### 5.5 `memory/invalidate`

Mark a memory no longer valid as of a given time, without deleting it. Equivalent to a `supersedes` link with no replacement, or a direct set of `invalidated_at`. The memory stays queryable via `as_of`. Hard delete exists only for mistaken writes and is a separate destructive operation, not the path for changed facts.

### 5.6 `memory/promote`

Move a memory between trust tiers after validation. Requires `to_tier` and a `why_better` justification of at least 16 characters that names a concrete signal. The minimum-length guard is deliberate: promotion is the moment untrusted content becomes trusted, so it carries an auditable reason rather than a rubber stamp.

### 5.7 `memory/provenance`

Return the `derived_from` DAG and incoming/outgoing links for a memory, so a caller can see the full chain behind a fact in one call rather than walking edges itself.

### 5.8 `memory/capabilities`

Return the implementation's conformance tier (§9) and the list of capabilities actually live right now (e.g. whether hybrid retrieval is currently available). The advertised tier MUST be backed by behavior. A store that cannot fuse vectors MUST NOT advertise the embeddings tier. This is the one place OMP's design fails outright, advertising `OMP-Core` from a hardcoded string while implementing none of its higher tiers, and the failure is instructive: capability advertisement is worthless if it can lie.

## 6. Retrieval semantics

A conformant store MUST support keyword retrieval as the floor. Hybrid retrieval (keyword fused with vector similarity) is OPTIONAL and, when present, MUST be reported honestly per §5.2. Ranking formulae beyond "keyword relevance is the floor" are implementation-defined; the spec standardises the contract, not the scoring.

## 7. Determinism as a protocol stance

The memory layer runs no language model. Capture is structured at write time through typed operations. Consolidation, summarisation, and extraction are the agent's job, and their outputs enter the store as ordinary writes with `source_kind` set accordingly (`agent_inference` or `dream`).

This is a deliberate boundary, not an omission. A memory layer that calls an LLM to decide what to store inherits non-determinism (the same input yields different memories across runs), unauditability (no way to explain why a fact was or was not captured), cost and latency on every write, and a fresh hallucination-feedback surface. A store that records facts has to be the trustworthy floor under the stochastic thing above it. **(open)** whether this belongs in the normative spec as a MUST, or in a rationale appendix as the reference implementation's stance.

## 8. Out of scope: server-side LLM endpoints

For contrast, OMP v0.2 adds `extract` and `compress` endpoints where the server calls Anthropic or OpenAI with a key passed in the request body. This spec deliberately excludes that category. Transcript-to-memory extraction and session compression are agent-side concerns whose results arrive through `memory/save`. Putting them in the memory server couples a storage standard to model vendors and turns every write into a paid network round-trip.

## 9. Conformance tiers

An implementation declares its tier through `memory/capabilities`, and the declaration must be backed by behavior.

| Tier | Requires |
|---|---|
| **memory-core** | `save`, `search` (keyword), `get`, `link`, `invalidate`, bi-temporal fields, deterministic dedup, the type taxonomy floor (§4.2). |
| **+provenance** | core, plus `source_kind` / `trust_tier` / `derived_from`, raw-tier quarantine, `promote`, `provenance`. |
| **+embeddings** | core, plus hybrid retrieval with an honest `retrieval_mode`. |
| **+federation** | the above, plus cross-store export/import that preserves bi-temporal and provenance fields. **(open, depends on ROADMAP Bet 2 phase 4.)** |

A conformance test suite is part of the deliverable, not prose. The credibility gap in OMP is precisely that §8 of its spec is a checklist with nothing runnable behind it. mnemos's verify harness is the seed of the runnable version.

## 10. Reference implementation mapping

| Protocol operation | mnemos tool |
|---|---|
| `memory/save` | `mnemos_save` (also `mnemos_correct`, `mnemos_convention` as typed specializations) |
| `memory/search` | `mnemos_search` |
| `memory/get` | `mnemos_get` |
| `memory/link` | `mnemos_link` |
| `memory/invalidate` | `mnemos_link` with `supersedes`, or hard `mnemos_delete` for mistakes |
| `memory/promote` | `mnemos_promote` |
| `memory/provenance` | carried inline on `mnemos_search` hits (`source_kind`, `trust_tier`, `derived_from`); a dedicated call is **(open)** |
| `memory/capabilities` | **(open)**. Today `/healthz` returns `{ok:true}` and `mnemos_stats` exposes `embedding.enabled`; a real capabilities endpoint is unbuilt |

## 11. Versioning and extension governance

The spec version is independent of any implementation version. Breaking changes increment the major version. An implementation MAY serve multiple spec versions at once.

Field extension is a named mechanism, not a free-for-all and not a wall. Vendor-specific fields live under a reserved `x_` prefix and MUST be ignored by peers that do not understand them. Promotion of an `x_` field into the core schema goes through the SEP process. This is the gap OMP leaves open: its schema is `additionalProperties: false` with no extension namespace, so any vendor addition is a violation and the only escape hatch is a capped `metadata` bag.

## 12. Open questions for André

1. **Venue and timing.** Submit as an MCP SEP, or publish standalone first and submit once it has traction? The June roadmap keeps Bet 3 behind Bet 4 (measurement). Does OMP's existence change that ordering, or just mean we write the doc now and sit on it?
2. **Determinism as MUST or rationale.** §7. Is "no LLM in the memory layer" a normative requirement of the protocol, or mnemos's stated stance that the protocol merely permits?
3. **Type taxonomy.** §4.2. Is the projection table from coding-agent types onto the cognitive trio normative, or do we standardise only the trio and leave the rest as `x_` extensions?
4. **Naming.** `memory/*` method names, the `x_` extension prefix, and the spec's public name. "mcp-memory-spec" is a working title.
5. **Federation tier.** §9. Gate it behind Bet 2 phase 4 (fidelity-preserving export/import) shipping first, or spec it now as a forward declaration?

---

## Changelog

| Version | Date | Changes |
|---|---|---|
| draft-0 | 2026-06-30 | Initial draft. Grounded in mnemos's shipped schema; framed against OMP as the minimal baseline. |
