-- Mnemos schema v2: rumination candidates.
-- Rumination is the destructive counterpart to the dream pass: stored
-- knowledge whose effectiveness has dropped below a threshold gets
-- packaged with adversarial prompts so the agent can propose a revision.
--
-- Candidates are a job queue, not memory. They have queue semantics
-- (pending → resolved | dismissed), a stable dedup key per (monitor,
-- target), and deliberately do not participate in bi-temporal validity
-- or FTS indexing. Keeping them in a dedicated table prevents the
-- conflation of domain facts (observations) with infrastructure events
-- (rumination queue rows).

CREATE TABLE IF NOT EXISTS rumination_candidates (
    id                TEXT PRIMARY KEY,                                                      -- "rumination-<sha256 prefix>"
    monitor_name      TEXT NOT NULL,                                                         -- e.g. "skill-effectiveness-floor"
    severity          INTEGER NOT NULL CHECK (severity BETWEEN 1 AND 3),                     -- 1=low 2=medium 3=high
    reason            TEXT NOT NULL,                                                         -- one-line human summary
    target_kind       TEXT NOT NULL CHECK (target_kind IN ('skill','observation')),
    target_id         TEXT NOT NULL,
    evidence          TEXT NOT NULL DEFAULT '[]',                                            -- JSON array of {label,content,source}
    status            TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','resolved','dismissed')),
    resolved_by       TEXT,                                                                  -- skill or observation id carrying the revision
    resolved_at       DATETIME,
    dismissed_reason  TEXT,
    dismissed_at      DATETIME,
    detected_at       DATETIME NOT NULL,                                                     -- explicit Go UTC; never CURRENT_TIMESTAMP here
    updated_at        DATETIME NOT NULL                                                      -- mirrors detected_at on insert, bumps on state change
);

-- Upsert dedup: repeat detection of the same (monitor, target) pair must
-- update the existing row, not create a duplicate. Composite unique index
-- is cheaper than a surrogate key plus a constraint.
CREATE UNIQUE INDEX IF NOT EXISTS idx_rumination_dedup
  ON rumination_candidates(monitor_name, target_kind, target_id);

-- Pending-pull path: list oldest-severest first. Matches the Service's
-- Detect ordering so the queue and live detection produce identical
-- results without re-sorting client-side.
CREATE INDEX IF NOT EXISTS idx_rumination_pending
  ON rumination_candidates(status, severity DESC, detected_at DESC);

-- Target lookup: "is there an open rumination against skill X?" comes up
-- during prewarm composition so we can surface a warning badge.
CREATE INDEX IF NOT EXISTS idx_rumination_target
  ON rumination_candidates(target_kind, target_id);
