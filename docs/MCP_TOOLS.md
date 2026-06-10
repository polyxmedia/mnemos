# MCP tools reference

All tool names are namespaced with `mnemos_`. Every tool has a JSON Schema in its definition — your agent client shows these automatically. A standard server exposes 15 core tools; enabling rumination adds 4 more.

## Save & retrieve

### `mnemos_save`
Store an agent-curated observation.

| Param | Required | Notes |
|---|---|---|
| `title` | ✓ | short scannable label |
| `content` | ✓ | the memory itself (structure as what/why/where/learned) |
| `type` | ✓ | `decision`, `bugfix`, `pattern`, `preference`, `context`, `architecture`, `episodic`, `semantic`, `procedural`, `correction`, `convention`, `dream` |
| `tags` |   | string array |
| `importance` |   | 1-10, defaults to 5 |
| `ttl_days` |   | auto-expire |
| `agent_id`, `project`, `session_id` |   | scoping |
| `valid_from`, `valid_until` |   | ISO-8601 fact-time bounds |
| `source_kind` |   | `user`, `tool`, `agent_inference`, `dream`, `import`; defaults to `user` |
| `trust_tier` |   | `raw`, `curated`, `skill`; defaults to `curated` |
| `derived_from` |   | parent observation IDs |
| `rationale` |   | why this memory matters |

Returns `{id, title, type, source_kind, trust_tier, derived_from, created_at, deduped}`. `deduped: true` means an identical live observation already existed; its access counter was bumped instead.

### `mnemos_search`
BM25 + importance + recency + access-frequency ranked search.

| Param | Required | Notes |
|---|---|---|
| `query` | ✓ | FTS query string |
| `type` |   | filter by observation type |
| `tags` |   | array, AND-joined |
| `min_importance` |   | floor |
| `limit` |   | default 20, max 100 |
| `agent_id`, `project` |   | scoping |
| `include_stale` |   | include invalidated/expired |
| `include_raw` |   | include quarantined raw-tier observations |
| `as_of` |   | ISO-8601, historical query |

Returns `{results: [{id, title, type, tags, importance, score, snippet, created_at, source_kind, trust_tier, derived_from}]}`.

### `mnemos_get`
Fetch full observation by ID. Bumps access counter.

### `mnemos_delete`
Hard-delete by ID. Use only for mistaken saves. For changed facts, use `mnemos_link` with `supersedes`.

### `mnemos_link`
| `source_id`, `target_id`, `link_type` | all required |

`link_type`: `related | caused_by | supersedes | contradicts | refines`. `supersedes` automatically invalidates the target so default searches no longer surface the stale fact.

## Sessions

### `mnemos_session_start`
**Returns a pre-warmed context block, not just an ID.** The block composes conventions for the project, recent session summaries, top matching skills, correction-journal matches on the goal, and hot files.

| Param | Required | Notes |
|---|---|---|
| `project` |   | recommended — enables convention injection |
| `goal` |   | recommended — improves skill and correction matching |
| `agent_id` |   | for multi-agent setups |

Returns `{session_id, started_at, prewarm: {text, token_estimate, section_count, safety_risk}}`. Token budget: ~500 (curated; bloat hurts).

### `mnemos_session_end`
| Param | Required | Notes |
|---|---|---|
| `session_id` | ✓ | |
| `summary` | ✓ | what shipped, what broke, what was learned |
| `reflection` |   | transferable lessons — drives skill promotion |
| `status` |   | `ok` \| `failed` \| `blocked` \| `abandoned` |
| `outcome_tags` |   | short tags characterising the outcome |

Observations from `failed` sessions get a ranking boost — agents learn faster from what went wrong.

### `mnemos_context`
Two modes.

**Default (query-based)**:
```json
{"query": "...", "max_tokens": 2000, "agent_id": "...", "project": "..."}
```
Token-budgeted search-and-pack.

**Recovery (after compaction)**:
```json
{"mode": "recovery", "session_id": "...", "project": "...", "goal": "..."}
```
Restores current session goal, in-session observations, conventions. The "oh shit, context just got compacted" button.

### `mnemos_premortem`
Submit a plan before executing it; returns how similar attempts failed.

```json
{"plan": "add oauth retry handling to the api client", "project": "...", "max_tokens": 800}
```

Sections, failure-first: matching corrections (wider cap than the prewarm block, full tried/wrong/fix substance), past sessions with overlapping goals that ended `failed`/`blocked`/`abandoned`, applicable skills (flagged when under rumination review), and conventions the plan must respect. Empty result returns an explicit "no recorded failures match — proceed" verdict so the agent knows the store was consulted, not skipped. Surfaced memories are recorded in the injection log under the `premortem` channel.

## Provenance

### `mnemos_promote`
Promote an observation between trust tiers after validation.

| Param | Required | Notes |
|---|---|---|
| `id` | ✓ | observation ID |
| `to_tier` | ✓ | `raw`, `curated`, or `skill` |
| `why_better` | ✓ | one sentence naming the concrete signal that justifies promotion; min 16 chars |

Typical path: `raw` to `curated` after tool-output content is verified, or `curated` to `skill` when a pattern is durable enough to reuse.

## Agent supercharge

### `mnemos_correct`
Record a mistake and its fix. Higher retrieval weight than regular observations.

| `title`, `tried`, `wrong_because`, `fix` | all required |
| `trigger_context`, `tags`, `agent_id`, `project`, `session_id`, `importance` | optional |

Surfaced automatically in pre-warm when the session goal matches.

### `mnemos_convention`
Declare a project convention. Auto-injected at every `mnemos_session_start` for the matching project.

| `title`, `rule`, `project` | required |
| `rationale`, `example`, `tags`, `agent_id` | optional |

Rationale is surfaced in pre-warm — WHY matters more than what.

### `mnemos_touch`
Record that a file was touched in the current session. Builds a heat map: frequently-touched files get priority in pre-warming.

| `path`, `project` | required |
| `session_id`, `agent_id`, `note` | optional |

## Skills

### `mnemos_skill_match`
Find skills matching a query. Effectiveness (success/use ratio) nudges ranking so skills that actually worked rise up.

### `mnemos_skill_save`
Save or version a reusable procedure. Keyed by `(agent_id, name)` — same name bumps `version`.

### `mnemos_skill_score`
Return a structured quality report for one skill: raw `effectiveness` (success/use ratio), `use_count`, `success_count`, Bayesian-shrunk `adjusted_effectiveness` (so a 1/1 record does not outrank 9/10), a `recency` factor that decays linearly to zero over 90 days, and a composite 0-1 `score`. Use this when ranking, selecting, or reporting skill quality from measurements rather than guesses.

| Parameter | Notes |
| --- | --- |
| `id` | required — the skill ID returned by `mnemos_skill_save` or `mnemos_skill_match` |

The composite `score` is `adjusted_effectiveness * recency`, intentionally simple so callers can recompute their own weighting from the components. Forms the basis of "skills ranked by measured lift" registries.

## Rumination

Four tools, exposed only when `[rumination].enabled = true` in config (the default). The pattern:

1. `mnemos_ruminate_list` → pick a candidate ID from the pending queue
2. `mnemos_ruminate_pack(id)` → read the review block, answer the hostile prompts
3. `mnemos_ruminate_resolve(id, resolved_by, why_better)` OR `mnemos_ruminate_dismiss(id, reason)`

Server-side validation enforces Popper's falsifiability guard at the resolve boundary: `why_better` must name a new prediction the revision makes. Cosmetic rewording is rejected.

### `mnemos_ruminate_list`
Return pending rumination candidates ordered severity-desc, detected-at-desc. Each candidate has `id`, `monitor`, `severity`, `reason`, `target_kind`, `target_id`, `detected_at`, `evidence_n`. Response also includes `counts` (pending / resolved / dismissed totals) so the agent can report store health at a glance.

| Parameter | Notes |
| --- | --- |
| `limit` | optional; 0 = all |

### `mnemos_ruminate_pack`
Fetch the full review block for one candidate: hypothesis verbatim, disconfirming evidence, falsifiable restatement, hostile-review prompts (steel-man → fatal flaw → falsification vs noise → context shift → new prediction), and an action section naming the provenance tag the revision must carry.

| Parameter | Notes |
| --- | --- |
| `id` | required — candidate ID from `mnemos_ruminate_list` |

### `mnemos_ruminate_resolve`
Close a candidate by naming the revision that replaces the flagged belief.

| Parameter | Notes |
| --- | --- |
| `id` | required |
| `resolved_by` | required — ID of the new skill version or superseding observation |
| `why_better` | required, min 16 chars — one sentence naming a concrete new prediction the revision makes that the old version did not. The Popper guard. |

Idempotent when called a second time with the same `resolved_by`. Rejects attempts to resolve an already-resolved candidate with a different `resolved_by` (that's a conflict; either the store is wrong or the agent is).

### `mnemos_ruminate_dismiss`
Close a candidate as noise. The rule stands.

| Parameter | Notes |
| --- | --- |
| `id` | required |
| `reason` | required, min 8 chars — why the evidence was insufficient to force a revision. Preserved so a later dream pass does not re-raise the same flag without context. |

## Stats

### `mnemos_stats`
Counts, top tags, recent sessions. When rumination is enabled, the response also includes a `rumination` object with `pending`, `resolved`, and `dismissed` counts.

## Resources

- `mnemos://session/current` — most recent open session
- `mnemos://skills/index` — all skills (slim)
- `mnemos://stats` — system statistics

## Safety

Pre-warm and recovery blocks scan their content for prompt-injection patterns (instruction-override phrases, role spoofing, fake tool syntax, zero-width unicode, bidi overrides). High-risk sections are wrapped in `[MNEMOS: FLAGGED risk=high rules=...]` before injection. Low-risk content is sanitised (zero-width + control chars stripped) silently. Memory stores are a new attack surface; we treat them like any other untrusted input source.
