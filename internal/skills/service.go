package skills

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Service is the agent-facing API for procedural memory. It validates input
// and delegates to the Store, which handles versioning and effectiveness
// tracking.
type Service struct {
	store Store
}

// Config bundles dependencies for NewService.
type Config struct {
	Store Store
}

// NewService constructs a skill service.
func NewService(cfg Config) *Service {
	return &Service{store: cfg.Store}
}

// Save creates or versions-up a skill. A skill is keyed by (agent_id, name);
// saving the same name again bumps the version and keeps history in the
// source_sessions provenance field (if the agent includes it).
func (s *Service) Save(ctx context.Context, in SaveInput) (*Skill, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("skill save: name required")
	}
	if strings.TrimSpace(in.Description) == "" {
		return nil, fmt.Errorf("skill save: description required")
	}
	if strings.TrimSpace(in.Procedure) == "" {
		return nil, fmt.Errorf("skill save: procedure required")
	}
	return s.store.Upsert(ctx, in)
}

// Match returns skills ranked by FTS relevance, with effectiveness as a
// tiebreaker nudge (so skills that actually worked rise to the top).
func (s *Service) Match(ctx context.Context, in MatchInput) ([]Match, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, fmt.Errorf("skill match: query required")
	}
	return s.store.Match(ctx, in)
}

// Restore reinserts an exported skill verbatim, preserving its ID, version,
// effectiveness, use/success counts, and timestamps. Unlike Save (which
// version-bumps an existing name), it is the fidelity-import path: an already
// present skill is skipped, not re-versioned. Returns true when a row was
// written.
func (s *Service) Restore(ctx context.Context, sk *Skill) (bool, error) {
	if sk.ID == "" {
		return false, fmt.Errorf("skill restore: id required")
	}
	return s.store.Restore(ctx, sk)
}

// Get returns a skill by ID.
func (s *Service) Get(ctx context.Context, id string) (*Skill, error) {
	return s.store.Get(ctx, id)
}

// List returns all skills for an agent (or all agents if empty).
func (s *Service) List(ctx context.Context, agentID string) ([]Skill, error) {
	return s.store.List(ctx, agentID)
}

// confidenceFloor is the use_count at which a skill's measured
// effectiveness is considered fully trustworthy. Below this, we shrink
// toward the 0.5 prior so a 1/1 record doesn't outrank a 9/10 one.
const confidenceFloor = 10

// stalenessHorizonDays sets the linear decay window for the recency
// factor. A skill last used today scores 1.0; one untouched for this
// many days scores 0.0. Tuned to roughly match the dream pass's
// stale_skill_days default.
const stalenessHorizonDays = 90

// Score returns a structured quality report for one skill, combining
// raw effectiveness, use count, recency, and provenance into one
// composite number plus the inputs that built it. The intent: clients
// (dashboards, "skills ranked by lift" registries, the MCP tool surface)
// pick whichever weighting they want without recomputing from scratch.
func (s *Service) Score(ctx context.Context, id string) (*ScoreReport, error) {
	if id == "" {
		return nil, fmt.Errorf("skill score: id required")
	}
	sk, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	rep := &ScoreReport{
		SkillID:       sk.ID,
		Effectiveness: sk.Effectiveness,
		UseCount:      sk.UseCount,
		SuccessCount:  sk.SuccessCount,
		UpdatedAt:     sk.UpdatedAt,
	}

	// Bayesian shrinkage: a 1/1 skill at 1.0 effectiveness is much less
	// trustworthy than a 9/10 skill at 0.9. Pulling the low-volume
	// estimate toward the 0.5 prior corrects the small-sample
	// over-confidence; at confidenceFloor uses, the raw rate stands.
	confidence := float64(sk.UseCount) / float64(confidenceFloor)
	if confidence > 1.0 {
		confidence = 1.0
	}
	rep.AdjustedEffectiveness = sk.Effectiveness*confidence + 0.5*(1-confidence)

	// Linear decay from last update. now() reads from the same monotonic
	// source the store uses, so a skill written this turn scores 1.0.
	since := time.Since(sk.UpdatedAt).Hours() / 24
	rep.Recency = 1.0 - since/float64(stalenessHorizonDays)
	if rep.Recency < 0 {
		rep.Recency = 0
	}
	if sk.UpdatedAt.IsZero() {
		rep.Recency = 0
	}

	rep.Score = rep.AdjustedEffectiveness * rep.Recency
	return rep, nil
}

// RecordUse updates usage and effectiveness based on agent feedback. This is
// how skills earn their rank position over time.
func (s *Service) RecordUse(ctx context.Context, in FeedbackInput) error {
	if in.ID == "" {
		return fmt.Errorf("skill feedback: id required")
	}
	return s.store.RecordUse(ctx, in)
}

// ExportPack builds a shareable pack from an agent's skills. If names is
// empty, every skill for the agent is included. Passing specific names
// lets users publish a focused subset (e.g. "just the go error-handling
// skills"). Returns ErrNotFound if any requested name is missing.
func (s *Service) ExportPack(ctx context.Context, agentID string, names []string, source PackSource) (*Pack, error) {
	all, err := s.store.List(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	if len(names) == 0 {
		if len(all) == 0 {
			return nil, fmt.Errorf("export: no skills to pack")
		}
		return BuildPack(source, all), nil
	}
	byName := make(map[string]Skill, len(all))
	for _, s := range all {
		byName[s.Name] = s
	}
	selected := make([]Skill, 0, len(names))
	for _, n := range names {
		sk, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("export: skill %q not found", n)
		}
		selected = append(selected, sk)
	}
	return BuildPack(source, selected), nil
}

// ImportResult summarises what ImportPack did. Counts distinguish skills
// that were created brand-new from ones that updated an existing skill
// under the same name (producing a version bump).
type ImportResult struct {
	Created int
	Updated int
	Source  PackSource
}

// ImportPack persists every skill in the pack for the given agent.
// Existing skills with the same name are version-bumped via Upsert; new
// ones are created. Returns a summary of what changed.
func (s *Service) ImportPack(ctx context.Context, agentID string, pack *Pack) (ImportResult, error) {
	result := ImportResult{Source: pack.Source}
	existing, err := s.store.List(ctx, agentID)
	if err != nil {
		return result, fmt.Errorf("list existing: %w", err)
	}
	existingNames := make(map[string]bool, len(existing))
	for _, sk := range existing {
		existingNames[sk.Name] = true
	}
	for _, ps := range pack.Skills {
		_, err := s.store.Upsert(ctx, SaveInput{
			AgentID: agentID, Name: ps.Name,
			Description: ps.Description,
			Procedure:   ps.Procedure, Pitfalls: ps.Pitfalls,
			Tags: ps.Tags,
		})
		if err != nil {
			return result, fmt.Errorf("import %q: %w", ps.Name, err)
		}
		if existingNames[ps.Name] {
			result.Updated++
		} else {
			result.Created++
		}
	}
	return result, nil
}
