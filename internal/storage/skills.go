package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// Skills returns the skill store backed by this DB.
func (d *DB) Skills() skills.Store { return &skillStore{db: d.sql} }

type skillStore struct{ db *sql.DB }

func (s *skillStore) Upsert(ctx context.Context, in skills.SaveInput) (*skills.Skill, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("skill: name required")
	}
	if in.Procedure == "" {
		return nil, fmt.Errorf("skill: procedure required")
	}
	agent := in.AgentID
	if agent == "" {
		agent = "default"
	}
	tags, err := json.Marshal(coalesceSlice(in.Tags))
	if err != nil {
		return nil, fmt.Errorf("marshal tags: %w", err)
	}
	sources, err := json.Marshal(coalesceSlice(in.SourceSessions))
	if err != nil {
		return nil, fmt.Errorf("marshal sources: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		existingID      string
		existingVersion int
	)
	err = tx.QueryRowContext(ctx,
		`SELECT id, version FROM skills WHERE agent_id = ? AND name = ?`,
		agent, in.Name).Scan(&existingID, &existingVersion)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		id := ulid.Make().String()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO skills (id, agent_id, name, description, procedure,
			                    pitfalls, tags, source_sessions, version)
			VALUES (?,?,?,?,?,?,?,?,1)`,
			id, agent, in.Name, in.Description, in.Procedure,
			nullableStr(in.Pitfalls), string(tags), string(sources)); err != nil {
			return nil, fmt.Errorf("insert skill: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return s.Get(ctx, id)
	case err != nil:
		return nil, fmt.Errorf("lookup skill: %w", err)
	default:
		if _, err := tx.ExecContext(ctx, `
			UPDATE skills SET
				description     = ?,
				procedure       = ?,
				pitfalls        = ?,
				tags            = ?,
				source_sessions = ?,
				version         = ?,
				updated_at      = CURRENT_TIMESTAMP
			WHERE id = ?`,
			in.Description, in.Procedure, nullableStr(in.Pitfalls),
			string(tags), string(sources), existingVersion+1, existingID); err != nil {
			return nil, fmt.Errorf("update skill: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return s.Get(ctx, existingID)
	}
}

func (s *skillStore) Get(ctx context.Context, id string) (*skills.Skill, error) {
	row := s.db.QueryRowContext(ctx, selectSkillSQL+` WHERE id = ?`, id)
	skill, err := scanSkill(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, skills.ErrNotFound
		}
		return nil, err
	}
	return skill, nil
}

func (s *skillStore) Match(ctx context.Context, in skills.MatchInput) ([]skills.Match, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	if in.Query == "" {
		return nil, fmt.Errorf("skill match: query required")
	}

	args := []any{ftsEscape(in.Query)}
	q := `
		SELECT s.id, s.agent_id, s.name, s.description, s.procedure,
		       s.pitfalls, s.tags, s.source_sessions,
		       s.use_count, s.success_count, s.effectiveness, s.version,
		       s.created_at, s.updated_at,
		       bm25(skills_fts) AS score
		  FROM skills_fts
		  JOIN skills s ON s.rowid = skills_fts.rowid
		 WHERE skills_fts MATCH ?`
	if in.AgentID != "" {
		q += ` AND s.agent_id = ?`
		args = append(args, in.AgentID)
	}
	q += ` ORDER BY score LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("match skills: %w", err)
	}
	defer rows.Close()

	var out []skills.Match
	for rows.Next() {
		var (
			skill skills.Skill
			score float64
		)
		var pitfalls sql.NullString
		var tagsJSON, sourcesJSON string
		if err := rows.Scan(
			&skill.ID, &skill.AgentID, &skill.Name, &skill.Description, &skill.Procedure,
			&pitfalls, &tagsJSON, &sourcesJSON,
			&skill.UseCount, &skill.SuccessCount, &skill.Effectiveness, &skill.Version,
			&skill.CreatedAt, &skill.UpdatedAt,
			&score,
		); err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		skill.Pitfalls = pitfalls.String
		_ = json.Unmarshal([]byte(tagsJSON), &skill.Tags)
		_ = json.Unmarshal([]byte(sourcesJSON), &skill.SourceSessions)
		// Effectiveness nudges score: higher effectiveness = better placement.
		weighted := (-score) * (0.5 + 0.5*skill.Effectiveness)
		out = append(out, skills.Match{Skill: skill, Score: weighted})
	}
	return out, rows.Err()
}

func (s *skillStore) List(ctx context.Context, agentID string) ([]skills.Skill, error) {
	args := []any{}
	q := selectSkillSQL
	if agentID != "" {
		q += ` WHERE agent_id = ?`
		args = append(args, agentID)
	}
	q += ` ORDER BY updated_at DESC`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	var out []skills.Skill
	for rows.Next() {
		skill, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *skill)
	}
	return out, rows.Err()
}

func (s *skillStore) RecordUse(ctx context.Context, in skills.FeedbackInput) error {
	delta := 0
	if in.Success {
		delta = 1
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE skills
		   SET use_count     = use_count + 1,
		       success_count = success_count + ?,
		       effectiveness = CAST(success_count + ? AS REAL) / (use_count + 1),
		       updated_at    = CURRENT_TIMESTAMP
		 WHERE id = ?`, delta, delta, in.ID)
	if err != nil {
		return fmt.Errorf("record use: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return skills.ErrNotFound
	}
	return nil
}

func (s *skillStore) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count skills: %w", err)
	}
	return n, nil
}

const selectSkillSQL = `
	SELECT id, agent_id, name, description, procedure,
	       pitfalls, tags, source_sessions,
	       use_count, success_count, effectiveness, version,
	       created_at, updated_at
	  FROM skills`

func scanSkill(row scanner) (*skills.Skill, error) {
	var skill skills.Skill
	var pitfalls sql.NullString
	var tagsJSON, sourcesJSON string
	var createdAt, updatedAt time.Time

	if err := row.Scan(
		&skill.ID, &skill.AgentID, &skill.Name, &skill.Description, &skill.Procedure,
		&pitfalls, &tagsJSON, &sourcesJSON,
		&skill.UseCount, &skill.SuccessCount, &skill.Effectiveness, &skill.Version,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	skill.Pitfalls = pitfalls.String
	_ = json.Unmarshal([]byte(tagsJSON), &skill.Tags)
	_ = json.Unmarshal([]byte(sourcesJSON), &skill.SourceSessions)
	skill.CreatedAt = createdAt
	skill.UpdatedAt = updatedAt
	return &skill, nil
}

func coalesceSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
