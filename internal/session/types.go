// Package session tracks agent working sessions: a goal, a start, optional
// end with summary and reflection. Observations link back to their session
// for provenance.
package session

import (
	"context"
	"crypto/sha256"
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
	// HostSessionID is the host's own identifier for the conversation this
	// session tracks (Claude Code's session_id from the hook payload).
	// Empty for sessions opened through mnemos_session_start or by a host
	// that sends none.
	HostSessionID string
	// RetrievalArm is how this session receives memory. Empty means no
	// assignment was made, which behaves as ArmPush.
	RetrievalArm RetrievalArm
}

// RetrievalArm names how a session receives memory from the store.
type RetrievalArm string

const (
	// ArmPush: the prompt hook searches on every prompt and injects hits.
	ArmPush RetrievalArm = "push"
	// ArmIndex: the session starts with a titles-only index and the agent
	// opens what it wants. The prompt hook injects nothing.
	ArmIndex RetrievalArm = "index"
)

// Retrieval modes accepted by the [retrieval] mode config setting.
const (
	ModePush       = "push"
	ModeIndex      = "index"
	ModeExperiment = "experiment"
)

// AssignArm picks the arm for a new session. In experiment mode the choice
// is a hash of the host's conversation ID, so it is effectively random
// across conversations and stable for one: resuming a conversation, or a
// duplicate SessionStart install firing twice, cannot move it between arms.
//
// Experiment mode only assigns sessions with a host ID, because the report
// reads outcomes from that conversation's transcript. Everything else, and
// push mode, returns "" so the session stays out of the comparison.
func AssignArm(mode, hostSessionID string) RetrievalArm {
	switch mode {
	case ModeIndex:
		return ArmIndex
	case ModeExperiment:
		if hostSessionID == "" {
			return ""
		}
		sum := sha256.Sum256([]byte(hostSessionID))
		if sum[0]&1 == 1 {
			return ArmIndex
		}
		return ArmPush
	}
	return ""
}

// OpenInput is the payload for mnemos_session_start.
type OpenInput struct {
	AgentID       string
	Project       string
	Goal          string
	HostSessionID string
	RetrievalArm  RetrievalArm
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
	// RecentInProject is Recent scoped to one project.
	RecentInProject(ctx context.Context, agentID, project string, limit int) ([]Session, error)
	Current(ctx context.Context, agentID string) (*Session, error)
	// ListOpen returns every open (ended_at IS NULL) session, newest
	// first, optionally scoped to a project. Empty project means all.
	// SessionStart hooks can fire more than once per Claude session
	// (user-level and project-level installs both fire), so "the" open
	// session is a set, not a row — SessionEnd needs the whole set to
	// close cleanly.
	ListOpen(ctx context.Context, project string) ([]Session, error)
	// ListOpenByHost returns the open sessions carrying hostSessionID,
	// newest first. More than one means duplicate SessionStart installs
	// each opened a session for the same host conversation.
	ListOpenByHost(ctx context.Context, hostSessionID string) ([]Session, error)
	// SetGoalIfEmpty fills in the goal only when the session has none yet
	// and is still open. No-op (and nil error) when either guard fails.
	// Used by the UserPromptSubmit hook so the first real user prompt
	// becomes the goal of an otherwise-anonymous prewarmed session.
	SetGoalIfEmpty(ctx context.Context, id, goal string) error
}
