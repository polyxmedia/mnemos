// Package injection records which memories were surfaced into agent
// context, and through which channel. This is the measurement substrate:
// observations only bump access_count on explicit Get, so without an
// injection log the thing mnemos does most (passive context injection via
// prewarm and the prompt hook) is the thing it measures least. Every
// surfaced-vs-applied metric, the digest's "12 memories surfaced" line,
// and Bet 4's causal attribution all read from this table.
//
// Writes are fire-and-forget from the caller's perspective: surfacing
// memory must never fail because the measurement write failed.
package injection

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"
)

// Kind names what was surfaced. Only observations and skills are tracked —
// the other prewarm sections (recent sessions, hot files, rumination
// candidates) are navigation aids, not memories whose usefulness we score.
type Kind string

const (
	KindObservation Kind = "observation"
	KindSkill       Kind = "skill"
)

// Channel names the surface a memory was injected through.
type Channel string

const (
	// ChannelPrewarm: the SessionStart prewarm block.
	ChannelPrewarm Channel = "prewarm"
	// ChannelRecovery: the compaction-recovery prewarm block.
	ChannelRecovery Channel = "recovery"
	// ChannelContext: an explicit mnemos_context tool call.
	ChannelContext Channel = "context"
	// ChannelPromptHook: the UserPromptSubmit hook's auto-search block.
	ChannelPromptHook Channel = "prompt_hook"
)

// Ref names one surfaced memory: the kind plus its store ID.
type Ref struct {
	Kind Kind
	ID   string
}

// Event is one persisted surfacing of one memory.
type Event struct {
	ID        string
	Kind      Kind
	RefID     string
	Channel   Channel
	AgentID   string
	Project   string
	SessionID string
	CreatedAt time.Time
}

// Store persists injection events.
type Store interface {
	Record(ctx context.Context, events []Event) error
	// ListByRef returns the surfacing history for one memory, newest first.
	ListByRef(ctx context.Context, kind Kind, refID string, limit int) ([]Event, error)
}

// Logger stamps IDs and timestamps onto refs and writes them as events.
// A nil *Logger is a valid no-op, so callers can wire it unconditionally.
type Logger struct {
	store Store
	clock func() time.Time
}

// NewLogger constructs a Logger. Clock defaults to time.Now; tests inject
// a fixed clock so bi-temporal assertions don't race the wall clock.
func NewLogger(store Store, clock func() time.Time) *Logger {
	if clock == nil {
		clock = time.Now
	}
	return &Logger{store: store, clock: clock}
}

// Log records one event per ref through the given channel. Refs with an
// empty ID are skipped. Safe on a nil receiver.
func (l *Logger) Log(ctx context.Context, channel Channel, agentID, project, sessionID string, refs []Ref) error {
	if l == nil || l.store == nil || len(refs) == 0 {
		return nil
	}
	now := l.clock().UTC()
	events := make([]Event, 0, len(refs))
	for _, r := range refs {
		if r.ID == "" {
			continue
		}
		events = append(events, Event{
			ID:        ulid.Make().String(),
			Kind:      r.Kind,
			RefID:     r.ID,
			Channel:   channel,
			AgentID:   agentID,
			Project:   project,
			SessionID: sessionID,
			CreatedAt: now,
		})
	}
	if len(events) == 0 {
		return nil
	}
	return l.store.Record(ctx, events)
}
