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

// Valid reports whether k is a recognised kind. Enforced at the storage
// layer on write — the schema deliberately carries no CHECK (migration
// 0005) so new kinds are a code change, not a table rebuild.
func (k Kind) Valid() bool {
	switch k {
	case KindObservation, KindSkill:
		return true
	}
	return false
}

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
	// ChannelPreTool: just-in-time memory fed into PreToolUse on file
	// edits — corrections/conventions relevant to the file being written.
	ChannelPreTool Channel = "pre_tool"
	// ChannelPremortem: the mnemos_premortem tool — failure-relevant
	// memory matched against a proposed plan before execution.
	ChannelPremortem Channel = "premortem"
)

// Valid reports whether c is a recognised channel. Same contract as
// Kind.Valid: Go-side enum enforcement instead of a schema CHECK.
func (c Channel) Valid() bool {
	switch c {
	case ChannelPrewarm, ChannelRecovery, ChannelContext,
		ChannelPromptHook, ChannelPreTool, ChannelPremortem:
		return true
	}
	return false
}

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
	// RecentRefIDs returns the ref IDs of the given kind surfaced through
	// any of the given channels since the cutoff, scoped to project when
	// project is non-empty. Backs re-injection suppression.
	RecentRefIDs(ctx context.Context, kind Kind, project string, since time.Time, channels []Channel) ([]string, error)
}

// SuppressWindow is how long a surfaced memory stays suppressed from the
// repeating channels. Once a fact is in the agent's context it stays there
// until compaction, so re-injecting it every prompt or every file edit
// buys nothing and costs tokens on every turn. Two hours is long enough to
// cover a working stretch and short enough that a post-compaction context
// gets the fact back.
//
// Sized against real data: over 2026-07-24 a single memory was injected
// into one project 352 times in a day through pre_tool alone, because the
// only suppression rule keyed on a session ID that was absent 96% of the
// time. A wall-clock window has no such dependency.
const SuppressWindow = 2 * time.Hour

// Suppressed returns the set of ref IDs surfaced into this project's
// context recently enough that surfacing them again would be a duplicate.
//
// Every channel counts as evidence that the fact is already in context,
// including prewarm and recovery: a memory the SessionStart block just
// stated does not need restating by the first prompt ten seconds later.
// Which channels are *subject* to suppression is decided by the call
// sites, and only the two high-frequency ones (prompt_hook, pre_tool) ask.
// prewarm and recovery deliberately never ask, because both fire at
// context boundaries where restating a memory is the entire point.
//
// A nil store or any query error yields an empty set: suppression is an
// optimisation, and failing to suppress must never stop a memory from
// reaching the agent.
func Suppressed(ctx context.Context, store Store, kind Kind, project string, now time.Time) map[string]struct{} {
	out := map[string]struct{}{}
	if store == nil {
		return out
	}
	ids, err := store.RecentRefIDs(ctx, kind, project, now.Add(-SuppressWindow), nil)
	if err != nil {
		return out
	}
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
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
