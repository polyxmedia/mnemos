package rumination

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a candidate lookup misses. Callers should
// treat this as "already resolved or never existed" — both are fine.
var ErrNotFound = errors.New("rumination candidate not found")

// Store persists rumination candidates. The surface is deliberately narrow
// — pending/resolve/dismiss are the lifecycle verbs a job queue needs, plus
// target-lookup for prewarm badges. Declared here (at the consumer) so
// tests can fake the minimum surface without pulling in the SQLite impl.
type Store interface {
	// Upsert inserts or updates a candidate. The composite unique key is
	// (monitor_name, target_kind, target_id). On a repeat detection of the
	// same tuple the row's severity, reason, evidence, and updated_at are
	// refreshed but detected_at and status are preserved — so a lingering
	// pending candidate does not look "newer" than it is, and an already-
	// resolved one is not reopened silently. Returns true when this was a
	// fresh insert, false when it updated an existing row.
	Upsert(ctx context.Context, c Candidate) (bool, error)

	// Get returns a candidate by ID. Returns ErrNotFound if the row is
	// absent. Status is not filtered — callers decide whether to care.
	Get(ctx context.Context, id string) (*Candidate, error)

	// Pending returns candidates in status='pending', severity-descending
	// then detected_at descending for stable ordering. Limit <= 0 returns
	// all pending rows.
	Pending(ctx context.Context, limit int) ([]Candidate, error)

	// PendingByTarget returns pending candidates against a specific
	// (target_kind, target_id). Used by prewarm to surface "a rumination is
	// pending against the skill you're about to use".
	PendingByTarget(ctx context.Context, kind TargetKind, targetID string) ([]Candidate, error)

	// Resolve marks a pending candidate as resolved by a revision. The
	// resolvedBy parameter is the ID of the new skill version or superseding
	// observation, carried so replay and audit can trace the revision back
	// to the rumination that triggered it.
	Resolve(ctx context.Context, id, resolvedBy string, at time.Time) error

	// Dismiss marks a candidate as dismissed. Reason is the one-line
	// justification the agent supplies when it decides the rule is fine
	// as-is — preserved so a later rumination pass does not repeatedly
	// raise the same flag without context.
	Dismiss(ctx context.Context, id, reason string, at time.Time) error

	// Counts returns totals by status. Cheap enough to call from stats and
	// from tests that assert state transitions.
	Counts(ctx context.Context) (Counts, error)
}

// Counts bundles the three status totals Counts returns. Named struct keeps
// call sites self-documenting at the cost of one extra type.
type Counts struct {
	Pending   int64
	Resolved  int64
	Dismissed int64
}

// Status is the queue lifecycle state of a candidate. Exposed so tests and
// clients can reference the exact strings without hand-copying them.
type Status string

const (
	StatusPending   Status = "pending"
	StatusResolved  Status = "resolved"
	StatusDismissed Status = "dismissed"
)

