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
