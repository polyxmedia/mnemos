package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// CuedMemory is a live memory together with the cues that trigger it.
type CuedMemory struct {
	Observation memory.Observation
	Cues        []string
}

// CueStore persists the action triggers attached to memories.
type CueStore struct{ db *sql.DB }

// Cues returns the cue store backed by this DB.
func (d *DB) Cues() *CueStore { return &CueStore{db: d.sql} }

// Add attaches cues to an observation. Cues already present are left alone.
// Callers validate cues with cue.Parse and pass the canonical form.
func (s *CueStore) Add(ctx context.Context, observationID string, cues []string, now time.Time) error {
	if len(cues) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("add cues: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range cues {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO observation_cues (observation_id, cue, created_at)
			VALUES (?, ?, ?)`, observationID, c, now.UTC()); err != nil {
			return fmt.Errorf("add cue %q to %s: %w", c, observationID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("add cues: commit: %w", err)
	}
	return nil
}

// Remove detaches one cue. Removing a cue that is not there is not an error.
func (s *CueStore) Remove(ctx context.Context, observationID, cue string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM observation_cues WHERE observation_id = ? AND cue = ?`, observationID, cue); err != nil {
		return fmt.Errorf("remove cue %q from %s: %w", cue, observationID, err)
	}
	return nil
}

// For returns the cues on one observation, oldest first.
func (s *CueStore) For(ctx context.Context, observationID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cue FROM observation_cues WHERE observation_id = ? ORDER BY created_at, cue`, observationID)
	if err != nil {
		return nil, fmt.Errorf("list cues for %s: %w", observationID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("scan cue: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Live returns every memory that has at least one cue and is live at now:
// not invalidated, not past valid_until or expiry, and not quarantined in
// the raw tier. Cues on dead memories stay stored but never fire.
func (s *CueStore) Live(ctx context.Context, now time.Time) ([]CuedMemory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixed("o", obsColumns)+`, c.cue
		  FROM observation_cues c
		  JOIN observations o ON o.id = c.observation_id
		 WHERE o.invalidated_at IS NULL
		   AND (o.valid_until IS NULL OR o.valid_until > ?)
		   AND (o.expires_at IS NULL OR o.expires_at > ?)
		   AND o.trust_tier != 'raw'
		 ORDER BY o.id, c.created_at, c.cue`, now.UTC(), now.UTC())
	if err != nil {
		return nil, fmt.Errorf("live cues: %w", err)
	}
	defer rows.Close()
	var out []CuedMemory
	for rows.Next() {
		o, c, err := scanObsWithCue(rows)
		if err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].Observation.ID == o.ID {
			out[n-1].Cues = append(out[n-1].Cues, c)
			continue
		}
		out = append(out, CuedMemory{Observation: *o, Cues: []string{c}})
	}
	return out, rows.Err()
}
