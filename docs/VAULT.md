# Obsidian vault

Mnemos can export its entire memory store as an Obsidian-compatible vault:
one markdown file per observation / session / skill, YAML frontmatter for
metadata, wikilinks for relationships, tag MOCs (maps of content), and a
dashboard index. The result is a browsable, human-readable view of
everything your agent knows — a genuine second brain, not a debug dump.

## Directory layout

```
~/.mnemos/vault/
├── observations/
│   ├── 2026-04-16-fixed-n1-query-abc123.md
│   └── 2026-04-15-auth-middleware-def456.md
├── sessions/
│   ├── 2026-04-16-wallet-api-refactor.md
│   └── 2026-04-15-spanner-migration.md
├── skills/
│   ├── go-error-handling-patterns.md
│   └── spanner-transaction-retry.md
├── tags/
│   ├── bugfix.md      # MOC linking all observations with this tag
│   └── architecture.md
└── _index.md          # dashboard: recent sessions, skills, counts
```

## Observation file

```markdown
---
accessed: 3
created: 2026-04-16T14:32:00Z
id: 01HXYZ1234567890
importance: 8
project: wallet-api
session: 01HXYZA
tags: [database, performance]
type: bugfix
---

# Fixed N+1 query in user list

**Why:** GORM eager loading wasn't configured on the roles association.

The user listing endpoint was firing a separate query per user to load
roles. Added a test that asserts max 2 queries for the endpoint.

## Session
- [[sessions/01HXYZA]]

## Tags
- [[tags/database]]
- [[tags/performance]]
```

- `**Why:**` renders when the observation has a `rationale` field (decisions,
  conventions, architecture entries typically do).
- `[[sessions/ID]]` links back to the originating session.
- `[[tags/X]]` links to the tag MOC page.
- Observation links (`related`, `supersedes`, etc.) become wikilinks in the
  body when present (planned extension).

## Export modes

| Command | Behaviour |
| --- | --- |
| `mnemos vault export` | One-shot full export. Overwrites existing files. |
| `mnemos vault watch` | Daemon. Syncs on an interval (default 5m). |
| `mnemos vault status` | Prints vault path + whether it has been exported. |

`vault watch` uses the `last_exported_at` column on observations to drive
incremental sync once implemented fully; the current implementation writes
the whole vault on each tick (cheap for realistic sizes).

## Configuration

```toml
[vault]
enabled        = true
path           = "~/.mnemos/vault"
watch_interval = "5m"        # Go duration: 30s, 5m, 1h, ...
```

## Filenames

- Observations: `{yyyy-mm-dd}-{slug}-{ulid-tail-6}.md`. The ULID tail
  prevents filename collisions when two observations share a date + title.
- Sessions: `{yyyy-mm-dd}-{slug-of-goal-or-id}.md`.
- Skills: `{slug-of-name}.md`.

## What does NOT get exported

- Invalidated observations (unless you pass `--include-stale`, planned)
- Superseded observations (same reason)
- Expired observations already pruned

The vault reflects your current live memory, not the historical record.
Use `mnemos export` (JSON) for a full historical dump.

## Promptware hygiene

The vault is browsable by humans. If an agent has saved content containing
prompt-injection patterns (see `docs/MCP_TOOLS.md` safety section), the
content goes into the vault as-is — sanitisation happens at the MCP
injection boundary, not at disk-write time. Don't wire your agent to
re-ingest the vault without running it through `internal/safety` first.
