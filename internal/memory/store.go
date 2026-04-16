package memory

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when an observation lookup misses.
var ErrNotFound = errors.New("observation not found")

// The store surface is segregated so consumers depend only on the subset
// they actually use. The concrete SQLite implementation satisfies all
// interfaces simultaneously; callers accept the narrowest one they need.
//
// The idiom intentionally follows the Go stdlib io package: many small
// interfaces that compose into wider ones (Store is the io.ReadWriter
// analogue). Tests can mock a Reader alone without stubbing write paths
// that don't matter to the subject under test.

// Reader is the read-only observation surface.
type Reader interface {
	Get(ctx context.Context, id string) (*Observation, error)
	Search(ctx context.Context, in SearchInput) ([]SearchResult, error)
	ListByProject(ctx context.Context, agentID, project string, obsType ObsType, limit int) ([]Observation, error)
	ListByTitleSimilarity(ctx context.Context, agentID, title string, limit int) ([]Observation, error)
	FindByContentHash(ctx context.Context, agentID, project, hash string) (*Observation, error)
	Stats(ctx context.Context) (Stats, error)
}

// Writer is the mutating observation surface, excluding housekeeping.
type Writer interface {
	Insert(ctx context.Context, o *Observation) error
	Delete(ctx context.Context, id string) error
	Invalidate(ctx context.Context, id string, validUntil time.Time) error
	Link(ctx context.Context, sourceID, targetID string, linkType LinkType) error
	BumpAccess(ctx context.Context, id string) error
}

// Maintenance is the housekeeping surface — time-based operations that
// run offline (consolidation, pruning).
type Maintenance interface {
	Prune(ctx context.Context, now time.Time) (int64, error)
	DecayImportance(ctx context.Context, staleDays int, amount int) (int64, error)
}

// Exportable tracks external-system sync state (currently: Obsidian vault).
type Exportable interface {
	MarkExported(ctx context.Context, id string, at time.Time) error
}

// Vectorable is the embedding-specific surface used by the backfill path.
type Vectorable interface {
	UpdateEmbedding(ctx context.Context, id, model string, vec []float32) error
	ListMissingEmbeddings(ctx context.Context, limit int) ([]Observation, error)
}

// Store is the union satisfied by the SQLite implementation. Most
// consumers should depend on one of the narrower interfaces; Service
// takes the full union because it genuinely needs all five surfaces.
type Store interface {
	Reader
	Writer
	Maintenance
	Exportable
	Vectorable
}

// TouchStore persists file-touch events (file heat map).
type TouchStore interface {
	Record(ctx context.Context, in TouchInput) error
	Hot(ctx context.Context, agentID, project string, limit int) ([]HotFile, error)
}
