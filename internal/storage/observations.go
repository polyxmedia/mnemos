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

const obsColumns = `
	id, session_id, agent_id, project,
	title, content, obs_type, tags, importance,
	access_count, last_accessed_at,
	created_at, valid_from, valid_until,
	invalidated_at, expires_at,
	content_hash, structured, rationale,
	embedding, embedding_model, last_exported_at,
	source_kind, trust_tier, derived_from`

const selectObsSQL = `SELECT ` + obsColumns + ` FROM observations`

func (s *obsStore) Insert(ctx context.Context, o *memory.Observation) error {
	tags, err := json.Marshal(coalesceSliceStr(o.Tags))
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}
	// Provenance fields default at the service layer, but defend in depth
	// here so a store caller that bypasses the service (tests, migrations)
	// still ends up with CHECK-constraint-valid rows.
	sourceKind := o.SourceKind
	if sourceKind == "" {
		sourceKind = memory.SourceUser
	}
	trustTier := o.TrustTier
	if trustTier == "" {
		trustTier = memory.TrustCurated
	}
	derived, err := json.Marshal(coalesceSliceStr(o.DerivedFrom))
	if err != nil {
		return fmt.Errorf("marshal derived_from: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO observations (
			id, session_id, agent_id, project,
			title, content, obs_type, tags, importance,
			created_at, valid_from, valid_until, expires_at,
			content_hash, structured, rationale,
			embedding, embedding_model,
			source_kind, trust_tier, derived_from
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
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
		nullableStr(o.ContentHash),
		nullableStr(o.Structured),
		nullableStr(o.Rationale),
		encodeVector(o.Embedding),
		nullableStr(o.EmbeddingModel),
		string(sourceKind),
		string(trustTier),
		string(derived),
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
	if err := s.BumpAccess(ctx, id); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *obsStore) BumpAccess(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE observations SET access_count = access_count + 1,
		        last_accessed_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("bump access: %w", err)
	}
	return nil
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
	// Explicit Go-side timestamp preserves sub-second precision — important
	// for bi-temporal queries and replay filtering.
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE observations
		   SET valid_until    = COALESCE(valid_until, ?),
		       invalidated_at = ?
		 WHERE id = ? AND invalidated_at IS NULL`,
		validUntil.UTC(), now, id)
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
	if strings.TrimSpace(in.Query) == "" {
		return nil, fmt.Errorf("search: query is required")
	}

	args := []any{ftsEscape(in.Query)}
	var sb strings.Builder
	sb.WriteString(`SELECT ` + prefixed("o", obsColumns) + `,
	       bm25(observations_fts) AS score,
	       snippet(observations_fts, 1, '', '', '…', 16) AS snip
	  FROM observations_fts
	  JOIN observations o ON o.rowid = observations_fts.rowid
	 WHERE observations_fts MATCH ?`)

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
		// Bi-temporal: invalidated_at is system time (when WE recorded the
		// invalidation); valid_until is fact time. For a historical AsOf
		// query we want everything that was true at AsOf, even if it was
		// invalidated in our system afterwards.
		sb.WriteString(` AND (o.invalidated_at IS NULL OR o.invalidated_at > ?)`)
		args = append(args, asOf.UTC())
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
		o, score, snippet, err := scanObsWithScore(rows)
		if err != nil {
			return nil, err
		}
		// FTS5 bm25() returns a negative score by convention (lower = better).
		// Flip to positive "relevance".
		bm25 := -score
		results = append(results, memory.SearchResult{
			Observation: *o,
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

// ListLinks returns edges of the given link type where both endpoints are
// still live. Callers pass agentID to scope to their agent; empty means
// all agents. The source and target titles come back inline so monitors
// don't need a second round-trip per edge. Ordered by link created_at
// descending so the newest contradictions rise first.
func (s *obsStore) ListLinks(ctx context.Context, linkType memory.LinkType, agentID string, limit int) ([]memory.LinkEdge, error) {
	if limit <= 0 {
		limit = 1000
	}
	query := `
		SELECT l.source_id, src.title, src.agent_id, src.created_at,
		       l.target_id, tgt.title, tgt.agent_id, tgt.created_at,
		       l.created_at
		  FROM observation_links l
		  JOIN observations src ON src.id = l.source_id
		  JOIN observations tgt ON tgt.id = l.target_id
		 WHERE l.link_type = ?
		   AND src.invalidated_at IS NULL
		   AND tgt.invalidated_at IS NULL`
	args := []any{string(linkType)}
	if agentID != "" {
		query += ` AND tgt.agent_id = ?`
		args = append(args, agentID)
	}
	query += ` ORDER BY l.created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	defer rows.Close()

	var out []memory.LinkEdge
	for rows.Next() {
		var e memory.LinkEdge
		if err := rows.Scan(
			&e.SourceID, &e.SourceTitle, &e.SourceAgent, &e.SourceCreatedAt,
			&e.TargetID, &e.TargetTitle, &e.TargetAgent, &e.TargetCreatedAt,
			&e.LinkedAt,
		); err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		e.LinkType = linkType
		e.SourceCreatedAt = e.SourceCreatedAt.UTC()
		e.TargetCreatedAt = e.TargetCreatedAt.UTC()
		e.LinkedAt = e.LinkedAt.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixed("o", obsColumns)+`
		  FROM observations o
		  JOIN observations_fts fts ON fts.rowid = o.rowid
		 WHERE o.agent_id = ? AND observations_fts MATCH ?
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

func (s *obsStore) FindByContentHash(ctx context.Context, agentID, project, hash string) (*memory.Observation, error) {
	if hash == "" {
		return nil, nil
	}
	args := []any{nullableStr(agentID), hash}
	query := selectObsSQL + ` WHERE agent_id = COALESCE(?, agent_id) AND content_hash = ?`
	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	query += ` AND invalidated_at IS NULL LIMIT 1`
	row := s.db.QueryRowContext(ctx, query, args...)
	o, err := scanObs(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return o, nil
}

func (s *obsStore) ListBySession(ctx context.Context, sessionID string) ([]memory.Observation, error) {
	// Returns ALL observations for a session, including invalidated ones.
	// Replay needs this so it can flag which session-observations were
	// superseded after the fact.
	rows, err := s.db.QueryContext(ctx,
		selectObsSQL+` WHERE session_id = ? ORDER BY created_at ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list by session: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

func (s *obsStore) ListByProject(ctx context.Context, agentID, project string, obsType memory.ObsType, limit int) ([]memory.Observation, error) {
	if limit <= 0 {
		limit = 20
	}
	args := []any{}
	query := selectObsSQL + ` WHERE invalidated_at IS NULL
	                          AND (valid_until IS NULL OR valid_until > CURRENT_TIMESTAMP)`
	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	if agentID != "" {
		query += ` AND agent_id = ?`
		args = append(args, agentID)
	}
	if obsType != "" {
		query += ` AND obs_type = ?`
		args = append(args, string(obsType))
	}
	query += ` ORDER BY importance DESC, created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list by project: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

func (s *obsStore) UpdateEmbedding(ctx context.Context, id, model string, vec []float32) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE observations SET embedding = ?, embedding_model = ? WHERE id = ?`,
		encodeVector(vec), nullableStr(model), id)
	if err != nil {
		return fmt.Errorf("update embedding: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return memory.ErrNotFound
	}
	return nil
}

func (s *obsStore) ListMissingEmbeddings(ctx context.Context, limit int) ([]memory.Observation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		selectObsSQL+` WHERE embedding IS NULL AND invalidated_at IS NULL ORDER BY created_at DESC LIMIT ?`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("list missing embeddings: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

func (s *obsStore) MarkExported(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE observations SET last_exported_at = ? WHERE id = ?`, at.UTC(), id)
	if err != nil {
		return fmt.Errorf("mark exported: %w", err)
	}
	return nil
}

// --- scanners -----------------------------------------------------------

type obsScanDest struct {
	o                         *memory.Observation
	sessID, project           *sql.NullString
	contentHash, structured   *sql.NullString
	rationale, embeddingModel *sql.NullString
	lastAcc, validUntil       *sql.NullTime
	invalidatedAt, expiresAt  *sql.NullTime
	lastExportedAt            *sql.NullTime
	tagsJSON                  *string
	embedding                 *[]byte
	sourceKind, trustTier     *string
	derivedFromJSON           *string
}

func newObsScanDest(o *memory.Observation) obsScanDest {
	return obsScanDest{
		o:               o,
		sessID:          &sql.NullString{},
		project:         &sql.NullString{},
		contentHash:     &sql.NullString{},
		structured:      &sql.NullString{},
		rationale:       &sql.NullString{},
		embeddingModel:  &sql.NullString{},
		lastAcc:         &sql.NullTime{},
		validUntil:      &sql.NullTime{},
		invalidatedAt:   &sql.NullTime{},
		expiresAt:       &sql.NullTime{},
		lastExportedAt:  &sql.NullTime{},
		tagsJSON:        new(string),
		embedding:       new([]byte),
		sourceKind:      new(string),
		trustTier:       new(string),
		derivedFromJSON: new(string),
	}
}

func (d obsScanDest) args() []any {
	return []any{
		&d.o.ID, d.sessID, &d.o.AgentID, d.project,
		&d.o.Title, &d.o.Content, &d.o.Type, d.tagsJSON, &d.o.Importance,
		&d.o.AccessCount, d.lastAcc,
		&d.o.CreatedAt, &d.o.ValidFrom, d.validUntil,
		d.invalidatedAt, d.expiresAt,
		d.contentHash, d.structured, d.rationale,
		d.embedding, d.embeddingModel, d.lastExportedAt,
		d.sourceKind, d.trustTier, d.derivedFromJSON,
	}
}

func (d obsScanDest) finalise() {
	d.o.SessionID = d.sessID.String
	d.o.Project = d.project.String
	d.o.ContentHash = d.contentHash.String
	d.o.Structured = d.structured.String
	d.o.Rationale = d.rationale.String
	d.o.EmbeddingModel = d.embeddingModel.String
	d.o.LastAccessedAt = nullableTimePtr(*d.lastAcc)
	d.o.ValidUntil = nullableTimePtr(*d.validUntil)
	d.o.InvalidatedAt = nullableTimePtr(*d.invalidatedAt)
	d.o.ExpiresAt = nullableTimePtr(*d.expiresAt)
	d.o.LastExportedAt = nullableTimePtr(*d.lastExportedAt)
	d.o.Embedding = decodeVector(*d.embedding)
	_ = json.Unmarshal([]byte(*d.tagsJSON), &d.o.Tags)
	d.o.SourceKind = memory.SourceKind(*d.sourceKind)
	d.o.TrustTier = memory.TrustTier(*d.trustTier)
	_ = json.Unmarshal([]byte(*d.derivedFromJSON), &d.o.DerivedFrom)
}

func scanObs(row scanner) (*memory.Observation, error) {
	o := &memory.Observation{}
	d := newObsScanDest(o)
	if err := row.Scan(d.args()...); err != nil {
		return nil, err
	}
	d.finalise()
	return o, nil
}

func scanObsWithScore(row scanner) (*memory.Observation, float64, string, error) {
	o := &memory.Observation{}
	d := newObsScanDest(o)
	var score float64
	var snippet string
	args := append(d.args(), &score, &snippet)
	if err := row.Scan(args...); err != nil {
		return nil, 0, "", fmt.Errorf("scan: %w", err)
	}
	d.finalise()
	return o, score, snippet, nil
}

func scanObsList(rows *sql.Rows) ([]memory.Observation, error) {
	var out []memory.Observation
	for rows.Next() {
		o, err := scanObs(rows)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// --- helpers ------------------------------------------------------------

// prefixed returns the column list with a table alias prefixed to each name.
func prefixed(alias, columns string) string {
	var out []string
	for _, raw := range strings.Split(columns, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		out = append(out, alias+"."+name)
	}
	return strings.Join(out, ", ")
}

func coalesceSliceStr(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
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
