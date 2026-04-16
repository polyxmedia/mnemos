// Package prewarm composes conventions, recent sessions, matching skills,
// correction-journal entries, and a file heat map into a single
// token-budgeted context block returned on session_start and on
// compaction-recovery calls to mnemos_context.
//
// The key idea: push, don't pull. LLMs don't reliably invoke memory tools
// on their own. When a session starts, Mnemos delivers the relevant
// context upfront — no second call required.
package prewarm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/safety"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// Service builds pre-warm blocks. It depends on the read-side of the
// observation store plus the session/skill readers and the safety scanner.
// Writes are not needed here, so we accept memory.Reader — narrower is
// easier to mock and harder to misuse.
type Service struct {
	obs       memory.Reader
	sessions  session.Store
	skills    skills.Store
	touches   memory.TouchStore
	scanner   *safety.Scanner
	maxTokens int
}

// Config bundles dependencies.
type Config struct {
	Observations memory.Reader
	Sessions     session.Store
	Skills       skills.Store
	Touches      memory.TouchStore
	Scanner      *safety.Scanner
	MaxTokens    int
}

// NewService constructs a pre-warm service. MaxTokens defaults to 500 —
// the research-backed sweet spot for session context (curate aggressively;
// bloat hurts agent performance).
func NewService(cfg Config) *Service {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 500
	}
	if cfg.Scanner == nil {
		cfg.Scanner = safety.NewScanner()
	}
	return &Service{
		obs:       cfg.Observations,
		sessions:  cfg.Sessions,
		skills:    cfg.Skills,
		touches:   cfg.Touches,
		scanner:   cfg.Scanner,
		maxTokens: cfg.MaxTokens,
	}
}

// Mode selects which slice of context to prioritise.
type Mode int

const (
	// ModeSessionStart: a new session is opening. Emphasise conventions
	// and recent session summaries for project continuity.
	ModeSessionStart Mode = iota

	// ModeCompactionRecovery: the agent's context was compacted and we're
	// restoring mid-session state. Emphasise current session goal, in-
	// session observations, recent decisions.
	ModeCompactionRecovery
)

// Request parameters.
type Request struct {
	Mode      Mode
	AgentID   string
	Project   string
	SessionID string
	Goal      string
	MaxTokens int
}

// Block is the composed pre-warm payload.
type Block struct {
	Text          string
	TokenEstimate int
	Sections      []Section
	SafetyReport  safety.Report
}

// Section is one labeled piece of the block.
type Section struct {
	Title string
	Body  string
}

// Build assembles the pre-warm block for a given request. The block is
// capped at Request.MaxTokens (or the service default) and each section is
// sanitised before inclusion — memory stores are an attack surface and
// we never inject raw content blindly.
func (s *Service) Build(ctx context.Context, req Request) (*Block, error) {
	budget := req.MaxTokens
	if budget <= 0 {
		budget = s.maxTokens
	}

	pipeline := s.pipeline(req.Mode)

	block := &Block{}
	var sections []sectionDraft
	for _, step := range pipeline {
		draft, err := step(ctx, s, req)
		if err != nil {
			return nil, fmt.Errorf("prewarm %s: %w", draft.title, err)
		}
		if draft.body == "" {
			continue
		}
		sections = append(sections, draft)
	}

	// Scan each section once; flag high-risk content rather than inject silently.
	var fullText strings.Builder
	used := 0
	for _, d := range sections {
		body := d.body
		rep := s.scanner.Scan(body)
		if rep.MaxRisk >= safety.RiskHigh {
			body = safety.WrapFlagged(rep.Sanitised, rep)
		} else {
			body = rep.Sanitised
		}
		if rep.MaxRisk > block.SafetyReport.MaxRisk {
			block.SafetyReport.MaxRisk = rep.MaxRisk
		}
		block.SafetyReport.Findings = append(block.SafetyReport.Findings, rep.Findings...)

		entry := fmt.Sprintf("## %s\n%s", d.title, body)
		cost := estimateTokens(entry)
		if used+cost > budget {
			// Truncate the last included section rather than drop it entirely
			// so the most important context is always present.
			remaining := budget - used
			if remaining > 128 {
				trimmed := truncateTokens(entry, remaining)
				fullText.WriteString(trimmed)
				fullText.WriteString("\n\n")
				used += estimateTokens(trimmed)
				block.Sections = append(block.Sections, Section{Title: d.title, Body: body})
			}
			break
		}
		fullText.WriteString(entry)
		fullText.WriteString("\n\n")
		used += cost
		block.Sections = append(block.Sections, Section{Title: d.title, Body: body})
	}

	block.Text = strings.TrimRight(fullText.String(), "\n")
	block.TokenEstimate = used
	return block, nil
}

// --- pipeline -----------------------------------------------------------

type sectionDraft struct {
	title string
	body  string
}

type stepFunc func(ctx context.Context, s *Service, req Request) (sectionDraft, error)

func (s *Service) pipeline(mode Mode) []stepFunc {
	switch mode {
	case ModeCompactionRecovery:
		return []stepFunc{
			stepCurrentSession,
			stepInSessionObservations,
			stepConventions,
			stepCorrections,
			stepHotFiles,
		}
	default: // ModeSessionStart
		return []stepFunc{
			stepConventions,
			stepRecentSessions,
			stepMatchingSkills,
			stepCorrections,
			stepHotFiles,
		}
	}
}

func stepConventions(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if req.Project == "" {
		return sectionDraft{title: "conventions"}, nil
	}
	list, err := s.obs.ListByProject(ctx, req.AgentID, req.Project, memory.TypeConvention, 10)
	if err != nil {
		return sectionDraft{title: "conventions"}, err
	}
	if len(list) == 0 {
		return sectionDraft{title: "conventions"}, nil
	}
	var b strings.Builder
	for _, o := range list {
		b.WriteString("- ")
		b.WriteString(o.Title)
		if strings.TrimSpace(o.Rationale) != "" {
			b.WriteString(" — ")
			b.WriteString(oneLine(o.Rationale))
		}
		b.WriteString("\n")
	}
	return sectionDraft{title: "conventions", body: strings.TrimRight(b.String(), "\n")}, nil
}

func stepRecentSessions(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	recent, err := s.sessions.Recent(ctx, req.AgentID, 5)
	if err != nil {
		return sectionDraft{title: "recent sessions"}, err
	}
	if req.Project != "" {
		filtered := recent[:0]
		for _, r := range recent {
			if r.Project == req.Project {
				filtered = append(filtered, r)
			}
		}
		recent = filtered
	}
	if len(recent) == 0 {
		return sectionDraft{title: "recent sessions"}, nil
	}
	var b strings.Builder
	for i, r := range recent {
		if i >= 3 {
			break
		}
		b.WriteString("- ")
		b.WriteString(r.StartedAt.Format("2006-01-02"))
		if r.Goal != "" {
			b.WriteString(" · ")
			b.WriteString(oneLine(r.Goal))
		}
		if r.Summary != "" {
			b.WriteString(" → ")
			b.WriteString(oneLine(r.Summary))
		}
		if r.Status != "" && r.Status != session.StatusOK {
			b.WriteString(" (")
			b.WriteString(string(r.Status))
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	return sectionDraft{title: "recent sessions", body: strings.TrimRight(b.String(), "\n")}, nil
}

func stepMatchingSkills(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if req.Goal == "" {
		return sectionDraft{title: "applicable skills"}, nil
	}
	matches, err := s.skills.Match(ctx, skills.MatchInput{
		Query:   req.Goal,
		AgentID: req.AgentID,
		Limit:   3,
	})
	if err != nil {
		return sectionDraft{title: "applicable skills"}, err
	}
	if len(matches) == 0 {
		return sectionDraft{title: "applicable skills"}, nil
	}
	var b strings.Builder
	for _, m := range matches {
		b.WriteString("- ")
		b.WriteString(m.Skill.Name)
		b.WriteString(": ")
		b.WriteString(oneLine(m.Skill.Description))
		b.WriteString("\n")
	}
	return sectionDraft{title: "applicable skills", body: strings.TrimRight(b.String(), "\n")}, nil
}

func stepCorrections(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if req.Goal == "" && req.Project == "" {
		return sectionDraft{title: "relevant corrections"}, nil
	}
	query := req.Goal
	if query == "" {
		query = req.Project
	}
	results, err := s.obs.Search(ctx, memory.SearchInput{
		Query:   query,
		Type:    memory.TypeCorrection,
		AgentID: req.AgentID,
		Project: req.Project,
		Limit:   5,
	})
	if err != nil {
		return sectionDraft{title: "relevant corrections"}, err
	}
	if len(results) == 0 {
		return sectionDraft{title: "relevant corrections"}, nil
	}
	var b strings.Builder
	for i, r := range results {
		if i >= 3 {
			break
		}
		b.WriteString("- ")
		b.WriteString(r.Observation.Title)
		b.WriteString(": ")
		b.WriteString(oneLine(r.Observation.Content))
		b.WriteString("\n")
	}
	return sectionDraft{title: "relevant corrections", body: strings.TrimRight(b.String(), "\n")}, nil
}

func stepHotFiles(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if s.touches == nil || req.Project == "" {
		return sectionDraft{title: "hot files"}, nil
	}
	hot, err := s.touches.Hot(ctx, req.AgentID, req.Project, 5)
	if err != nil {
		return sectionDraft{title: "hot files"}, err
	}
	if len(hot) == 0 {
		return sectionDraft{title: "hot files"}, nil
	}
	var b strings.Builder
	for _, h := range hot {
		b.WriteString(fmt.Sprintf("- %s (%d touches)\n", h.Path, h.TouchCount))
	}
	return sectionDraft{title: "hot files", body: strings.TrimRight(b.String(), "\n")}, nil
}

func stepCurrentSession(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if req.SessionID == "" {
		return sectionDraft{title: "current session"}, nil
	}
	sess, err := s.sessions.Get(ctx, req.SessionID)
	if err != nil {
		return sectionDraft{title: "current session"}, nil
	}
	age := time.Since(sess.StartedAt).Truncate(time.Minute)
	var b strings.Builder
	fmt.Fprintf(&b, "started %s ago in %s", age, sess.Project)
	if sess.Goal != "" {
		fmt.Fprintf(&b, "\ngoal: %s", sess.Goal)
	}
	return sectionDraft{title: "current session", body: b.String()}, nil
}

func stepInSessionObservations(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if req.SessionID == "" {
		return sectionDraft{title: "session observations"}, nil
	}
	// We piggy-back on ListByProject, then filter by session — there's no
	// ListBySession in Store yet. This is cheap for realistic session sizes.
	all, err := s.obs.ListByProject(ctx, req.AgentID, req.Project, "", 50)
	if err != nil {
		return sectionDraft{title: "session observations"}, err
	}
	var b strings.Builder
	n := 0
	for _, o := range all {
		if o.SessionID != req.SessionID {
			continue
		}
		if n >= 8 {
			break
		}
		fmt.Fprintf(&b, "- [%s] %s\n", o.Type, o.Title)
		n++
	}
	if n == 0 {
		return sectionDraft{title: "session observations"}, nil
	}
	return sectionDraft{title: "session observations", body: strings.TrimRight(b.String(), "\n")}, nil
}

// --- helpers ------------------------------------------------------------

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
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

func truncateTokens(s string, tokens int) string {
	maxChars := tokens * 4
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars-1] + "…"
}
