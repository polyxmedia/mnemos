// Package session tracks agent working sessions: a goal, a start, optional
// end with summary and reflection. Observations link back to their session
// for provenance.
package session

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a session lookup misses.
var ErrNotFound = errors.New("session not found")

// Session is one agent work unit, bounded by a start and (eventually) an end.
type Session struct {
	ID         string
	AgentID    string
	Project    string
	Goal       string
	Summary    string
	Reflection string
	StartedAt  time.Time
	EndedAt    *time.Time
}

// OpenInput is the payload for mnemos_session_start.
type OpenInput struct {
	AgentID string
	Project string
	Goal    string
}

// CloseInput is the payload for mnemos_session_end. Reflection is the
// agent-authored extraction of transferable lessons from the session; it
// feeds skill promotion during consolidation.
type CloseInput struct {
	ID         string
	Summary    string
	Reflection string
}

// Store persists sessions.
type Store interface {
	Insert(ctx context.Context, s *Session) error
	Get(ctx context.Context, id string) (*Session, error)
	Close(ctx context.Context, in CloseInput) error
	Recent(ctx context.Context, agentID string, limit int) ([]Session, error)
	Current(ctx context.Context, agentID string) (*Session, error)
}
