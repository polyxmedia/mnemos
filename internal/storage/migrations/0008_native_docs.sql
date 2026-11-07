-- 0008_native_docs.sql: a read-only search index over Claude Code's auto
-- memory notes.
--
-- Auto memory lives per repo, so a note learned in one repo cannot be found
-- from another. These rows are a rebuildable copy of files mnemos never
-- writes to. They sit apart from observations on purpose: ranking, decay,
-- dream, export and the vault must not treat another tool's files as
-- mnemos memories.
--
-- root is the Claude config directory the note came from. Search filters on
-- it so separate accounts on one machine stay separate.

CREATE TABLE native_docs (
    id          TEXT PRIMARY KEY,
    path        TEXT NOT NULL UNIQUE,
    root        TEXT NOT NULL,
    project     TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    doc_type    TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL,
    size        INTEGER NOT NULL,
    modified_at DATETIME NOT NULL,
    indexed_at  DATETIME NOT NULL
);

CREATE INDEX idx_native_docs_root ON native_docs(root);

CREATE VIRTUAL TABLE native_docs_fts USING fts5(
    name, description, body,
    content='native_docs',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

CREATE TRIGGER native_docs_ai AFTER INSERT ON native_docs BEGIN
    INSERT INTO native_docs_fts(rowid, name, description, body)
    VALUES (new.rowid, new.name, new.description, new.body);
END;

CREATE TRIGGER native_docs_ad AFTER DELETE ON native_docs BEGIN
    INSERT INTO native_docs_fts(native_docs_fts, rowid, name, description, body)
    VALUES ('delete', old.rowid, old.name, old.description, old.body);
END;

CREATE TRIGGER native_docs_au AFTER UPDATE ON native_docs BEGIN
    INSERT INTO native_docs_fts(native_docs_fts, rowid, name, description, body)
    VALUES ('delete', old.rowid, old.name, old.description, old.body);
    INSERT INTO native_docs_fts(rowid, name, description, body)
    VALUES (new.rowid, new.name, new.description, new.body);
END;
