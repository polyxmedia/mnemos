package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// EfficacyStats answers the question the digest cannot: not "was memory
// surfaced" but "did surfacing memory go with sessions that ended well".
// It joins the injection log (what was shown) to session outcomes (how it
// went), which is the closest to causal attribution the store can offer
// without an explicit applied signal — see the honesty caveat rendered by
// the efficacy command. Aggregation crosses injections and sessions, so it
// lives on DB directly like Digest.
type EfficacyStats struct {
	Since time.Time

	// WithInjections / WithoutInjections split every session that ended in
	// the window by whether it received any injection. The gap between the
	// two ok-rates is the headline signal (confounded, but the strongest
	// the data supports).
	WithInjections    OutcomeSplit
	WithoutInjections OutcomeSplit

	// ByChannel scores each surfacing channel by the outcome of the ended
	// sessions its injections landed in.
	ByChannel []ChannelEfficacy

	// ByMemory lists individual memories ranked by how many ended sessions
	// they were surfaced into, with the ok-rate of those sessions. A memory
	// surfaced often into sessions that keep getting abandoned is a
	// candidate for pruning; the inverse is a memory earning its keep.
	ByMemory []MemoryEfficacy
}

// OutcomeSplit is a count of ended sessions and how many closed "ok".
type OutcomeSplit struct {
	Sessions int64
	OK       int64
}

// OKPct is the share of sessions that closed ok, 0 when none ended.
func (o OutcomeSplit) OKPct() float64 {
	if o.Sessions == 0 {
		return 0
	}
	return 100 * float64(o.OK) / float64(o.Sessions)
}

// ChannelEfficacy scores one surfacing channel.
type ChannelEfficacy struct {
	Channel    string
	Surfacings int64
	Sessions   int64
	OKSessions int64
}

// OKPct is the share of this channel's ended sessions that closed ok.
func (c ChannelEfficacy) OKPct() float64 {
	if c.Sessions == 0 {
		return 0
	}
	return 100 * float64(c.OKSessions) / float64(c.Sessions)
}

// MemoryEfficacy scores one surfaced memory against session outcomes.
type MemoryEfficacy struct {
	Kind       string
	RefID      string
	Title      string
	Surfacings int64
	Sessions   int64
	OKSessions int64
}

// OKPct is the share of this memory's ended sessions that closed ok.
func (m MemoryEfficacy) OKPct() float64 {
	if m.Sessions == 0 {
		return 0
	}
	return 100 * float64(m.OKSessions) / float64(m.Sessions)
}

// Efficacy computes the surfaced-vs-outcome aggregates since the cutoff.
// Only sessions that have ended are counted — an open session has no
// outcome to attribute. Injections with no session_id (e.g. mnemos_context
// calls) contribute to no channel or memory score, since there is nothing
// to join them to.
func (d *DB) Efficacy(ctx context.Context, since time.Time) (*EfficacyStats, error) {
	since = since.UTC()
	out := &EfficacyStats{Since: since}

	// Session-level split: every ended session in the window, grouped by
	// whether it ever received an injection.
	rows, err := d.sql.QueryContext(ctx, `
		SELECT CASE WHEN i.n > 0 THEN 1 ELSE 0 END AS had,
		       COUNT(*) AS sessions,
		       SUM(CASE WHEN s.status = 'ok' THEN 1 ELSE 0 END) AS ok
		  FROM sessions s
		  LEFT JOIN (SELECT session_id, COUNT(*) n FROM injections GROUP BY session_id) i
		    ON i.session_id = s.id
		 WHERE s.ended_at IS NOT NULL AND s.ended_at >= ?
		 GROUP BY had`, since)
	if err != nil {
		return nil, fmt.Errorf("efficacy session split: %w", err)
	}
	for rows.Next() {
		var had int
		var split OutcomeSplit
		if err := rows.Scan(&had, &split.Sessions, &split.OK); err != nil {
			rows.Close()
			return nil, fmt.Errorf("efficacy scan split: %w", err)
		}
		if had == 1 {
			out.WithInjections = split
		} else {
			out.WithoutInjections = split
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if out.ByChannel, err = d.channelEfficacy(ctx, since); err != nil {
		return nil, fmt.Errorf("efficacy by channel: %w", err)
	}
	if out.ByMemory, err = d.memoryEfficacy(ctx, since, 10); err != nil {
		return nil, fmt.Errorf("efficacy by memory: %w", err)
	}
	return out, nil
}

func (d *DB) channelEfficacy(ctx context.Context, since time.Time) ([]ChannelEfficacy, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT i.channel,
		       COUNT(*) AS surfacings,
		       COUNT(DISTINCT s.id) AS sessions,
		       COUNT(DISTINCT CASE WHEN s.status = 'ok' THEN s.id END) AS ok_sessions
		  FROM injections i
		  JOIN sessions s ON s.id = i.session_id AND s.ended_at IS NOT NULL
		 WHERE i.created_at >= ?
		 GROUP BY i.channel
		 ORDER BY surfacings DESC, i.channel`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChannelEfficacy
	for rows.Next() {
		var c ChannelEfficacy
		if err := rows.Scan(&c.Channel, &c.Surfacings, &c.Sessions, &c.OKSessions); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) memoryEfficacy(ctx context.Context, since time.Time, limit int) ([]MemoryEfficacy, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT i.kind, i.ref_id,
		       COALESCE(o.title, sk.name, '') AS title,
		       COUNT(*) AS surfacings,
		       COUNT(DISTINCT s.id) AS sessions,
		       COUNT(DISTINCT CASE WHEN s.status = 'ok' THEN s.id END) AS ok_sessions
		  FROM injections i
		  JOIN sessions s ON s.id = i.session_id AND s.ended_at IS NOT NULL
		  LEFT JOIN observations o ON i.kind = 'observation' AND o.id = i.ref_id
		  LEFT JOIN skills      sk ON i.kind = 'skill'       AND sk.id = i.ref_id
		 WHERE i.created_at >= ?
		 GROUP BY i.kind, i.ref_id
		 ORDER BY sessions DESC, surfacings DESC, i.ref_id
		 LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MemoryEfficacy
	for rows.Next() {
		var m MemoryEfficacy
		var title sql.NullString
		if err := rows.Scan(&m.Kind, &m.RefID, &title, &m.Surfacings, &m.Sessions, &m.OKSessions); err != nil {
			return nil, err
		}
		m.Title = title.String
		out = append(out, m)
	}
	return out, rows.Err()
}
