-- 0009_cues_and_retrieval_events.sql: deterministic delivery and a decision log.
--
-- observation_cues: a cue is an action that makes a memory apply, written as
-- "cmd:git push --force" or "path:**/migrations/*.sql". The pre-tool hook
-- fires a memory whenever the agent is about to run a matching command or
-- edit a matching file, with no score involved. A blind gold set of 500 real
-- prompts (2026-11-06) found 58 of 79 relevant memory pairs were rules for the
-- same project, while ranking on the prompt's words reached 6% precision at
-- best. A rule tied to an action can be delivered at that action instead.
--
-- retrieval_events: one row per delivery decision a hook makes, fired or not,
-- with what it searched, the scores it saw and what it put in context. Health
-- checks used to rebuild this from transcripts, which broke whenever Claude
-- Code changed how it records hooks. created_at is written from Go.

CREATE TABLE observation_cues (
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    cue            TEXT NOT NULL,
    created_at     DATETIME NOT NULL,
    PRIMARY KEY (observation_id, cue)
);

CREATE TABLE retrieval_events (
    id              TEXT PRIMARY KEY,
    created_at      DATETIME NOT NULL,
    channel         TEXT NOT NULL,
    host_session_id TEXT,
    session_id      TEXT,
    project         TEXT,
    arm             TEXT,
    query           TEXT,
    fired           INTEGER NOT NULL DEFAULT 0,
    top_score       REAL,
    gap             REAL,
    candidates      TEXT,
    injected        TEXT,
    chars           INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_retrieval_events_host ON retrieval_events(host_session_id, created_at) WHERE host_session_id IS NOT NULL;
CREATE INDEX idx_retrieval_events_time ON retrieval_events(created_at);
