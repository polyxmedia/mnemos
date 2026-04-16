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

## When to save a skill vs an observation

- **Observation**: a fact. "We decided on Postgres because of JSON support."
- **Skill**: a procedure. "How to add a new API route in this codebase."

If you'd write it as numbered steps, it's a skill. If you'd write it as a paragraph, it's an observation.

## Effectiveness

`effectiveness = success_count / use_count`, clamped to [0, 1]. Skills with effectiveness below 0.3 over 10+ uses are candidates for pruning during consolidation (planned). Skills don't decay on time — they decay on *failure*, which is the right signal.

## What we don't do

- **Auto-extract skills from sessions** — the agent authors them explicitly. LLM-driven extraction lives in the agent, not in Mnemos. We store and rank; the agent thinks.
- **Version conflicts** — there's no branching. Each upsert replaces the active procedure and bumps the version integer. Prior versions can be recovered from the session transcript if you exported one.
