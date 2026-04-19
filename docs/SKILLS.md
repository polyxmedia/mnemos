# Skills

Skills are procedural memory: reusable step-by-step procedures an agent has learned work. Distinct from observations, which are facts.

## Data model

A skill has:

- `name` — unique per `(agent_id, name)`
- `description` — one-line summary
- `procedure` — step-by-step markdown the agent can follow
- `pitfalls` — known failure modes
- `tags` — string array
- `source_sessions` — provenance: session IDs that contributed
- `use_count` / `success_count` / `effectiveness` — effectiveness is `success / use`, nudges ranking
- `version` — auto-bumps on save-with-same-name

## Lifecycle

1. **Save** via `mnemos_skill_save`. Same name = new version, keeping the original ID.
2. **Match** via `mnemos_skill_match` — FTS over name/description/procedure/tags, tie-broken by effectiveness.
3. **Use** — agent follows the procedure; on completion, records feedback (planned: `mnemos_skill_use` tool wiring effectiveness tracking end-to-end).

## Auto-promotion from corrections

Skills also appear without the agent calling `mnemos_skill_save` at all.

The dream consolidation pass scans live correction observations and clusters them by `(agent_id, project, label)` where `label` is the correction's first tag, falling back to the first three words of the title. When a cluster reaches three corrections, mnemos synthesises a skill from the underlying `tried / wrong_because / fix` records:

```
## When this applies
- <trigger_context from each correction, deduped>

## Avoid
- <tried> — <wrong_because>

## Do
- <fix>
```

The promoted skill is named `auto: <label> (<project>)` and carries three tags: `auto-promoted`, `promoted-origin:<12-char hash of agent|project|label>`, and `project:<name>`. The origin hash is the idempotency key: later passes find the existing skill by that tag and either no-op (when nothing new) or extend it (when new corrections joined the cluster), bumping the version and expanding `source_sessions`.

`mnemos stats` reports the count as `skills: N (M auto-promoted from corrections)`. `mnemos skill list` marks promoted entries with `[auto-promoted]`.

The mechanism is pattern-mining, not LLM synthesis — mnemos stays LLM-free at the memory layer. The agent authors the *corrections*; the dream pass compounds them into rules.

## When to save a skill vs an observation

## When to save a skill vs an observation

- **Observation**: a fact. "We decided on Postgres because of JSON support."
- **Skill**: a procedure. "How to add a new API route in this codebase."

If you'd write it as numbered steps, it's a skill. If you'd write it as a paragraph, it's an observation.

For recurring mistakes, prefer recording each one as a *correction* (`mnemos_correct`) and let the dream pass promote them into a skill automatically once a pattern emerges. That way the skill is grounded in evidence you can trace back to specific sessions via `source_sessions`.

## Effectiveness

`effectiveness = success_count / use_count`, clamped to [0, 1]. Skills with effectiveness below 0.3 over 10+ uses are candidates for pruning during consolidation (planned). Skills don't decay on time — they decay on *failure*, which is the right signal.

## What we don't do

- **LLM-driven skill synthesis** — promotion from corrections is pattern-mining, not model inference. The procedure text is a structured join of records the agent already wrote. No prompts, no tokens, no non-determinism. If you want richer synthesis, the agent can always re-save the promoted skill under a new name after rewriting the procedure itself.
- **Version conflicts** — there's no branching. Each upsert replaces the active procedure and bumps the version integer. Prior versions can be recovered from the session transcript if you exported one.
