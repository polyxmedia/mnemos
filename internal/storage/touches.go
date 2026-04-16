package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// Touches returns the file-heat-map store backed by this DB.
func (d *DB) Touches() memory.TouchStore { return &touchStore{db: d.sql} }

type touchStore struct{ db *sql.DB }

func (s *touchStore) Record(ctx context.Context, in memory.TouchInput) error {
	if in.Path == "" {
		return fmt.Errorf("touch: path required")
	}
	agent := in.AgentID
	if agent == "" {
		agent = "default"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO file_touches (
			project, agent_id, path, session_id, note, touched_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		in.Project, agent, in.Path,
		nullableStr(in.SessionID), nullableStr(in.Note), time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("record touch: %w", err)
	}
	return nil
}

func (s *touchStore) Hot(ctx context.Context, agentID, project string, limit int) ([]memory.HotFile, error) {
	if limit <= 0 {
		limit = 10
	}
	args := []any{}
	query := `
		SELECT project, agent_id, path, COUNT(*) AS touches, MAX(touched_at) AS last_touched
		  FROM file_touches`
	where := []string{}
	if agentID != "" {
		where = append(where, `agent_id = ?`)
		args = append(args, agentID)
	}
	if project != "" {
		where = append(where, `project = ?`)
		args = append(args, project)
	}
	if len(where) > 0 {
		query += ` WHERE ` + joinAnd(where)
	}
	query += `
		 GROUP BY project, agent_id, path
		 ORDER BY touches DESC, last_touched DESC
		 LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("hot files: %w", err)
	}
	defer rows.Close()

	var out []memory.HotFile
	for rows.Next() {
		var hf memory.HotFile
		if err := rows.Scan(&hf.Project, &hf.AgentID, &hf.Path, &hf.TouchCount, &hf.LastTouched); err != nil {
			return nil, fmt.Errorf("scan hot: %w", err)
		}
		out = append(out, hf)
	}
	return out, rows.Err()
}

func joinAnd(parts []string) string {
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += " AND "
		}
		s += p
	}
	return s
}
