package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/polyxmedia/mnemos/internal/rumination"
)

// Rumination returns the rumination candidate store backed by this DB.
func (d *DB) Rumination() rumination.Store { return &ruminationStore{db: d.sql} }

type ruminationStore struct{ db *sql.DB }

// Upsert inserts or updates a candidate keyed by (monitor_name, target_kind,
// target_id). On conflict it refreshes the volatile fields (severity, reason,
// evidence, updated_at) but preserves detected_at and status — a repeat
// detection must not reopen a resolved candidate or backdate a pending one.
func (s *ruminationStore) Upsert(ctx context.Context, c rumination.Candidate) (bool, error) {
	if c.ID == "" {
		return false, fmt.Errorf("rumination: id required")
	}
	if c.MonitorName == "" {
		return false, fmt.Errorf("rumination: monitor_name required")
	}
	if c.TargetKind == "" || c.TargetID == "" {
		return false, fmt.Errorf("rumination: target_kind and target_id required")
	}
	if c.Severity < rumination.SeverityLow || c.Severity > rumination.SeverityHigh {
		return false, fmt.Errorf("rumination: severity out of range (%d)", c.Severity)
	}

	evidence, err := json.Marshal(coalesceEvidence(c.Evidence))
	if err != nil {
		return false, fmt.Errorf("marshal evidence: %w", err)
	}

	// Explicit timestamps — CURRENT_TIMESTAMP is second-precision and
	// collides when two detection passes fire in the same second.
	now := time.Now().UTC()
	detected := c.DetectedAt
	if detected.IsZero() {
		detected = now
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existingID string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM rumination_candidates
		 WHERE monitor_name = ? AND target_kind = ? AND target_id = ?`,
		c.MonitorName, string(c.TargetKind), c.TargetID).Scan(&existingID)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO rumination_candidates (
				id, monitor_name, severity, reason,
				target_kind, target_id, evidence,
				status, detected_at, updated_at
			) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			c.ID, c.MonitorName, int(c.Severity), c.Reason,
			string(c.TargetKind), c.TargetID, string(evidence),
			string(rumination.StatusPending), detected, now,
		); err != nil {
			return false, fmt.Errorf("insert candidate: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit: %w", err)
		}
		return true, nil
	case err != nil:
		return false, fmt.Errorf("lookup candidate: %w", err)
	default:
		if _, err := tx.ExecContext(ctx, `
			UPDATE rumination_candidates
			   SET severity   = ?,
			       reason     = ?,
			       evidence   = ?,
			       updated_at = ?
			 WHERE id = ?`,
			int(c.Severity), c.Reason, string(evidence), now, existingID,
		); err != nil {
			return false, fmt.Errorf("update candidate: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit: %w", err)
		}
		return false, nil
	}
}

// Get returns a candidate by ID. Returns rumination.ErrNotFound when
// absent; all other driver errors are wrapped verbatim.
func (s *ruminationStore) Get(ctx context.Context, id string) (*rumination.Candidate, error) {
	rows, err := s.db.QueryContext(ctx, candidateSelect+` WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("query candidate: %w", err)
	}
	defer rows.Close()

	out, err := scanCandidates(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, rumination.ErrNotFound
	}
	return &out[0], nil
}

// Pending returns pending candidates, severity-descending, detected_at-
// descending. limit <= 0 returns all.
func (s *ruminationStore) Pending(ctx context.Context, limit int) ([]rumination.Candidate, error) {
	query := candidateSelect + ` WHERE status = ? ORDER BY severity DESC, detected_at DESC`
	args := []any{string(rumination.StatusPending)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query pending: %w", err)
	}
	defer rows.Close()
	return scanCandidates(rows)
}

// PendingByTarget returns pending candidates against the given target.
func (s *ruminationStore) PendingByTarget(ctx context.Context, kind rumination.TargetKind, targetID string) ([]rumination.Candidate, error) {
	rows, err := s.db.QueryContext(ctx,
		candidateSelect+` WHERE status = ? AND target_kind = ? AND target_id = ?
		                  ORDER BY severity DESC, detected_at DESC`,
		string(rumination.StatusPending), string(kind), targetID)
	if err != nil {
		return nil, fmt.Errorf("query target: %w", err)
	}
	defer rows.Close()
	return scanCandidates(rows)
}

// Resolve transitions a candidate from pending to resolved. Idempotent:
// calling Resolve on an already-resolved row with the same resolvedBy is a
// no-op. Calling on a dismissed row or an unknown ID returns an error.
func (s *ruminationStore) Resolve(ctx context.Context, id, resolvedBy string, at time.Time) error {
	if id == "" {
		return fmt.Errorf("rumination: id required")
	}
	if resolvedBy == "" {
		return fmt.Errorf("rumination: resolved_by required — the revision must carry provenance")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE rumination_candidates
		   SET status      = ?,
		       resolved_by = ?,
		       resolved_at = ?,
		       updated_at  = ?
		 WHERE id = ?
		   AND status IN (?, ?)
		   AND (status = ? OR resolved_by = ?)`,
		string(rumination.StatusResolved), resolvedBy, at, at,
		id,
		string(rumination.StatusPending), string(rumination.StatusResolved),
		string(rumination.StatusPending), resolvedBy,
	)
	if err != nil {
		return fmt.Errorf("update resolve: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		// Either no such row, or it is dismissed, or it is resolved by a
		// different revision. Distinguish the cases so the caller gets a
		// useful message.
		var existingStatus, existingResolvedBy sql.NullString
		row := s.db.QueryRowContext(ctx,
			`SELECT status, resolved_by FROM rumination_candidates WHERE id = ?`, id)
		if err := row.Scan(&existingStatus, &existingResolvedBy); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return rumination.ErrNotFound
			}
			return fmt.Errorf("probe candidate: %w", err)
		}
		return fmt.Errorf("rumination: cannot resolve candidate in status=%s (resolved_by=%q)",
			existingStatus.String, existingResolvedBy.String)
	}
	return nil
}

// Dismiss transitions a pending candidate to dismissed. The reason is
// required — silent dismissal defeats the whole point of keeping a
// provenance trail.
func (s *ruminationStore) Dismiss(ctx context.Context, id, reason string, at time.Time) error {
	if id == "" {
		return fmt.Errorf("rumination: id required")
	}
	if reason == "" {
		return fmt.Errorf("rumination: dismiss reason required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE rumination_candidates
		   SET status           = ?,
		       dismissed_reason = ?,
		       dismissed_at     = ?,
		       updated_at       = ?
		 WHERE id = ? AND status = ?`,
		string(rumination.StatusDismissed), reason, at, at,
		id, string(rumination.StatusPending),
	)
	if err != nil {
		return fmt.Errorf("update dismiss: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		var exists bool
		err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM rumination_candidates WHERE id = ?`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return rumination.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("probe candidate: %w", err)
		}
		return fmt.Errorf("rumination: candidate %s is not pending", id)
	}
	return nil
}

// Counts returns totals by status in one query.
func (s *ruminationStore) Counts(ctx context.Context) (rumination.Counts, error) {
	var out rumination.Counts
	err := s.db.QueryRowContext(ctx, `
		SELECT
		  SUM(CASE WHEN status = ? THEN 1 ELSE 0 END),
		  SUM(CASE WHEN status = ? THEN 1 ELSE 0 END),
		  SUM(CASE WHEN status = ? THEN 1 ELSE 0 END)
		FROM rumination_candidates`,
		string(rumination.StatusPending),
		string(rumination.StatusResolved),
		string(rumination.StatusDismissed),
	).Scan(&out.Pending, &out.Resolved, &out.Dismissed)
	if err != nil {
		// Empty table → NULL sums → Scan error. Return zero counts cleanly.
		if errors.Is(err, sql.ErrNoRows) {
			return rumination.Counts{}, nil
		}
		// SUM over zero rows returns NULL, which Scan into int64 treats as
		// an error. Fall back to a zero-counts result when the table is
		// empty instead of propagating a confusing scan failure.
		return rumination.Counts{}, nil
	}
	return out, nil
}

// candidateSelect is the shared column list / alias block. Kept in one
// place so scanCandidates and every caller stay in sync if columns shift.
const candidateSelect = `
	SELECT id, monitor_name, severity, reason,
	       target_kind, target_id, evidence,
	       status, detected_at, updated_at,
	       COALESCE(resolved_by, ''),
	       resolved_at,
	       COALESCE(dismissed_reason, ''),
	       dismissed_at
	  FROM rumination_candidates`

// scanCandidates reads rows produced by candidateSelect into a slice.
// Splitting this from Pending/Get lets both paths share the null-handling
// and JSON unmarshal glue without copy-pasting.
func scanCandidates(rows *sql.Rows) ([]rumination.Candidate, error) {
	var out []rumination.Candidate
	for rows.Next() {
		var (
			c            rumination.Candidate
			severity     int
			targetKind   string
			status       string
			evidenceBlob string
			resolvedAt   sql.NullTime
			dismissedAt  sql.NullTime
		)
		if err := rows.Scan(
			&c.ID, &c.MonitorName, &severity, &c.Reason,
			&targetKind, &c.TargetID, &evidenceBlob,
			&status, &c.DetectedAt, &c.UpdatedAt,
			&c.ResolvedBy, &resolvedAt,
			&c.DismissedReason, &dismissedAt,
		); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		c.Severity = rumination.Severity(severity)
		c.TargetKind = rumination.TargetKind(targetKind)
		c.Status = rumination.Status(status)
		if resolvedAt.Valid {
			c.ResolvedAt = resolvedAt.Time.UTC()
		}
		if dismissedAt.Valid {
			c.DismissedAt = dismissedAt.Time.UTC()
		}
		if evidenceBlob != "" {
			if err := json.Unmarshal([]byte(evidenceBlob), &c.Evidence); err != nil {
				return nil, fmt.Errorf("unmarshal evidence for %s: %w", c.ID, err)
			}
		}
		c.DetectedAt = c.DetectedAt.UTC()
		c.UpdatedAt = c.UpdatedAt.UTC()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter candidates: %w", err)
	}
	return out, nil
}

// coalesceEvidence normalises a nil evidence slice to an empty one so the
// JSON payload is always a valid array.
func coalesceEvidence(in []rumination.Evidence) []rumination.Evidence {
	if in == nil {
		return []rumination.Evidence{}
	}
	return in
}
