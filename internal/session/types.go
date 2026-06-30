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

// Status classifies how a session ended. Observations from failed sessions
// get a ranking boost — agents learn faster from what went wrong than from
// what went right.
type Status string

const (
	StatusOK        Status = "ok"
	StatusFailed    Status = "failed"
	StatusBlocked   Status = "blocked"
	StatusAbandoned Status = "abandoned"
)

// Valid reports whether s is a recognised status value.
func (s Status) Valid() bool {
	switch s {
	case StatusOK, StatusFailed, StatusBlocked, StatusAbandoned:
		return true
	}
	return false
}

// Session is one agent work unit, bounded by a start and (eventually) an end.
type Session struct {
	ID          string
	AgentID     string
	Project     string
	Goal        string
	Summary     string
	Reflection  string
	Status      Status
	OutcomeTags []string
	StartedAt   time.Time
	EndedAt     *time.Time
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
	ID          string
	Summary     string
	Reflection  string
	Status      Status
	OutcomeTags []string
}

// Store persists sessions.
type Store interface {
	Insert(ctx context.Context, s *Session) error
	// Restore inserts a session verbatim (preserving ID, status, summary,
	// reflection, timestamps), skipping on an existing ID. The fidelity-import
	// counterpart to Insert. Returns true when a row was written.
	Restore(ctx context.Context, s *Session) (bool, error)
	Get(ctx context.Context, id string) (*Session, error)
	Close(ctx context.Context, in CloseInput) error
	Recent(ctx context.Context, agentID string, limit int) ([]Session, error)
	Current(ctx context.Context, agentID string) (*Session, error)
	// ListOpen returns every open (ended_at IS NULL) session, newest
	// first, optionally scoped to a project. Empty project means all.
	// SessionStart hooks can fire more than once per Claude session
	// (user-level and project-level installs both fire), so "the" open
	// session is a set, not a row — SessionEnd needs the whole set to
	// close cleanly.
	ListOpen(ctx context.Context, project string) ([]Session, error)
	// SetGoalIfEmpty fills in the goal only when the session has none yet
	// and is still open. No-op (and nil error) when either guard fails.
	// Used by the UserPromptSubmit hook so the first real user prompt
	// becomes the goal of an otherwise-anonymous prewarmed session.
	SetGoalIfEmpty(ctx context.Context, id, goal string) error
}
