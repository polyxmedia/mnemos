package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/session"
)

// Sessions returns the session store backed by this DB.
func (d *DB) Sessions() session.Store { return &sessStore{db: d.sql} }

type sessStore struct{ db *sql.DB }

func (s *sessStore) Insert(ctx context.Context, sess *session.Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, agent_id, project, goal, started_at)
		VALUES (?, ?, ?, ?, ?)`,
		sess.ID,
		sess.AgentID,
		nullableStr(sess.Project),
		nullableStr(sess.Goal),
		sess.StartedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *sessStore) Get(ctx context.Context, id string) (*session.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, agent_id, project, goal, summary, reflection, started_at, ended_at
		  FROM sessions WHERE id = ?`, id)
	return scanSession(row)
}

func (s *sessStore) Close(ctx context.Context, in session.CloseInput) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		   SET summary    = ?,
		       reflection = ?,
		       ended_at   = CURRENT_TIMESTAMP
		 WHERE id = ? AND ended_at IS NULL`,
		nullableStr(in.Summary), nullableStr(in.Reflection), in.ID)
	if err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return session.ErrNotFound
	}
	return nil
}

func (s *sessStore) Recent(ctx context.Context, agentID string, limit int) ([]session.Session, error) {
	if limit <= 0 {
		limit = 10
	}
	args := []any{limit}
	query := `
		SELECT id, agent_id, project, goal, summary, reflection, started_at, ended_at
		  FROM sessions`
	if agentID != "" {
		query += ` WHERE agent_id = ?`
		args = []any{agentID, limit}
	}
	query += ` ORDER BY started_at DESC LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("recent: %w", err)
	}
	defer rows.Close()

	var out []session.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

func (s *sessStore) Current(ctx context.Context, agentID string) (*session.Session, error) {
	args := []any{}
	query := `
		SELECT id, agent_id, project, goal, summary, reflection, started_at, ended_at
		  FROM sessions WHERE ended_at IS NULL`
	if agentID != "" {
		query += ` AND agent_id = ?`
		args = append(args, agentID)
	}
	query += ` ORDER BY started_at DESC LIMIT 1`
	row := s.db.QueryRowContext(ctx, query, args...)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, session.ErrNotFound
		}
		return nil, err
	}
	return sess, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(row scanner) (*session.Session, error) {
	var sess session.Session
	var project, goal, summary, reflection sql.NullString
	var endedAt sql.NullTime
	if err := row.Scan(
		&sess.ID, &sess.AgentID, &project, &goal, &summary, &reflection,
		&sess.StartedAt, &endedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, session.ErrNotFound
		}
		return nil, fmt.Errorf("scan session: %w", err)
	}
	sess.Project = project.String
	sess.Goal = goal.String
	sess.Summary = summary.String
	sess.Reflection = reflection.String
	sess.EndedAt = nullableTimePtr(endedAt)
	return &sess, nil
}
