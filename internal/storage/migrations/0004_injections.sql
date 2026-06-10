-- 0004_injections.sql — injection-event log, the measurement substrate.
--
-- One row per memory per surfacing into agent context. Before this table,
-- passive injection (prewarm, prompt hook) left no trace: observations only
-- bumped access_count on explicit Get, so surfaced-vs-used ratios were
-- unmeasurable. Downstream consumers: mnemos digest, outcome-gated skill
-- promotion, and Bet 4's causal attribution.
--
-- created_at is written from Go (never CURRENT_TIMESTAMP): injection events
-- are compared against session and observation timestamps at sub-second
-- granularity, and a whole-second floor would merge nearby surfacings.

CREATE TABLE injections (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('observation', 'skill')),
    ref_id     TEXT NOT NULL,
    channel    TEXT NOT NULL CHECK (channel IN ('prewarm', 'recovery', 'context', 'prompt_hook')),
    agent_id   TEXT NOT NULL DEFAULT '',
    project    TEXT,
    session_id TEXT,
    created_at DATETIME NOT NULL
);

CREATE INDEX idx_injections_ref ON injections(kind, ref_id, created_at DESC);
CREATE INDEX idx_injections_session ON injections(session_id) WHERE session_id IS NOT NULL;
