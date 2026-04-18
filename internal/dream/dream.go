// Package dream runs the sleep-time compute consolidation pass: the
// "dream cycle" where Mnemos organises memory while the agent is idle.
// Each pass produces a dream journal — a single observation of type
// TypeDream summarising what was changed. This gives the agent a way to
// query what happened while it was "asleep".
//
// Consolidation operations:
//   - Importance decay: observations untouched for N days drop in importance.
//   - Prune: expired observations are removed.
//   - Dedup sweep: near-duplicate titles within the same project are linked
//     with 'refines' edges so search doesn't double-count them.
//   - (Hook for future) Skill promotion: patterns reused across sessions
//     become skill candidates.
package dream

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// Journal records the outcome of one consolidation pass. Mirrored into
// memory as an observation of type 'dream' so the agent can query past
// dreams via regular search.
type Journal struct {
	StartedAt  time.Time
	FinishedAt time.Time
	Pruned     int64
	Decayed    int64
	Linked     int64
	Promoted   int
	Notes      []string
}

// Summary produces the text persisted as the dream observation content.
func (j Journal) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dream pass %s → %s\n", j.StartedAt.Format(time.RFC3339), j.FinishedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- pruned: %d\n", j.Pruned)
	fmt.Fprintf(&b, "- decayed: %d\n", j.Decayed)
	fmt.Fprintf(&b, "- linked: %d\n", j.Linked)
	fmt.Fprintf(&b, "- promoted: %d\n", j.Promoted)
	for _, n := range j.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Service runs consolidation. Stateless — every Run is independent.
// Depends on the Maintenance surface of the observation store, the
// memory.Service for dream journal writes, and (optionally) the reader
// + skills service for correction → skill promotion.
type Service struct {
	mem    *memory.Service
	store  memory.Maintenance
	reader memory.Reader
	skills *skills.Service
	log    *slog.Logger

	staleDays      int
	decayAmount    int
	dedupThreshold int
}

// Config bundles dependencies.
type Config struct {
	Memory      *memory.Service
	Store       memory.Maintenance
	Reader      memory.Reader   // optional; enables correction → skill promotion
	Skills      *skills.Service // optional; required alongside Reader for promotion
	Logger      *slog.Logger
	StaleDays   int // importance decay kicks in past this many days idle; default 30
	DecayAmount int // how much to subtract; default 1
	DedupWindow int // how many title-similar candidates to link per primary; default 3
}

// NewService constructs a dream service.
func NewService(cfg Config) *Service {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.StaleDays == 0 {
		cfg.StaleDays = 30
	}
	if cfg.DecayAmount == 0 {
		cfg.DecayAmount = 1
	}
	if cfg.DedupWindow == 0 {
		cfg.DedupWindow = 3
	}
	return &Service{
		mem:            cfg.Memory,
		store:          cfg.Store,
		reader:         cfg.Reader,
		skills:         cfg.Skills,
		log:            cfg.Logger,
		staleDays:      cfg.StaleDays,
		decayAmount:    cfg.DecayAmount,
		dedupThreshold: cfg.DedupWindow,
	}
}

// Run executes one consolidation pass and returns the journal. Idempotent
// within a time window — calling twice in the same minute produces a
// near-empty journal on the second call.
func (s *Service) Run(ctx context.Context, writeJournal bool) (*Journal, error) {
	j := &Journal{StartedAt: time.Now().UTC()}

	// 1. Prune expired observations.
	pruned, err := s.store.Prune(ctx, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("prune: %w", err)
	}
	j.Pruned = pruned

	// 2. Importance decay on stale observations.
	decayed, err := s.store.DecayImportance(ctx, s.staleDays, s.decayAmount)
	if err != nil {
		return nil, fmt.Errorf("decay: %w", err)
	}
	j.Decayed = decayed

	// 3. Skill promotion from correction clusters. Optional: guarded
	// internally when reader/skills are not wired. Never fatal; the rest
	// of the pass should complete even if promotion fails.
	if promoted, err := s.promoteSkillsFromCorrections(ctx); err != nil {
		s.log.Warn("promote skills", "err", err)
	} else {
		j.Promoted = promoted
	}

	j.FinishedAt = time.Now().UTC()

	s.log.Info("dream pass",
		"pruned", j.Pruned, "decayed", j.Decayed, "linked", j.Linked,
		"promoted", j.Promoted,
		"duration", j.FinishedAt.Sub(j.StartedAt))

	// 3. Write the dream journal as a memory observation so the agent can
	// retrieve it via standard search. Guarded because callers may run
	// consolidation frequently and don't always want an observation per run.
	if writeJournal && (j.Pruned > 0 || j.Decayed > 0 || j.Linked > 0 || j.Promoted > 0) {
		_, err := s.mem.Save(ctx, memory.SaveInput{
			Title:      "dream pass " + j.StartedAt.Format("2006-01-02 15:04"),
			Content:    j.Summary(),
			Type:       memory.TypeDream,
			Tags:       []string{"consolidation", "dream"},
			Importance: 3, // low — housekeeping, not content
		})
		if err != nil {
			s.log.Warn("failed to write dream journal", "err", err)
		}
	}

	return j, nil
}
