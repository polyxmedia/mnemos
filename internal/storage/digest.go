package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DigestStats aggregates store activity since a cutoff. This is the data
// behind `mnemos digest` — the "numbers on the screen" view of whether the
// memory store is earning its keep. Aggregation crosses every domain
// (observations, sessions, skills, injections), so it lives on DB directly
// rather than behind one domain's store interface.
type DigestStats struct {
	Since time.Time

	ObservationsSaved  int64
	ObservationsByType []KindCount

	SessionsOpened   int64
	SessionsClosed   int64
	SessionsByStatus []KindCount

	// SkillsTouched counts skills created or version-bumped in the window.
	SkillsTouched int64

	InjectionsTotal     int64
	InjectionsByChannel []KindCount
	// TopSurfaced lists the most-injected memories in the window, with
	// titles resolved so the digest reads as prose, not IDs.
	TopSurfaced []SurfacedCount

	DreamPasses int64
}

// KindCount is one (label, count) aggregation row.
type KindCount struct {
	Kind  string
	Count int64
}

// SurfacedCount is one most-surfaced memory with its resolved title.
type SurfacedCount struct {
	Kind  string
	RefID string
	Title string
	Count int64
}

// Digest computes activity aggregates since the cutoff.
func (d *DB) Digest(ctx context.Context, since time.Time) (*DigestStats, error) {
	since = since.UTC()
	out := &DigestStats{Since: since}

	byType, total, err := d.groupCount(ctx,
		`SELECT obs_type, COUNT(*) FROM observations WHERE created_at >= ? GROUP BY obs_type ORDER BY COUNT(*) DESC, obs_type`, since)
	if err != nil {
		return nil, fmt.Errorf("digest observations: %w", err)
	}
	out.ObservationsByType = byType
	out.ObservationsSaved = total
	for _, kc := range byType {
		if kc.Kind == "dream" {
			out.DreamPasses = kc.Count
		}
	}

	if err := d.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE started_at >= ?`, since).Scan(&out.SessionsOpened); err != nil {
		return nil, fmt.Errorf("digest sessions opened: %w", err)
	}
	byStatus, closed, err := d.groupCount(ctx,
		`SELECT status, COUNT(*) FROM sessions WHERE ended_at >= ? GROUP BY status ORDER BY COUNT(*) DESC, status`, since)
	if err != nil {
		return nil, fmt.Errorf("digest sessions closed: %w", err)
	}
	out.SessionsByStatus = byStatus
	out.SessionsClosed = closed

	if err := d.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM skills WHERE updated_at >= ?`, since).Scan(&out.SkillsTouched); err != nil {
		return nil, fmt.Errorf("digest skills: %w", err)
	}

	byChannel, injTotal, err := d.groupCount(ctx,
		`SELECT channel, COUNT(*) FROM injections WHERE created_at >= ? GROUP BY channel ORDER BY COUNT(*) DESC, channel`, since)
	if err != nil {
		return nil, fmt.Errorf("digest injections: %w", err)
	}
	out.InjectionsByChannel = byChannel
	out.InjectionsTotal = injTotal

	top, err := d.topSurfaced(ctx, since, 5)
	if err != nil {
		return nil, fmt.Errorf("digest top surfaced: %w", err)
	}
	out.TopSurfaced = top

	return out, nil
}

// groupCount runs a (label, count) GROUP BY query and also returns the
// total across groups.
func (d *DB) groupCount(ctx context.Context, query string, since time.Time) ([]KindCount, int64, error) {
	rows, err := d.sql.QueryContext(ctx, query, since)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []KindCount
	var total int64
	for rows.Next() {
		var kc KindCount
		if err := rows.Scan(&kc.Kind, &kc.Count); err != nil {
			return nil, 0, err
		}
		out = append(out, kc)
		total += kc.Count
	}
	return out, total, rows.Err()
}

func (d *DB) topSurfaced(ctx context.Context, since time.Time, limit int) ([]SurfacedCount, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT i.kind, i.ref_id,
		       COALESCE(o.title, s.name, '') AS title,
		       COUNT(*) AS n
		  FROM injections i
		  LEFT JOIN observations o ON i.kind = 'observation' AND o.id = i.ref_id
		  LEFT JOIN skills       s ON i.kind = 'skill'       AND s.id = i.ref_id
		 WHERE i.created_at >= ?
		 GROUP BY i.kind, i.ref_id
		 ORDER BY n DESC, i.ref_id
		 LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SurfacedCount
	for rows.Next() {
		var sc SurfacedCount
		var title sql.NullString
		if err := rows.Scan(&sc.Kind, &sc.RefID, &title, &sc.Count); err != nil {
			return nil, err
		}
		sc.Title = title.String
		out = append(out, sc)
	}
	return out, rows.Err()
}
