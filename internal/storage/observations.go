package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// Observations returns the observation store backed by this DB.
func (d *DB) Observations() memory.Store { return &obsStore{db: d.sql} }

type obsStore struct{ db *sql.DB }

func (s *obsStore) Insert(ctx context.Context, o *memory.Observation) error {
	tags, err := json.Marshal(o.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO observations (
			id, session_id, agent_id, project,
			title, content, obs_type, tags, importance,
			created_at, valid_from, valid_until, expires_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID,
		nullableStr(o.SessionID),
		o.AgentID,
		nullableStr(o.Project),
		o.Title,
		o.Content,
		string(o.Type),
		string(tags),
		o.Importance,
		o.CreatedAt.UTC(),
		o.ValidFrom.UTC(),
		nullableTime(o.ValidUntil),
		nullableTime(o.ExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}
	return nil
}

func (s *obsStore) Get(ctx context.Context, id string) (*memory.Observation, error) {
	row := s.db.QueryRowContext(ctx, selectObsSQL+` WHERE id = ?`, id)
	o, err := scanObs(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, memory.ErrNotFound
		}
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE observations SET access_count = access_count + 1,
		        last_accessed_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
		return nil, fmt.Errorf("bump access: %w", err)
	}
	return o, nil
}

func (s *obsStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM observations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return memory.ErrNotFound
	}
	return nil
}

func (s *obsStore) Invalidate(ctx context.Context, id string, validUntil time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE observations
		   SET valid_until    = COALESCE(valid_until, ?),
		       invalidated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND invalidated_at IS NULL`,
		validUntil.UTC(), id)
	if err != nil {
		return fmt.Errorf("invalidate: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return memory.ErrNotFound
	}
	return nil
}

func (s *obsStore) Search(ctx context.Context, in memory.SearchInput) ([]memory.SearchResult, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	asOf := in.AsOf
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}

	args := []any{}
	var sb strings.Builder
	sb.WriteString(`
		SELECT o.id, o.session_id, o.agent_id, o.project,
		       o.title, o.content, o.obs_type, o.tags, o.importance,
		       o.access_count, o.last_accessed_at,
		       o.created_at, o.valid_from, o.valid_until,
		       o.invalidated_at, o.expires_at,
		       bm25(observations_fts) AS score,
		       snippet(observations_fts, 1, '', '', '…', 16) AS snip
		  FROM observations_fts
		  JOIN observations o ON o.rowid = observations_fts.rowid
		 WHERE `)

	// FTS5 match is required for BM25 to work.
	if strings.TrimSpace(in.Query) == "" {
		return nil, fmt.Errorf("search: query is required")
	}
	sb.WriteString(`observations_fts MATCH ?`)
	args = append(args, ftsEscape(in.Query))

	if in.AgentID != "" {
		sb.WriteString(` AND o.agent_id = ?`)
		args = append(args, in.AgentID)
	}
	if in.Project != "" {
		sb.WriteString(` AND o.project = ?`)
		args = append(args, in.Project)
	}
	if in.Type != "" {
		sb.WriteString(` AND o.obs_type = ?`)
		args = append(args, string(in.Type))
	}
	if in.MinImportance > 0 {
		sb.WriteString(` AND o.importance >= ?`)
		args = append(args, in.MinImportance)
	}
	if !in.IncludeStale {
		sb.WriteString(` AND o.invalidated_at IS NULL`)
		sb.WriteString(` AND (o.valid_until IS NULL OR o.valid_until > ?)`)
		args = append(args, asOf.UTC())
		sb.WriteString(` AND (o.expires_at  IS NULL OR o.expires_at  > ?)`)
		args = append(args, asOf.UTC())
	}
	for _, tag := range in.Tags {
		sb.WriteString(` AND o.tags LIKE ?`)
		args = append(args, "%"+jsonQuote(tag)+"%")
	}

	sb.WriteString(` ORDER BY score LIMIT ?`)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	results := make([]memory.SearchResult, 0, limit)
	for rows.Next() {
		var (
			o       memory.Observation
			score   float64
			snippet string
		)
		var sessID, project sql.NullString
		var lastAcc, validUntil, invalidatedAt, expiresAt sql.NullTime
		var tagsJSON string

		if err := rows.Scan(
			&o.ID, &sessID, &o.AgentID, &project,
			&o.Title, &o.Content, &o.Type, &tagsJSON, &o.Importance,
			&o.AccessCount, &lastAcc,
			&o.CreatedAt, &o.ValidFrom, &validUntil,
			&invalidatedAt, &expiresAt,
			&score, &snippet,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		o.SessionID = sessID.String
		o.Project = project.String
		o.LastAccessedAt = nullableTimePtr(lastAcc)
		o.ValidUntil = nullableTimePtr(validUntil)
		o.InvalidatedAt = nullableTimePtr(invalidatedAt)
		o.ExpiresAt = nullableTimePtr(expiresAt)
		_ = json.Unmarshal([]byte(tagsJSON), &o.Tags)

		// SQLite FTS5 bm25() returns a negative value by convention
		// (lower = better). Convert to a positive "relevance" score.
		bm25 := -score

		results = append(results, memory.SearchResult{
			Observation: o,
			Score:       bm25,
			BM25:        bm25,
			Snippet:     snippet,
		})
	}
	return results, rows.Err()
}

func (s *obsStore) Link(ctx context.Context, sourceID, targetID string, linkType memory.LinkType) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO observation_links(source_id, target_id, link_type)
		VALUES (?, ?, ?)`, sourceID, targetID, string(linkType))
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	return nil
}

func (s *obsStore) Prune(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM observations WHERE expires_at IS NOT NULL AND expires_at <= ?`,
		now.UTC())
	if err != nil {
		return 0, fmt.Errorf("prune: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *obsStore) Stats(ctx context.Context) (memory.Stats, error) {
	var st memory.Stats

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations`).
		Scan(&st.Observations); err != nil {
		return st, fmt.Errorf("count observations: %w", err)
	}

	now := time.Now().UTC()
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM observations
		 WHERE invalidated_at IS NULL
		   AND (valid_until IS NULL OR valid_until > ?)
		   AND (expires_at  IS NULL OR expires_at  > ?)`, now, now).
		Scan(&st.LiveObservations); err != nil {
		return st, fmt.Errorf("count live: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).
		Scan(&st.Sessions); err != nil {
		return st, fmt.Errorf("count sessions: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills`).
		Scan(&st.Skills); err != nil {
		return st, fmt.Errorf("count skills: %w", err)
	}

	st.TopTags = collectTopTags(ctx, s.db)
	st.RecentSessions = collectRecentSessions(ctx, s.db, 5)
	return st, nil
}

func (s *obsStore) ListByTitleSimilarity(ctx context.Context, agentID, title string, limit int) ([]memory.Observation, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, selectObsSQL+`
		  JOIN observations_fts fts ON fts.rowid = observations.rowid
		 WHERE agent_id = ? AND observations_fts MATCH ?
		 ORDER BY bm25(observations_fts) LIMIT ?`,
		agentID, ftsEscape(title), limit)
	if err != nil {
		return nil, fmt.Errorf("similarity search: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

func (s *obsStore) DecayImportance(ctx context.Context, staleDays int, amount int) (int64, error) {
	if staleDays <= 0 || amount <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -staleDays)
	res, err := s.db.ExecContext(ctx, `
		UPDATE observations
		   SET importance = MAX(1, importance - ?)
		 WHERE importance > 1
		   AND COALESCE(last_accessed_at, created_at) < ?`,
		amount, cutoff)
	if err != nil {
		return 0, fmt.Errorf("decay: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

const selectObsSQL = `
	SELECT id, session_id, agent_id, project,
	       title, content, obs_type, tags, importance,
	       access_count, last_accessed_at,
	       created_at, valid_from, valid_until,
	       invalidated_at, expires_at
	  FROM observations`

func scanObs(row interface {
	Scan(dest ...any) error
}) (*memory.Observation, error) {
	var o memory.Observation
	var sessID, project sql.NullString
	var lastAcc, validUntil, invalidatedAt, expiresAt sql.NullTime
	var tagsJSON string

	if err := row.Scan(
		&o.ID, &sessID, &o.AgentID, &project,
		&o.Title, &o.Content, &o.Type, &tagsJSON, &o.Importance,
		&o.AccessCount, &lastAcc,
		&o.CreatedAt, &o.ValidFrom, &validUntil,
		&invalidatedAt, &expiresAt,
	); err != nil {
		return nil, err
	}
	o.SessionID = sessID.String
	o.Project = project.String
	o.LastAccessedAt = nullableTimePtr(lastAcc)
	o.ValidUntil = nullableTimePtr(validUntil)
	o.InvalidatedAt = nullableTimePtr(invalidatedAt)
	o.ExpiresAt = nullableTimePtr(expiresAt)
	_ = json.Unmarshal([]byte(tagsJSON), &o.Tags)
	return &o, nil
}

func scanObsList(rows *sql.Rows) ([]memory.Observation, error) {
	var out []memory.Observation
	for rows.Next() {
		var o memory.Observation
		var sessID, project sql.NullString
		var lastAcc, validUntil, invalidatedAt, expiresAt sql.NullTime
		var tagsJSON string
		if err := rows.Scan(
			&o.ID, &sessID, &o.AgentID, &project,
			&o.Title, &o.Content, &o.Type, &tagsJSON, &o.Importance,
			&o.AccessCount, &lastAcc,
			&o.CreatedAt, &o.ValidFrom, &validUntil,
			&invalidatedAt, &expiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		o.SessionID = sessID.String
		o.Project = project.String
		o.LastAccessedAt = nullableTimePtr(lastAcc)
		o.ValidUntil = nullableTimePtr(validUntil)
		o.InvalidatedAt = nullableTimePtr(invalidatedAt)
		o.ExpiresAt = nullableTimePtr(expiresAt)
		_ = json.Unmarshal([]byte(tagsJSON), &o.Tags)
		out = append(out, o)
	}
	return out, rows.Err()
}

func collectTopTags(ctx context.Context, db *sql.DB) []memory.TagCount {
	rows, err := db.QueryContext(ctx, `SELECT tags FROM observations WHERE tags <> '[]'`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	counts := map[string]int64{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var tags []string
		if err := json.Unmarshal([]byte(raw), &tags); err != nil {
			continue
		}
		for _, t := range tags {
			counts[t]++
		}
	}

	out := make([]memory.TagCount, 0, len(counts))
	for tag, c := range counts {
		out = append(out, memory.TagCount{Tag: tag, Count: c})
	}
	// simple top-10 selection
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Count > out[i].Count {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func collectRecentSessions(ctx context.Context, db *sql.DB, limit int) []memory.RecentSession {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project, goal, started_at, ended_at
		   FROM sessions ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []memory.RecentSession
	for rows.Next() {
		var rs memory.RecentSession
		var project, goal sql.NullString
		var endedAt sql.NullTime
		if err := rows.Scan(&rs.ID, &project, &goal, &rs.StartedAt, &endedAt); err != nil {
			continue
		}
		rs.Project = project.String
		rs.Goal = goal.String
		rs.EndedAt = nullableTimePtr(endedAt)
		out = append(out, rs)
	}
	return out
}
