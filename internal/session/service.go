package session

import (
	"context"
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
