// Package replay reconstructs a prior session with the benefit of
// everything learned since: conventions added after it started, matching
// corrections recorded later, skills promoted in the meantime, and any
// observations from the session that have been superseded or invalidated.
//
// The output is a markdown document that can be pasted back into an
// agent's context so it can answer "what would I do differently now?".
// This turns Mnemos into a tool for retrospective self-improvement —
// not just memory, but *learning loop compounding*.
package replay

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// Service builds replay blocks. It depends on read-side interfaces for
// observations, sessions, and skills — no mutating calls.
type Service struct {
	obs       memory.Reader
	sessions  session.Store
	skills    skills.Store
	maxTokens int
}

// Config bundles dependencies. MaxTokens defaults to 3000 — replay is a
// deliberate human-triggered action, so the budget is larger than the
// auto-pushed pre-warm block.
type Config struct {
	Observations memory.Reader
	Sessions     session.Store
	Skills       skills.Store
	MaxTokens    int
}

// NewService constructs a replay service.
func NewService(cfg Config) *Service {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 3000
	}
	return &Service{
		obs:       cfg.Observations,
		sessions:  cfg.Sessions,
		skills:    cfg.Skills,
		maxTokens: cfg.MaxTokens,
	}
}

// Request parameters. SessionID is required; everything else is optional
// and overrides what we can derive from the session record.
type Request struct {
	SessionID string
	AgentID   string // defaults to the session's agent
	Project   string // defaults to the session's project
	MaxTokens int
}

// Block is the composed replay payload.
type Block struct {
	Text           string
	TokenEstimate  int
	Session        *session.Session
	Observations   []memory.Observation
	NewConventions []memory.Observation
	NewCorrections []memory.Observation
	NewSkills      []skills.Skill
	Superseded     []Superseded
}

// Superseded records one of the session's observations that has been
// invalidated since the session ran — the most interesting thing to
// surface because the agent was operating on now-stale facts.
type Superseded struct {
	Original   memory.Observation
	ReplacedBy *memory.Observation // nil if just invalidated (no replacement link)
}

// Build constructs a replay block for req.SessionID. Errors if the
// session doesn't exist.
func (s *Service) Build(ctx context.Context, req Request) (*Block, error) {
	if req.SessionID == "" {
		return nil, errors.New("replay: session_id required")
	}
	sess, err := s.sessions.Get(ctx, req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	agent := req.AgentID
	if agent == "" {
		agent = sess.AgentID
	}
	project := req.Project
	if project == "" {
		project = sess.Project
	}

	// "Since" cutoff: end of the session (or start if still open).
	since := sess.StartedAt
	if sess.EndedAt != nil {
		since = *sess.EndedAt
	}

	block := &Block{Session: sess}

	// 1. Observations from inside the session — ALL of them, including
	// invalidated ones, so we can flag what's been superseded since.
	inSession, err := s.obs.ListBySession(ctx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("list session obs: %w", err)
	}
	block.Observations = inSession
	sort.Slice(block.Observations, func(i, j int) bool {
		return block.Observations[i].CreatedAt.Before(block.Observations[j].CreatedAt)
	})

	// 2. Conventions for the project that were added after the session —
	// only live ones (filtered by ListByProject).
	projectObs, err := s.obs.ListByProject(ctx, agent, project, "", 500)
	if err != nil {
		return nil, fmt.Errorf("list project obs: %w", err)
	}
	for _, o := range projectObs {
		if o.Type == memory.TypeConvention && o.CreatedAt.After(since) {
			block.NewConventions = append(block.NewConventions, o)
		}
	}

	// 3. Corrections recorded after the session — matched loosely by
	// searching the session's goal.
	if strings.TrimSpace(sess.Goal) != "" {
		hits, err := s.obs.Search(ctx, memory.SearchInput{
			Query: sess.Goal, Type: memory.TypeCorrection,
			AgentID: agent, Project: project, Limit: 20,
		})
		if err == nil {
			for _, h := range hits {
				if h.Observation.CreatedAt.After(since) {
					block.NewCorrections = append(block.NewCorrections, h.Observation)
				}
			}
		}
	}

	// 4. Skills added since (regardless of project — skills are typically
	// agent-scoped and broadly useful).
	sks, err := s.skills.List(ctx, agent)
	if err == nil {
		for _, sk := range sks {
			if sk.CreatedAt.After(since) {
				block.NewSkills = append(block.NewSkills, sk)
			}
		}
	}

	// 5. Superseded observations — any session-observation that has been
	// invalidated since or has a newer observation linked as supersedes.
	for _, o := range block.Observations {
		if o.InvalidatedAt != nil && o.InvalidatedAt.After(since) {
			block.Superseded = append(block.Superseded, Superseded{Original: o})
		}
	}

	budget := req.MaxTokens
	if budget <= 0 {
		budget = s.maxTokens
	}
	block.Text = render(block, budget)
	block.TokenEstimate = estimateTokens(block.Text)
	return block, nil
}

// render produces the markdown doc. The sections are ordered by what the
// agent most needs to re-orient: first "what's changed since?", then the
// original work, then forward-looking hints.
func render(b *Block, budget int) string {
	var sb strings.Builder
	sess := b.Session

	fmt.Fprintf(&sb, "# Replay · %s\n\n", defaultTitle(sess.Goal, sess.ID))
	fmt.Fprintf(&sb, "_Session started %s", sess.StartedAt.Format(time.RFC3339))
	if sess.EndedAt != nil {
		dur := sess.EndedAt.Sub(sess.StartedAt).Truncate(time.Minute)
		fmt.Fprintf(&sb, " · ended after %s · status **%s**_\n\n", dur, sess.Status)
	} else {
		sb.WriteString(" · still open_\n\n")
	}
	if sess.Summary != "" {
		fmt.Fprintf(&sb, "**Summary at close:** %s\n\n", sess.Summary)
	}
	if sess.Reflection != "" {
		fmt.Fprintf(&sb, "**Reflection:** %s\n\n", sess.Reflection)
	}

	// What's changed since — surfaced first because this is the replay
	// payoff.
	if len(b.NewCorrections) > 0 {
		sb.WriteString("## Corrections recorded since\n")
		sb.WriteString("_If you were working on this now, you'd avoid these._\n\n")
		for _, c := range b.NewCorrections {
			fmt.Fprintf(&sb, "- **%s**\n  %s\n\n", c.Title, oneLine(c.Content))
		}
	}
	if len(b.NewConventions) > 0 {
		sb.WriteString("## Conventions added since\n\n")
		for _, c := range b.NewConventions {
			fmt.Fprintf(&sb, "- **%s** — %s\n", c.Title, oneLine(c.Content))
		}
		sb.WriteString("\n")
	}
	if len(b.Superseded) > 0 {
		sb.WriteString("## Facts from this session that are no longer true\n\n")
		for _, s := range b.Superseded {
			fmt.Fprintf(&sb, "- ~~%s~~ (invalidated)\n", s.Original.Title)
		}
		sb.WriteString("\n")
	}
	if len(b.NewSkills) > 0 {
		sb.WriteString("## Skills learned since\n\n")
		for _, sk := range b.NewSkills {
			fmt.Fprintf(&sb, "- **%s** — %s\n", sk.Name, sk.Description)
		}
		sb.WriteString("\n")
	}

	// Original session observations.
	if len(b.Observations) > 0 {
		sb.WriteString("## What this session produced\n\n")
		for _, o := range b.Observations {
			fmt.Fprintf(&sb, "### [%s] %s\n", o.Type, o.Title)
			if o.Rationale != "" {
				fmt.Fprintf(&sb, "**Why:** %s\n\n", o.Rationale)
			}
			sb.WriteString(truncateForBudget(o.Content, budget/8))
			sb.WriteString("\n\n")
		}
	}

	text := sb.String()
	// Hard cap: if the naive render busted the budget, truncate with
	// a marker so the caller knows the block is incomplete.
	if estimateTokens(text) > budget {
		text = truncateForBudget(text, budget) + "\n\n_[replay truncated at token budget]_\n"
	}
	return strings.TrimRight(text, "\n") + "\n"
}

// --- helpers ------------------------------------------------------------

func defaultTitle(goal, id string) string {
	if strings.TrimSpace(goal) != "" {
		return goal
	}
	return "session " + id
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// truncateForBudget returns s clamped to approximately tokens worth of
// characters (4 chars/token heuristic).
func truncateForBudget(s string, tokens int) string {
	maxChars := tokens * 4
	if len(s) <= maxChars {
		return s
	}
	if maxChars <= 1 {
		return "…"
	}
	return s[:maxChars-1] + "…"
}
