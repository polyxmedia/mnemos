package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/injection"
)

// Injections returns the injection-event store backed by this DB.
func (d *DB) Injections() injection.Store { return &injStore{db: d.sql} }

type injStore struct{ db *sql.DB }

const injColumns = `id, kind, ref_id, channel, agent_id, project, session_id, created_at`

func (s *injStore) Record(ctx context.Context, events []injection.Event) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record injections: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO injections (`+injColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("record injections: prepare: %w", err)
	}
	defer stmt.Close()

	for _, e := range events {
		// Enum validation lives here, not in a schema CHECK (see migration
		// 0005): the write fails loudly on a typo'd kind/channel while new
		// values stay a one-line Go change.
		if !e.Kind.Valid() {
			return fmt.Errorf("record injection: invalid kind %q", e.Kind)
		}
		if !e.Channel.Valid() {
			return fmt.Errorf("record injection: invalid channel %q", e.Channel)
		}
		if _, err := stmt.ExecContext(ctx,
			e.ID,
			string(e.Kind),
			e.RefID,
			string(e.Channel),
			e.AgentID,
			nullableStr(e.Project),
			nullableStr(e.SessionID),
			e.CreatedAt.UTC(),
		); err != nil {
			return fmt.Errorf("record injection %s/%s: %w", e.Kind, e.RefID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("record injections: commit: %w", err)
	}
	return nil
}

func (s *injStore) ListByRef(ctx context.Context, kind injection.Kind, refID string, limit int) ([]injection.Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+injColumns+`
		  FROM injections
		 WHERE kind = ? AND ref_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		string(kind), refID, limit)
	if err != nil {
		return nil, fmt.Errorf("list injections: %w", err)
	}
	defer rows.Close()

	var out []injection.Event
	for rows.Next() {
		var e injection.Event
		var kindStr, channelStr string
		var project, sessionID sql.NullString
		if err := rows.Scan(
			&e.ID, &kindStr, &e.RefID, &channelStr,
			&e.AgentID, &project, &sessionID, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan injection: %w", err)
		}
		e.Kind = injection.Kind(kindStr)
		e.Channel = injection.Channel(channelStr)
		e.Project = project.String
		e.SessionID = sessionID.String
		out = append(out, e)
	}
	return out, rows.Err()
}
