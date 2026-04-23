-- 0003_provenance.sql
-- Bet 2 phase 1: provenance substrate for observations.
--
-- Every observation now carries:
--   source_kind   — who/what produced the content:
--                   user              explicit user-authored save
--                   tool              derived from a tool call's output
--                   agent_inference   the agent's own conclusion
--                   dream             consolidation / promotion output
--                   import            imported from another store
--
--   trust_tier    — how much weight to give it in retrieval & prewarm:
--                   raw               recorded, not yet validated; excluded
--                                     from prewarm and search unless asked
--                   curated           trusted enough to surface by default
--                   skill             promoted into a procedural skill
--
--   derived_from  — JSON array of parent observation IDs. Chains raw →
--                   curated → skill transitions and tool-output → user-
--                   confirmed promotions. The scaffold a provenance DAG
--                   composes on top of.
--
-- Existing rows default to user/curated/[] — the bi-temporal invariant
-- stays intact and back-compat is preserved.

ALTER TABLE observations ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'user'
    CHECK (source_kind IN ('user','tool','agent_inference','dream','import'));

ALTER TABLE observations ADD COLUMN trust_tier TEXT NOT NULL DEFAULT 'curated'
    CHECK (trust_tier IN ('raw','curated','skill'));

ALTER TABLE observations ADD COLUMN derived_from TEXT NOT NULL DEFAULT '[]';

CREATE INDEX IF NOT EXISTS idx_obs_trust_tier  ON observations(trust_tier);
CREATE INDEX IF NOT EXISTS idx_obs_source_kind ON observations(source_kind);
