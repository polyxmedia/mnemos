-- Mnemos schema v1
-- Bi-temporal: system time (created_at, invalidated_at) separate from
-- fact time (valid_from, valid_until). Facts are invalidated, never deleted.
-- Connection-level pragmas (WAL, foreign_keys, synchronous) are set in the
-- DSN by storage.Open — setting them inside a migration transaction errors.

CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL DEFAULT 'default',
    project         TEXT,
    goal            TEXT,
    summary         TEXT,
    reflection      TEXT,
    status          TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok','failed','blocked','abandoned')),
    outcome_tags    TEXT NOT NULL DEFAULT '[]',
    started_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at        DATETIME
);

CREATE INDEX IF NOT EXISTS idx_sessions_agent   ON sessions(agent_id);
CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project);
CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_status  ON sessions(status);

CREATE TABLE IF NOT EXISTS observations (
    id               TEXT PRIMARY KEY,
    session_id       TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    agent_id         TEXT NOT NULL DEFAULT 'default',
    project          TEXT,
    title            TEXT NOT NULL,
    content          TEXT NOT NULL,
    obs_type         TEXT NOT NULL CHECK (obs_type IN (
        'decision','bugfix','pattern','preference','context',
        'architecture','episodic','semantic','procedural',
        'correction','convention','dream'
    )),
    tags             TEXT NOT NULL DEFAULT '[]',
    importance       INTEGER NOT NULL DEFAULT 5 CHECK (importance BETWEEN 1 AND 10),
    access_count     INTEGER NOT NULL DEFAULT 0,
    last_accessed_at DATETIME,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    valid_from       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    valid_until      DATETIME,
    invalidated_at   DATETIME,
    expires_at       DATETIME,
    content_hash     TEXT,
    structured       TEXT,                      -- JSON blob for type-specific fields (correction.tried, etc.)
    rationale        TEXT,                      -- the WHY for decisions and conventions
    embedding        BLOB,                      -- optional float32 LE-encoded vector
    embedding_model  TEXT,                      -- model ID used (so we can re-embed when we change models)
    last_exported_at DATETIME                   -- vault export tracking
);

CREATE INDEX IF NOT EXISTS idx_obs_agent_project  ON observations(agent_id, project);
CREATE INDEX IF NOT EXISTS idx_obs_session        ON observations(session_id);
CREATE INDEX IF NOT EXISTS idx_obs_type           ON observations(obs_type);
CREATE INDEX IF NOT EXISTS idx_obs_created        ON observations(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_obs_valid_until    ON observations(valid_until);
CREATE INDEX IF NOT EXISTS idx_obs_invalidated    ON observations(invalidated_at);
CREATE INDEX IF NOT EXISTS idx_obs_expires        ON observations(expires_at);
CREATE INDEX IF NOT EXISTS idx_obs_content_hash   ON observations(agent_id, project, content_hash);
CREATE INDEX IF NOT EXISTS idx_obs_exported       ON observations(last_exported_at);

-- FTS5 virtual table mirrors content for BM25 search.
CREATE VIRTUAL TABLE IF NOT EXISTS observations_fts USING fts5(
    title, content, tags,
    content='observations',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

CREATE TRIGGER IF NOT EXISTS observations_ai AFTER INSERT ON observations BEGIN
    INSERT INTO observations_fts(rowid, title, content, tags)
    VALUES (new.rowid, new.title, new.content, new.tags);
END;

CREATE TRIGGER IF NOT EXISTS observations_ad AFTER DELETE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, tags)
    VALUES ('delete', old.rowid, old.title, old.content, old.tags);
END;

CREATE TRIGGER IF NOT EXISTS observations_au AFTER UPDATE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, tags)
    VALUES ('delete', old.rowid, old.title, old.content, old.tags);
    INSERT INTO observations_fts(rowid, title, content, tags)
    VALUES (new.rowid, new.title, new.content, new.tags);
END;

CREATE TABLE IF NOT EXISTS observation_links (
    source_id   TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    target_id   TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    link_type   TEXT NOT NULL CHECK (link_type IN (
        'related','caused_by','supersedes','contradicts','refines'
    )),
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (source_id, target_id, link_type)
);

CREATE INDEX IF NOT EXISTS idx_links_target ON observation_links(target_id);

CREATE TABLE IF NOT EXISTS skills (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL DEFAULT 'default',
    name            TEXT NOT NULL,
    description     TEXT NOT NULL,
    procedure       TEXT NOT NULL,
    pitfalls        TEXT,
    tags            TEXT NOT NULL DEFAULT '[]',
    source_sessions TEXT NOT NULL DEFAULT '[]',
    use_count       INTEGER NOT NULL DEFAULT 0,
    success_count   INTEGER NOT NULL DEFAULT 0,
    effectiveness   REAL NOT NULL DEFAULT 0.0 CHECK (effectiveness BETWEEN 0.0 AND 1.0),
    version         INTEGER NOT NULL DEFAULT 1,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_exported_at DATETIME,
    UNIQUE(agent_id, name)
);

CREATE INDEX IF NOT EXISTS idx_skills_agent ON skills(agent_id);
CREATE INDEX IF NOT EXISTS idx_skills_name  ON skills(name);

CREATE VIRTUAL TABLE IF NOT EXISTS skills_fts USING fts5(
    name, description, procedure, tags,
    content='skills',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

CREATE TRIGGER IF NOT EXISTS skills_ai AFTER INSERT ON skills BEGIN
    INSERT INTO skills_fts(rowid, name, description, procedure, tags)
    VALUES (new.rowid, new.name, new.description, new.procedure, new.tags);
END;

CREATE TRIGGER IF NOT EXISTS skills_ad AFTER DELETE ON skills BEGIN
    INSERT INTO skills_fts(skills_fts, rowid, name, description, procedure, tags)
    VALUES ('delete', old.rowid, old.name, old.description, old.procedure, old.tags);
END;

CREATE TRIGGER IF NOT EXISTS skills_au AFTER UPDATE ON skills BEGIN
    INSERT INTO skills_fts(skills_fts, rowid, name, description, procedure, tags)
    VALUES ('delete', old.rowid, old.name, old.description, old.procedure, old.tags);
    INSERT INTO skills_fts(rowid, name, description, procedure, tags)
    VALUES (new.rowid, new.name, new.description, new.procedure, new.tags);
END;

-- File heat map: tracks which files an agent has touched, per project, per session.
CREATE TABLE IF NOT EXISTS file_touches (
    project      TEXT NOT NULL,
    agent_id     TEXT NOT NULL DEFAULT 'default',
    path         TEXT NOT NULL,
    session_id   TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    note         TEXT,
    touched_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project, agent_id, path, session_id, touched_at)
);

CREATE INDEX IF NOT EXISTS idx_touches_path    ON file_touches(project, agent_id, path);
CREATE INDEX IF NOT EXISTS idx_touches_recency ON file_touches(touched_at DESC);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    applied_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
