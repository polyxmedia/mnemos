-- 0005_injection_channels.sql — move injections enum validation to Go.
--
-- 0004 shipped kind/channel CHECK constraints. New surfacing channels
-- (pre_tool just-in-time memory, premortem) arrive faster than releases,
-- and SQLite cannot ALTER a CHECK — every new channel would force a table
-- rebuild like this one. So this rebuild drops the enum CHECKs for good;
-- validation lives in Go (injection.Kind.Valid / injection.Channel.Valid,
-- enforced by the storage layer on write), where adding a channel is a
-- one-line change next to the constant that defines it.
--
-- 0004 is locked (shipped in v0.6.0) so the change is a new migration.

CREATE TABLE injections_new (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    ref_id     TEXT NOT NULL,
    channel    TEXT NOT NULL,
    agent_id   TEXT NOT NULL DEFAULT '',
    project    TEXT,
    session_id TEXT,
    created_at DATETIME NOT NULL
);

INSERT INTO injections_new SELECT id, kind, ref_id, channel, agent_id, project, session_id, created_at FROM injections;

DROP TABLE injections;

ALTER TABLE injections_new RENAME TO injections;

CREATE INDEX idx_injections_ref ON injections(kind, ref_id, created_at DESC);
CREATE INDEX idx_injections_session ON injections(session_id) WHERE session_id IS NOT NULL;
