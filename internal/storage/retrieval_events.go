package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// Retrieval event channels. Each names the hook decision that wrote the row.
const (
	// EventPromptHook: the UserPromptSubmit search. Written on both arms, so
	// index sessions record what push would have shown without showing it.
	EventPromptHook = "prompt_hook"
	// EventPreTool: the path search before a file edit.
	EventPreTool = "pre_tool"
	// EventCue: a memory fired by a cmd: or path: cue.
	EventCue = "cue"
	// EventPrewarm: the SessionStart block, index included on the index arm.
	EventPrewarm = "prewarm"
	// EventRecovery: the block after a compaction.
	EventRecovery = "recovery"
)

// maxEventQuery bounds the stored query text. It is enough to judge what a
// prompt asked for, and keeps pasted logs out of the store.
const maxEventQuery = 600

// ScoredRef is one candidate a hook considered, with the score it saw.
type ScoredRef struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// RetrievalEvent is one delivery decision.
type RetrievalEvent struct {
	ID            string
	CreatedAt     time.Time
	Channel       string
	HostSessionID string
	SessionID     string
	Project       string
	Arm           string
	Query         string
	Fired         bool
	TopScore      float64
	Gap           float64
	Candidates    []ScoredRef
	Injected      []string
	Chars         int
}

// RetrievalEventStore persists delivery decisions.
type RetrievalEventStore struct{ db *sql.DB }

// RetrievalEvents returns the event store backed by this DB.
func (d *DB) RetrievalEvents() *RetrievalEventStore { return &RetrievalEventStore{db: d.sql} }

// Record writes one event, stamping an ID when it has none.
func (s *RetrievalEventStore) Record(ctx context.Context, e RetrievalEvent) error {
	if e.ID == "" {
		e.ID = ulid.Make().String()
	}
	if e.CreatedAt.IsZero() {
		return fmt.Errorf("record retrieval event: created_at is required")
	}
	if len(e.Query) > maxEventQuery {
		e.Query = e.Query[:maxEventQuery]
	}
	cands, err := json.Marshal(e.Candidates)
	if err != nil {
		return fmt.Errorf("record retrieval event: candidates: %w", err)
	}
	injected, err := json.Marshal(e.Injected)
	if err != nil {
		return fmt.Errorf("record retrieval event: injected: %w", err)
	}
	fired := 0
	if e.Fired {
		fired = 1
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO retrieval_events (id, created_at, channel, host_session_id, session_id, project, arm,
		                              query, fired, top_score, gap, candidates, injected, chars)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.CreatedAt.UTC(), e.Channel, nullableStr(e.HostSessionID), nullableStr(e.SessionID),
		nullableStr(e.Project), nullableStr(e.Arm), e.Query, fired, e.TopScore, e.Gap,
		string(cands), string(injected), e.Chars); err != nil {
		return fmt.Errorf("record retrieval event: %w", err)
	}
	return nil
}

// Since returns events at or after the cutoff, oldest first.
func (s *RetrievalEventStore) Since(ctx context.Context, since time.Time) ([]RetrievalEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, created_at, channel, COALESCE(host_session_id, ''), COALESCE(session_id, ''),
		       COALESCE(project, ''), COALESCE(arm, ''), COALESCE(query, ''), fired,
		       COALESCE(top_score, 0), COALESCE(gap, 0), COALESCE(candidates, 'null'),
		       COALESCE(injected, 'null'), chars
		  FROM retrieval_events
		 WHERE created_at >= ?
		 ORDER BY created_at`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("list retrieval events: %w", err)
	}
	defer rows.Close()
	var out []RetrievalEvent
	for rows.Next() {
		var (
			e               RetrievalEvent
			fired           int
			cands, injected string
		)
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.Channel, &e.HostSessionID, &e.SessionID, &e.Project,
			&e.Arm, &e.Query, &fired, &e.TopScore, &e.Gap, &cands, &injected, &e.Chars); err != nil {
			return nil, fmt.Errorf("scan retrieval event: %w", err)
		}
		e.Fired = fired == 1
		_ = json.Unmarshal([]byte(cands), &e.Candidates)
		_ = json.Unmarshal([]byte(injected), &e.Injected)
		out = append(out, e)
	}
	return out, rows.Err()
}
