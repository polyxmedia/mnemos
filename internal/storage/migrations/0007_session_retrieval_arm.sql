-- 0007_session_retrieval_arm.sql: record how a session receives memory.
--
-- Every health check so far could only correlate memory with outcomes,
-- because every session got the same treatment. Storing the arm a session
-- was assigned to lets `mnemos experiment` compare randomly assigned groups.
--
-- Nullable: NULL means the session was opened outside any assignment, which
-- includes every session before this migration. Those behave as push.

ALTER TABLE sessions ADD COLUMN retrieval_arm TEXT;

CREATE INDEX idx_sessions_arm ON sessions(retrieval_arm, started_at) WHERE retrieval_arm IS NOT NULL;
