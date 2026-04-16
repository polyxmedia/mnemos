package skills

import (
	"context"
	"fmt"
	"strings"
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

// Get returns a skill by ID.
func (s *Service) Get(ctx context.Context, id string) (*Skill, error) {
	return s.store.Get(ctx, id)
}

// List returns all skills for an agent (or all agents if empty).
func (s *Service) List(ctx context.Context, agentID string) ([]Skill, error) {
	return s.store.List(ctx, agentID)
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
