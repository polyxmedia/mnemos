package rumination

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// SkillReader is the narrow read surface the Service depends on for
// resolving skill targets. Declared at the consumer (not in the skills
// package) so the Service depends only on what it actually needs.
type SkillReader interface {
	Get(ctx context.Context, id string) (*skills.Skill, error)
	List(ctx context.Context, agentID string) ([]skills.Skill, error)
}

// Service composes detection, packaging, and the queue store. Monitors
// emit thin Candidates; Detect returns them live (no persistence), and
// PersistDetected writes them through the Store so they survive beyond
// the dream pass. Pack hydrates a target body on demand. Resolve and
// Dismiss close candidates with provenance.
type Service struct {
	monitors []Monitor
	skills   SkillReader
	memory   memory.Reader
	store    Store
	now      func() time.Time
}

// Config bundles dependencies. Monitors is the full set to run, in the
// order the caller wants them reported. Store is optional for callers
// that only need live detection (tests, preview tools); the persistence,
// Pending, Resolve, and Dismiss methods all error cleanly when it's nil.
type Config struct {
	Monitors []Monitor
	Skills   SkillReader
	Memory   memory.Reader
	Store    Store

	// Now is a clock override for tests. Unset in production.
	Now func() time.Time
}

// NewService constructs a rumination service.
func NewService(cfg Config) *Service {
	clock := cfg.Now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		monitors: cfg.Monitors,
		skills:   cfg.Skills,
		memory:   cfg.Memory,
		store:    cfg.Store,
		now:      clock,
	}
}

// Detect runs every configured monitor and returns the combined candidate
// list, sorted severity-descending with reason-ascending as the stable
// tiebreaker so identical inputs always produce the same ordering in
// tests and logs.
func (s *Service) Detect(ctx context.Context) ([]Candidate, error) {
	var all []Candidate
	for _, m := range s.monitors {
		found, err := m.Detect(ctx)
		if err != nil {
			return nil, fmt.Errorf("monitor %s: %w", m.Name(), err)
		}
		all = append(all, found...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Severity != all[j].Severity {
			return all[i].Severity > all[j].Severity
		}
		return all[i].Reason < all[j].Reason
	})
	return all, nil
}

// Pack resolves the candidate's target and renders the review block. If
// the target has been deleted since detection (skill purged, observation
// hard-deleted) the caller gets a wrapped error they can log and drop the
// candidate on.
func (s *Service) Pack(ctx context.Context, c Candidate) (Block, error) {
	target, err := s.resolveTarget(ctx, c)
	if err != nil {
		return Block{}, err
	}
	return Pack(c, target), nil
}

// PersistDetected runs every monitor and upserts each Candidate through
// the Store. Returns the number of fresh rows and the number of updates
// so the dream pass can report both counts in its journal. On the first
// upsert error the method stops and returns the error along with the
// partial counts — the upsert is idempotent per dedup key, so the caller
// can retry safely.
func (s *Service) PersistDetected(ctx context.Context) (inserted, updated int, err error) {
	if s.store == nil {
		return 0, 0, fmt.Errorf("rumination: store not configured")
	}
	candidates, err := s.Detect(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, c := range candidates {
		fresh, err := s.store.Upsert(ctx, c)
		if err != nil {
			return inserted, updated, fmt.Errorf("upsert %s: %w", c.ID, err)
		}
		if fresh {
			inserted++
		} else {
			updated++
		}
	}
	return inserted, updated, nil
}

// Pending returns pending candidates from the store, ordered by severity
// then age. limit <= 0 returns all.
func (s *Service) Pending(ctx context.Context, limit int) ([]Candidate, error) {
	if s.store == nil {
		return nil, fmt.Errorf("rumination: store not configured")
	}
	return s.store.Pending(ctx, limit)
}

// Get returns one candidate by ID. Thin passthrough — exposed so callers
// that need to inspect state before a mutation (e.g. dream auto-resolve
// checking whether a candidate is still pending) don't have to scan the
// Pending list.
func (s *Service) Get(ctx context.Context, id string) (*Candidate, error) {
	if s.store == nil {
		return nil, fmt.Errorf("rumination: store not configured")
	}
	return s.store.Get(ctx, id)
}

// PendingByTarget is the prewarm hook — it answers "is there a live
// rumination against the skill or observation I'm about to surface?".
func (s *Service) PendingByTarget(ctx context.Context, kind TargetKind, targetID string) ([]Candidate, error) {
	if s.store == nil {
		return nil, fmt.Errorf("rumination: store not configured")
	}
	return s.store.PendingByTarget(ctx, kind, targetID)
}

// PackByID loads a candidate from the store and renders it. The common
// agent-facing path: mnemos_ruminate_list picks an id, mnemos_ruminate_pack
// hands back the block.
func (s *Service) PackByID(ctx context.Context, id string) (Block, error) {
	if s.store == nil {
		return Block{}, fmt.Errorf("rumination: store not configured")
	}
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return Block{}, fmt.Errorf("load candidate %s: %w", id, err)
	}
	return s.Pack(ctx, *c)
}

// Resolve marks the candidate as resolved by the given revision ID. The
// revision ID must be non-empty — the Popper-style requirement that a
// replacement name its new prediction maps structurally onto the
// provenance contract: every resolution carries a pointer to a concrete
// object (skill version or superseding observation).
func (s *Service) Resolve(ctx context.Context, id, resolvedBy string) error {
	if s.store == nil {
		return fmt.Errorf("rumination: store not configured")
	}
	return s.store.Resolve(ctx, id, resolvedBy, s.now())
}

// Dismiss closes the candidate as "rule stands; evidence was noise". The
// reason is required; it preserves the judgement so a later pass doesn't
// re-raise the same flag without context.
func (s *Service) Dismiss(ctx context.Context, id, reason string) error {
	if s.store == nil {
		return fmt.Errorf("rumination: store not configured")
	}
	return s.store.Dismiss(ctx, id, reason, s.now())
}

// Counts returns queue totals for stats/UI.
func (s *Service) Counts(ctx context.Context) (Counts, error) {
	if s.store == nil {
		return Counts{}, fmt.Errorf("rumination: store not configured")
	}
	return s.store.Counts(ctx)
}

func (s *Service) resolveTarget(ctx context.Context, c Candidate) (TargetRef, error) {
	switch c.TargetKind {
	case TargetSkill:
		if s.skills == nil {
			return TargetRef{}, fmt.Errorf("rumination: skills reader not configured")
		}
		sk, err := s.skills.Get(ctx, c.TargetID)
		if err != nil {
			return TargetRef{}, fmt.Errorf("load skill %s: %w", c.TargetID, err)
		}
		return TargetRef{
			Kind: TargetSkill,
			ID:   sk.ID,
			Name: sk.Name,
			Body: sk.Procedure,
		}, nil
	case TargetObservation:
		if s.memory == nil {
			return TargetRef{}, fmt.Errorf("rumination: memory reader not configured")
		}
		o, err := s.memory.Get(ctx, c.TargetID)
		if err != nil {
			return TargetRef{}, fmt.Errorf("load observation %s: %w", c.TargetID, err)
		}
		return TargetRef{
			Kind: TargetObservation,
			ID:   o.ID,
			Name: o.Title,
			Body: o.Content,
		}, nil
	default:
		return TargetRef{}, fmt.Errorf("rumination: unknown target kind %q", c.TargetKind)
	}
}
