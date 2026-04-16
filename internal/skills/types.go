// Package skills holds procedural memory: reusable step-by-step procedures
// the agent has learned work. Skills are explicitly saved by the agent
// (or promoted by consolidation), versioned, and tracked for effectiveness.
package skills

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a skill lookup misses.
var ErrNotFound = errors.New("skill not found")

// Skill is one reusable procedure.
type Skill struct {
	ID             string
	AgentID        string
	Name           string
	Description    string
	Procedure      string
	Pitfalls       string
	Tags           []string
	SourceSessions []string
	UseCount       int
	SuccessCount   int
	Effectiveness  float64
	Version        int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SaveInput is the agent-provided payload for skill_save. If a skill with the
// same (agent_id, name) exists, it is versioned up, not duplicated.
type SaveInput struct {
	AgentID        string
	Name           string
	Description    string
	Procedure      string
	Pitfalls       string
	Tags           []string
	SourceSessions []string
}

// MatchInput is the payload for skill_match.
type MatchInput struct {
	AgentID string
	Query   string
	Tags    []string
	Limit   int
}

// Match is a ranked skill lookup.
type Match struct {
	Skill Skill
	Score float64
}

// FeedbackInput records whether a skill worked. Updates effectiveness.
type FeedbackInput struct {
	ID      string
	Success bool
}

// Store persists skills.
type Store interface {
	Upsert(ctx context.Context, in SaveInput) (*Skill, error)
	Get(ctx context.Context, id string) (*Skill, error)
	Match(ctx context.Context, in MatchInput) ([]Match, error)
	List(ctx context.Context, agentID string) ([]Skill, error)
	RecordUse(ctx context.Context, in FeedbackInput) error
	Count(ctx context.Context) (int64, error)
}
