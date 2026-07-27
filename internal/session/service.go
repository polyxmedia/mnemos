package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Service wraps the session Store with ID assignment and validation. It is
// thin by design — sessions are bookends around observations; the heavy
// lifting happens in the memory and skills services.
type Service struct {
	store Store
	clock func() time.Time
}

// Config bundles dependencies for NewService.
type Config struct {
	Store Store
	Clock func() time.Time
}

// NewService constructs a session service.
func NewService(cfg Config) *Service {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Service{store: cfg.Store, clock: cfg.Clock}
}

// Open starts a new session and returns it. Multiple open sessions per
// agent are allowed; Current returns the most recent one.
func (s *Service) Open(ctx context.Context, in OpenInput) (*Session, error) {
	agent := in.AgentID
	if agent == "" {
		agent = "default"
	}
	sess := &Session{
		ID:        ulid.Make().String(),
		AgentID:   agent,
		Project:   in.Project,
		Goal:      strings.TrimSpace(in.Goal),
		StartedAt: s.clock().UTC(),
	}
	if err := s.store.Insert(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// Close writes summary + reflection and stamps the end time. Reflection is
// the agent-authored extraction of transferable lessons; it feeds skill
// promotion during consolidation.
func (s *Service) Close(ctx context.Context, in CloseInput) error {
	if in.ID == "" {
		return fmt.Errorf("session close: id required")
	}
	return s.store.Close(ctx, in)
}

// Restore reinserts an exported session verbatim, preserving its ID, status,
// summary, reflection, and timestamps. Keeping the original ID is what lets
// restored observations' session_id foreign keys resolve. Returns true when a
// row was written, false when a session with the same ID already existed.
func (s *Service) Restore(ctx context.Context, sess *Session) (bool, error) {
	if sess.ID == "" {
		return false, fmt.Errorf("session restore: id required")
	}
	return s.store.Restore(ctx, sess)
}

// Get returns a session by ID.
func (s *Service) Get(ctx context.Context, id string) (*Session, error) {
	return s.store.Get(ctx, id)
}

// Recent returns the N most recently started sessions for an agent (or all
// agents if agentID is empty).
func (s *Service) Recent(ctx context.Context, agentID string, limit int) ([]Session, error) {
	return s.store.Recent(ctx, agentID, limit)
}

// Current returns the most recently started open (ended_at IS NULL) session
// for an agent, or ErrNotFound if none is open.
func (s *Service) Current(ctx context.Context, agentID string) (*Session, error) {
	return s.store.Current(ctx, agentID)
}

// ListOpen returns all open sessions, newest first, optionally scoped to
// a project (empty means all projects).
func (s *Service) ListOpen(ctx context.Context, project string) ([]Session, error) {
	return s.store.ListOpen(ctx, project)
}

// TwinWindow bounds how far apart two open sessions for the same project
// may start and still be considered the same terminal's twins. SessionStart
// hooks installed at both user and project scope fire within milliseconds
// of each other, so anything inside this window is a duplicate of one
// terminal launch. Mirrors the prewarm adoption window.
const TwinWindow = 90 * time.Second

// CloseAllOpen closes the newest open session for a project plus any twin
// that started within TwinWindow of it, and returns how many it closed.
// in.ID is ignored.
//
// SessionStart hooks installed at both user and project scope each open a
// session, so SessionEnd must close the set rather than just the newest —
// closing one and leaving its twin open is how the store accumulated
// hundreds of never-ended sessions.
//
// The window is what keeps that from overreaching. Closing every open
// session for the project meant a second terminal working in the same repo
// lost its session the moment the first one exited: on 2026-07-24 two
// taken sessions were closed together at 09:49, and the still-live
// terminal then ran until 23:00 with no session, which cost every
// downstream injection its session attribution. Sessions older than the
// window belong to somebody else; the 24h stale sweep collects them if
// their terminal really is gone.
func (s *Service) CloseAllOpen(ctx context.Context, project string, in CloseInput) (int, error) {
	open, err := s.store.ListOpen(ctx, project)
	if err != nil {
		return 0, err
	}
	if len(open) == 0 {
		return 0, nil
	}
	// ListOpen is ordered started_at DESC, so open[0] is the newest and the
	// most likely owner of the SessionEnd that got us here.
	cutoff := open[0].StartedAt.Add(-TwinWindow)

	closed := 0
	for _, sess := range open {
		if sess.StartedAt.Before(cutoff) {
			continue
		}
		ci := in
		ci.ID = sess.ID
		if err := s.store.Close(ctx, ci); err != nil {
			// A concurrent close (the agent calling mnemos_session_end in
			// parallel) is not a failure of this sweep.
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return closed, err
		}
		closed++
	}
	return closed, nil
}

// SetGoalIfEmpty backfills the goal on a still-open session that has none.
// If goal is empty, or the session is missing, closed, or already has a
// goal, the call is a silent no-op. The UserPromptSubmit hook relies on
// this to be idempotent across repeated user prompts.
func (s *Service) SetGoalIfEmpty(ctx context.Context, id, goal string) error {
	goal = strings.TrimSpace(goal)
	if id == "" || goal == "" {
		return nil
	}
	return s.store.SetGoalIfEmpty(ctx, id, goal)
}
