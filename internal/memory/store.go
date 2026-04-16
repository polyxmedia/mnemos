package memory

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when an observation lookup misses.
var ErrNotFound = errors.New("observation not found")

// Store is the persistence boundary for observations. Implementations live in
// internal/storage; services depend on this interface only.
type Store interface {
	// Insert writes a new observation. The caller has already populated ID,
	// timestamps, and defaults.
	Insert(ctx context.Context, o *Observation) error

	// Get returns an observation by ID and bumps its access counter.
	Get(ctx context.Context, id string) (*Observation, error)

	// Delete removes an observation outright (hard delete). Prefer Invalidate
	// for anything that was ever true.
	Delete(ctx context.Context, id string) error

	// Invalidate marks an observation as no longer true as of validUntil.
	// InvalidatedAt is set to now. The record is not removed.
	Invalidate(ctx context.Context, id string, validUntil time.Time) error

	// Search executes a BM25 FTS5 query with filters and returns raw hits
	// plus the base BM25 score. Ranking is layered on top in the service.
	Search(ctx context.Context, in SearchInput) ([]SearchResult, error)

	// Link records an edge between two observations. If link_type is
	// 'supersedes', the caller should also Invalidate the target.
	Link(ctx context.Context, sourceID, targetID string, linkType LinkType) error

	// Prune removes observations past their expires_at (hard delete). Returns
	// the number of rows removed.
	Prune(ctx context.Context, now time.Time) (int64, error)

	// Stats returns aggregate counts and top-tag summary.
	Stats(ctx context.Context) (Stats, error)

	// ListByTitleSimilarity finds observations with titles similar to the
	// given text (used by consolidation to detect near-duplicates).
	ListByTitleSimilarity(ctx context.Context, agentID, title string, limit int) ([]Observation, error)

	// DecayImportance reduces importance of observations untouched for more
	// than staleDays by the given amount (floor 1). Returns rows affected.
	DecayImportance(ctx context.Context, staleDays int, amount int) (int64, error)
}
